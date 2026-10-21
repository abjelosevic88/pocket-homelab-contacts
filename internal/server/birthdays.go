package server

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/store"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/vcard"
)

// Birthdays and anniversaries: the next ones of the contacts a person sees,
// a secret calendar feed of them, and (reminders.go) a message on the day.

// dayOf is the calendar date of t in its zone, at midnight UTC (easy to compare).
func dayOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// splitDate reads "YYYY-MM-DD" or "--MM-DD" (year 0 = unknown).
func splitDate(v string) (year, month, day int, ok bool) {
	v = vcard.NormDate(v, false)
	if v == "" {
		return 0, 0, 0, false
	}
	if !strings.HasPrefix(v, "--") {
		year, _ = strconv.Atoi(v[:4])
	}
	month, _ = strconv.Atoi(v[len(v)-5 : len(v)-3])
	day, _ = strconv.Atoi(v[len(v)-2:])
	return year, month, day, month >= 1 && month <= 12 && day >= 1 && day <= 31
}

// onDate is month/day in a year; 29 February falls on the 28th in other years.
func onDate(year, month, day int) time.Time {
	if month == 2 && day == 29 && !isLeap(year) {
		day = 28
	}
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

func isLeap(y int) bool { return y%4 == 0 && (y%100 != 0 || y%400 == 0) }

// nextOccurrence is the first time a yearly date comes on or after from
// (a UTC midnight), and how many years it marks (nil without a year).
func nextOccurrence(original string, from time.Time) (time.Time, *int, bool) {
	y, m, d, ok := splitDate(original)
	if !ok {
		return time.Time{}, nil, false
	}
	at := onDate(from.Year(), m, d)
	if at.Before(from) {
		at = onDate(from.Year()+1, m, d)
	}
	if y == 0 || y > at.Year() {
		return at, nil, true
	}
	age := at.Year() - y
	return at, &age, true
}

// Upcoming is one birthday or anniversary in the list.
type Upcoming struct {
	Contact  Summary `json:"contact"`
	Kind     string  `json:"kind"` // birthday | anniversary
	Date     string  `json:"date"`
	Original string  `json:"original"`
	Age      *int    `json:"age"`
	Days     int     `json:"days"`

	row *store.ContactRow
}

// upcoming lists the birthdays and anniversaries in [from, from+days) of the
// person's visible books, soonest first.
func (s *Server) upcoming(ctx context.Context, uid int64, from time.Time, days int) ([]Upcoming, error) {
	books, err := s.store.VisibleBookIDs(ctx, uid)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.DatedContacts(ctx, books)
	if err != nil {
		return nil, err
	}
	starred := s.store.Starred(ctx, uid)
	from = dayOf(from)
	end := from.AddDate(0, 0, days)
	out := []Upcoming{}
	for _, e := range rows {
		for _, d := range []struct{ kind, v string }{{"birthday", e.Bday}, {"anniversary", e.Anniversary}} {
			if d.v == "" {
				continue
			}
			at, age, ok := nextOccurrence(d.v, from)
			if !ok || !at.Before(end) {
				continue
			}
			out = append(out, Upcoming{Contact: s.summary(e, starred[e.ID]), Kind: d.kind, Date: at.Format("2006-01-02"),
				Original: d.v, Age: age, Days: int(at.Sub(from).Hours() / 24), row: e})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		return nameKey(out[i].Contact.FN) < nameKey(out[j].Contact.FN)
	})
	return out, nil
}

func (s *Server) handleBirthdays(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	days := queryInt(r, "days")
	if days <= 0 {
		days = 60
	}
	days = min(days, 366)
	from := time.Now().In(s.loc(ctx, u.ID))
	if v := r.URL.Query().Get("from"); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "from looks like 2026-10-21")
			return
		}
		from = t
	}
	list, err := s.upcoming(ctx, u.ID, from, days)
	if err != nil {
		s.fail(w, err, "birthdays")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// ---------- the feed ----------

func (s *Server) feedRoutes(r chi.Router) {
	r.Get("/feed/birthdays/{token}", s.handleBirthdayFeed)
}

func (s *Server) feedLinks(r *http.Request, u *store.User) map[string]any {
	feed := s.appURL(r, "/feed/birthdays/"+u.FeedToken+".ics")
	webcal := strings.Replace(strings.Replace(feed, "https://", "webcal://", 1), "http://", "webcal://", 1)
	return map[string]any{"birthdays_url": feed, "birthdays_webcal": webcal}
}

func (s *Server) handleFeeds(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	if u.FeedToken == "" {
		u.FeedToken = store.RandomToken(16)
		if err := s.store.UpdateUser(ctx, u); err != nil {
			s.fail(w, err, "feed")
			return
		}
	}
	writeJSON(w, http.StatusOK, s.feedLinks(r, u))
}

func (s *Server) handleRotateFeed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	u.FeedToken = store.RandomToken(16)
	if err := s.store.UpdateUser(ctx, u); err != nil {
		s.fail(w, err, "rotate feed")
		return
	}
	writeJSON(w, http.StatusOK, s.feedLinks(r, u))
}

