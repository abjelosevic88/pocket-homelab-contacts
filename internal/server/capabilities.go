package server

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/store"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/vcard"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// What Pocket Search, Pocket Day and Pocket Hub ask Contacts (the
// capabilities value in links.go): one route each, in the shape every Pocket
// app answers in. They read what /search and /birthdays read, as the
// request's person: their own address books and the shared ones they have
// not hidden. Nothing here writes.

// widgetDays is how far ahead the widget looks: the "next 30 days" of the
// sidebar's Birthdays count.
const widgetDays = 30

// dots joins the parts of a line that are there: "Acme · Designer · ana@example.com".
func dots(parts ...string) string {
	out := parts[:0:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// counted is a number with its word: "1 birthday", "2 anniversaries".
func counted(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// andList: "Ana", "Ana and Marko", "Ana, Marko and Iva", "Ana, Marko and 3 more".
func andList(names []string) string {
	switch n := len(names); {
	case n == 0:
		return ""
	case n == 1:
		return names[0]
	case n > 3:
		return names[0] + ", " + names[1] + " and " + strconv.Itoa(n-2) + " more"
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// ---------- search ----------

// reachOf is one way to reach a contact: the email or the phone number the
// words found, else the first email, else the first phone. byPhone says the
// words were a number that one of the phones has.
func reachOf(e *store.ContactRow, p cardParts, q string) (reach string, byPhone bool) {
	if lq := strings.ToLower(q); lq != "" {
		for _, m := range p.emails {
			if strings.Contains(strings.ToLower(m), lq) {
				return m, false
			}
		}
	}
	// as the search itself reads a number: "065 123" finds +387 65 123…
	if d := strings.TrimPrefix(vcard.Digits(q), "+"); len(d) >= 3 {
		if short := strings.TrimLeft(d, "0"); len(short) >= 3 {
			d = short
		}
		for _, ph := range p.phones {
			if strings.Contains(vcard.Digits(ph), d) {
				return ph, true
			}
		}
	}
	for _, v := range append(append([]string{}, p.emails...), p.phones...) {
		if v != "" && v != e.FN { // a contact named by its email has it in the title
			return v, false
		}
	}
	return "", false
}

// contactSnippet is the line under a found contact: where they work and one
// way to reach them, as the contact list shows it ("Acme · Designer ·
// ana@example.com"). When the words are in none of that nor in the name, it
// also says where they were found: another email, the nickname, a label, an
// address or the note.
func contactSnippet(e *store.ContactRow, q string) string {
	p := contactParts(e)
	reach, byPhone := reachOf(e, p, q)
	org := e.Org
	if e.Kind == "org" || nameKey(org) == nameKey(e.FN) {
		org = "" // a company's card has its name in the title
	}
	line := dots(org, e.Title, reach)
	if byPhone {
		return line
	}
	shown := nameKey(strings.Join([]string{e.FN, e.Given, e.Family, e.Org, line}, " "))
	var hidden [][2]string // what it is, the text
	for _, m := range p.emails {
		hidden = append(hidden, [2]string{"", m})
	}
	hidden = append(hidden, [2]string{"Nickname", e.Nickname})
	for _, c := range e.Categories {
		hidden = append(hidden, [2]string{"Label", c})
	}
	if c, err := vcard.ParseOne([]byte(e.VCF)); err == nil {
		ct := c.Contact()
		for _, a := range ct.Addresses {
			hidden = append(hidden, [2]string{"Address", strings.Join(nonBlank(a.Street, a.Postcode, a.City, a.Region, a.Country), ", ")})
		}
		note := strings.Join(strings.Fields(ct.Note), " ")
		if r := []rune(note); len(r) > 140 {
			note = strings.TrimSpace(string(r[:139])) + "…"
		}
		hidden = append(hidden, [2]string{"Note", note})
	}
	for _, w := range strings.Fields(nameKey(q)) {
		if strings.Contains(shown, w) {
			continue
		}
		for _, h := range hidden {
			if !strings.Contains(nameKey(h[1]), w) {
				continue
			}
			if h[0] == "" {
				return dots(line, h[1])
			}
			return dots(line, h[0]+": "+h[1])
		}
	}
	return line
}

// nonBlank keeps the values that say something.
func nonBlank(v ...string) []string {
	out := v[:0:0]
	for _, s := range v {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// pocketSearch answers Pocket Search: the contacts /search finds, in its
// order (a name that starts with the words first).
func (s *Server) pocketSearch(r *http.Request, q string, limit int) ([]core.SearchItem, error) {
	ctx := r.Context()
	rows, err := s.searchContacts(ctx, actorID(ctx), q, limit)
	if err != nil {
		return nil, err
	}
	out := make([]core.SearchItem, 0, len(rows))
	for _, e := range rows {
		id := strconv.FormatInt(e.ID, 10)
		out = append(out, core.SearchItem{ID: id, Title: e.FN, Snippet: contactSnippet(e, q), Kind: "contact", URL: "/contacts/" + id})
	}
	return out, nil
}

// ---------- birthdays in words ----------

// occasionTitle names a birthday or an anniversary: "Ana Petrović's birthday".
func occasionTitle(it Upcoming) string {
	return it.Contact.FN + "'s " + it.Kind
}

// occasionYears says what the day marks when the year is known: "turns 40",
// "10 years together". Nothing for the day itself (0 years).
func occasionYears(it Upcoming) string {
	switch {
	case it.Age == nil || *it.Age < 1:
		return ""
	case it.Kind == "anniversary":
		return counted(*it.Age, "year", "years") + " together"
	}
	return fmt.Sprintf("turns %d", *it.Age)
}

// whenWords: "today", "tomorrow", "in 5 days".
func whenWords(days int) string {
	switch days {
	case 0:
		return "today"
	case 1:
		return "tomorrow"
	}
	return fmt.Sprintf("in %d days", days)
}

// shortName is what a person is called in a line: the given name, else the whole name.
func shortName(it Upcoming) string {
	return firstNonEmpty(strings.TrimSpace(it.Contact.Given), it.Contact.FN)
}

// ---------- day ----------

// pocketDay answers Pocket Day: the birthdays and anniversaries that fall on
// that day, as /birthdays works them out (29 February falls on the 28th in
// other years). A date before the year on the card is left out: nobody has a
// birthday before they were born. Birthdays are plain dates, so the time
// zone is not used.
func (s *Server) pocketDay(r *http.Request, date string, _ *time.Location) (*core.Day, error) {
	ctx := r.Context()
	at, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, err
	}
	list, err := s.upcoming(ctx, actorID(ctx), at, 1)
	if err != nil {
		return nil, err
	}
	day := &core.Day{Date: date}
	birthdays, anniversaries := 0, 0
	for _, it := range list {
		if y, _, _, _ := splitDate(it.Original); y > at.Year() {
			continue
		}
		id := strconv.FormatInt(it.Contact.ID, 10)
		day.Items = append(day.Items, core.DayItem{ID: it.Kind + ":" + id, Title: occasionTitle(it), Sub: occasionYears(it), Kind: it.Kind, URL: "/contacts/" + id})
		if it.Kind == "anniversary" {
			anniversaries++
		} else {
			birthdays++
		}
	}
	if len(day.Items) == 0 {
		return nil, nil
	}
	var parts []string
	if birthdays > 0 {
		parts = append(parts, counted(birthdays, "birthday", "birthdays"))
	}
	if anniversaries > 0 {
		parts = append(parts, counted(anniversaries, "anniversary", "anniversaries"))
	}
	day.Summary = strings.Join(parts, ", ")
	return day, nil
}

// ---------- widget ----------

// birthdayLine says where things stand: "Ana's birthday is today",
// "Birthdays today: Ana and Marko", else the next one: "Next: Marko in 5 days".
// list is soonest first and not empty.
func birthdayLine(list []Upcoming) string {
	var today []Upcoming
	for _, it := range list {
		if it.Days == 0 {
			today = append(today, it)
		}
	}
	// a birthday needs only the name under "Birthdays"; an anniversary says what it is
	name := func(it Upcoming) string {
		if it.Kind == "anniversary" {
			return shortName(it) + "'s anniversary"
		}
		return shortName(it)
	}
	if len(today) == 1 {
		return shortName(today[0]) + "'s " + today[0].Kind + " is today"
	}
	if len(today) > 1 {
		var names, mixed []string
		kinds := map[string]bool{}
		for _, it := range today {
			kinds[it.Kind] = true
			names = append(names, shortName(it))
			mixed = append(mixed, shortName(it)+"'s "+it.Kind)
		}
		switch {
		case len(kinds) > 1:
			return "Today: " + andList(mixed)
		case kinds["anniversary"]:
			return "Anniversaries today: " + andList(names)
		}
		return "Birthdays today: " + andList(names)
	}
	var next []string
	for _, it := range list {
		if it.Days == list[0].Days {
			next = append(next, name(it))
		}
	}
	return "Next: " + andList(next) + " " + whenWords(list[0].Days)
}

// pocketWidget answers Pocket Hub: the birthdays and anniversaries of the
// next 30 days, the soonest first (the Birthdays page, cut to a month).
// With none in sight there is nothing to show.
func (s *Server) pocketWidget(r *http.Request) (*core.WidgetData, error) {
	ctx := r.Context()
	uid := actorID(ctx)
	list, err := s.upcoming(ctx, uid, time.Now().In(s.loc(ctx, uid)), widgetDays)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	w := &core.WidgetData{Line: birthdayLine(list), URL: "/birthdays"}
	for _, it := range list {
		id := strconv.FormatInt(it.Contact.ID, 10)
		what := occasionYears(it)
		if it.Kind == "anniversary" && what == "" {
			what = "anniversary"
		}
		w.Items = append(w.Items, core.WidgetItem{ID: it.Kind + ":" + id, Title: it.Contact.FN, Detail: dots(whenWords(it.Days), what), URL: "/contacts/" + id})
	}
	return w, nil
}