func (s *Server) handleBirthdayFeed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tok := strings.TrimSuffix(chi.URLParam(r, "token"), ".ics")
	u, err := s.store.GetUserByFeedToken(ctx, tok)
	if err != nil {
		time.Sleep(200 * time.Millisecond)
		http.NotFound(w, r)
		return
	}
	body, err := s.birthdayICS(ctx, u)
	if err != nil {
		http.Error(w, "feed unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=900")
	_, _ = w.Write([]byte(body))
}

// birthdayICS is a calendar with a yearly all-day event for every birthday
// and anniversary in the person's visible books.
func (s *Server) birthdayICS(ctx context.Context, u *store.User) (string, error) {
	books, err := s.store.VisibleBookIDs(ctx, u.ID)
	if err != nil {
		return "", err
	}
	rows, err := s.store.DatedContacts(ctx, books)
	if err != nil {
		return "", err
	}
	st, _ := s.store.GetSettings(ctx, 0)
	var b strings.Builder
	line := func(l string) { foldICS(&b, l) }
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//Pocket Contacts//Birthdays//EN")
	line("CALSCALE:GREGORIAN")
	line("METHOD:PUBLISH")
	line("X-WR-CALNAME:" + icsText("Birthdays · "+firstNonEmpty(st.Title, store.DefaultTitle)))
	line("X-APPLE-CALENDAR-COLOR:#e11d48")
	line("REFRESH-INTERVAL;VALUE=DURATION:PT12H")
	line("X-PUBLISHED-TTL:PT12H")
	stamp := time.Now().UTC().Format("20060102T150405Z")
	today := dayOf(time.Now().In(s.loc(ctx, u.ID)))
	for _, e := range rows {
		for _, d := range []struct{ kind, v string }{{"birthday", e.Bday}, {"anniversary", e.Anniversary}} {
			y, m, day, ok := splitDate(d.v)
			if d.v == "" || !ok {
				continue
			}
			// a date without a year starts in 2000 (a leap year, so 29 February exists)
			startYear := y
			if startYear == 0 {
				startYear = 2000
			}
			if m == 2 && day == 29 && !isLeap(startYear) {
				startYear = 2000
			}
			start := time.Date(startYear, time.Month(m), day, 0, 0, 0, 0, time.UTC)
			summary := "🎂 " + e.FN
			desc := ""
			if d.kind == "anniversary" {
				summary = "💍 " + e.FN
				if spouse := spouseOf(e); spouse != "" {
					summary += " & " + spouse
				}
			}
			if y > 0 {
				next, age, _ := nextOccurrence(d.v, today)
				word := "Born"
				if d.kind == "anniversary" {
					word = "Since"
				}
				desc = fmt.Sprintf("%s %d", word, y)
				if age != nil {
					if d.kind == "birthday" {
						desc += fmt.Sprintf(" — turns %d on %s", *age, next.Format("2 January 2006"))
					} else {
						desc += fmt.Sprintf(" — %d years on %s", *age, next.Format("2 January 2006"))
					}
				}
			}
			line("BEGIN:VEVENT")
			line("UID:" + d.kind + "-" + strconv.FormatInt(e.AddressBookID, 10) + "-" + icsText(e.UID) + "@pocket-contacts")
			line("DTSTAMP:" + stamp)
			line("DTSTART;VALUE=DATE:" + start.Format("20060102"))
			line("DTEND;VALUE=DATE:" + start.AddDate(0, 0, 1).Format("20060102"))
			if m == 2 && day == 29 {
				// the last day of February: the 29th in leap years, the 28th otherwise
				line("RRULE:FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=-1")
			} else {
				line("RRULE:FREQ=YEARLY")
			}
			line("SUMMARY:" + icsText(summary))
			if desc != "" {
				line("DESCRIPTION:" + icsText(desc))
			}
			if s.cfg.BaseURL != "" {
				line("URL:" + s.cfg.BaseURL + "/contacts/" + strconv.FormatInt(e.ID, 10))
			}
			line("CATEGORIES:" + map[string]string{"birthday": "Birthday", "anniversary": "Anniversary"}[d.kind])
			line("TRANSP:TRANSPARENT")
			line("END:VEVENT")
		}
	}
	line("END:VCALENDAR")
	return b.String(), nil
}

// spouseOf is the name of a contact's spouse or partner ("" when none).
func spouseOf(e *store.ContactRow) string {
	c, err := vcard.ParseOne([]byte(e.VCF))
	if err != nil {
		return ""
	}
	for _, r := range c.Contact().Related {
		if l := strings.ToLower(r.Label); l == "spouse" || l == "partner" || l == "husband" || l == "wife" {
			if w := strings.Fields(r.Value); len(w) > 0 {
				return w[0]
			}
		}
	}
	return ""
}

func icsText(s string) string {
	return strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`).Replace(s)
}

// foldICS writes one content line folded at 75 octets.
func foldICS(b *strings.Builder, l string) {
	for len(l) > 75 {
		cut := 75
		for cut > 0 && (l[cut]&0xC0) == 0x80 {
			cut--
		}
		b.WriteString(l[:cut] + "\r\n ")
		l = l[cut:]
	}
	b.WriteString(l + "\r\n")
}
