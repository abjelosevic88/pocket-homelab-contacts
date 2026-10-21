package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/store"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/vcard"
)

// A contact in the other Pocket apps, reached as the person (apps.go):
// meetings with them in Pocket Calendar (an email of theirs is a guest),
// notes that link to their page in Pocket Notes, open tasks that link to it
// in Pocket Tasks. Each section stands alone: an app that is off, not
// linked or down never fails the others. docs/integrations.md lists what
// each app answers.

const relatedTimeout = 8 * time.Second

// RelatedSection is one app's part of GET /contacts/{id}/related.
type RelatedSection struct {
	Status string           `json:"status"` // ok | off | error
	Detail string           `json:"detail"`
	AppURL string           `json:"app_url"`
	NewURL string           `json:"new_url,omitempty"`
	Items  []map[string]any `json:"items"`
}

// flexID reads an id another app sends as a number or a string.
type flexID string

func (f *flexID) UnmarshalJSON(b []byte) error {
	*f = flexID(strings.Trim(string(b), `"`))
	return nil
}

// siblingErr says in plain words why another app did not answer.
func siblingErr(app string, err error) string {
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Client.Timeout") {
		return app + " did not answer in time"
	}
	if strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "no such host") {
		return app + " can't be reached right now"
	}
	return app + ": " + err.Error()
}

// relatedSection connects to one app and lets fill read from it.
func (s *Server) relatedSection(ctx context.Context, u *store.User, appID string, fill func(context.Context, *sibling, *RelatedSection) error) RelatedSection {
	sec := RelatedSection{Items: []map[string]any{}}
	app, _ := pocketApp(appID)
	st, _ := s.store.GetSettings(ctx, u.ID)
	switch mode := s.appMode(st, app); {
	case mode == modeOff:
		sec.Status, sec.Detail = "off", app.Name+" is switched off in Settings → Apps"
		return sec
	case mode == modeCore && !s.core.Configured():
		sec.Status, sec.Detail = "off", "there is no Pocket Core on this box; connect "+app.Name+" directly in Settings → Apps"
		return sec
	}
	sb, err := s.siblingFor(ctx, u, app)
	if err != nil {
		sec.Status, sec.Detail = "error", err.Error()
		return sec
	}
	sec.AppURL = sb.public
	c, cancel := context.WithTimeout(ctx, relatedTimeout)
	defer cancel()
	if err := fill(c, sb, &sec); err != nil {
		sec.Status, sec.Detail, sec.Items = "error", siblingErr(app.Name, err), []map[string]any{}
		return sec
	}
	sec.Status = "ok"
	return sec
}

// calOccurrence is what Pocket Calendar answers for an event.
type calOccurrence struct {
	ID        flexID `json:"id"`
	Title     string `json:"title"`
	Start     string `json:"start"`
	End       string `json:"end"`
	AllDay    bool   `json:"all_day"`
	Organizer *struct {
		Email string `json:"email"`
	} `json:"organizer"`
	Attendees []struct {
		Email string `json:"email"`
	} `json:"attendees"`
}

// whenOf reads a Pocket Calendar time (RFC 3339 or an all-day date).
func whenOf(v string) time.Time {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t
	}
	return time.Time{}
}

func (s *Server) calendarItem(sb *sibling, o calOccurrence, now time.Time) map[string]any {
	end := whenOf(o.End)
	if end.IsZero() {
		end = whenOf(o.Start)
	}
	return map[string]any{"id": string(o.ID), "title": o.Title, "start": o.Start, "end": o.End, "all_day": o.AllDay,
		"url": sb.public + "/event/" + string(o.ID), "past": !end.After(now)}
}

// relatedCalendar: events where an email of the contact is a guest (or the
// organiser), the next 10 upcoming first, then the last 10 past ones.
func (s *Server) relatedCalendar(ctx context.Context, sb *sibling, sec *RelatedSection, ct vcard.Contact) error {
	if len(ct.Emails) == 0 {
		sec.Detail = "add an email address to see the meetings with them"
		return nil
	}
	now := time.Now()
	seen := map[string]bool{}
	var upcoming, past []map[string]any
	for i, f := range ct.Emails {
		if i == 5 {
			break
		}
		email := strings.ToLower(strings.TrimSpace(f.Value))
		var found []calOccurrence
		if err := sb.call(ctx, http.MethodGet, "/api/v1/search?q="+url.QueryEscape(email), nil, &found); err != nil {
			return err
		}
		for _, o := range found {
			guest := o.Organizer != nil && strings.EqualFold(o.Organizer.Email, email)
			for _, a := range o.Attendees {
				guest = guest || strings.EqualFold(a.Email, email)
			}
			if !guest || seen[string(o.ID)] {
				continue
			}
			seen[string(o.ID)] = true
			it := s.calendarItem(sb, o, now)
			if it["past"] == true {
				past = append(past, it)
			} else {
				upcoming = append(upcoming, it)
			}
		}
	}
	sort.SliceStable(upcoming, func(i, j int) bool {
		return whenOf(upcoming[i]["start"].(string)).Before(whenOf(upcoming[j]["start"].(string)))
	})
	sort.SliceStable(past, func(i, j int) bool {
		return whenOf(past[i]["start"].(string)).After(whenOf(past[j]["start"].(string)))
	})
	if len(upcoming) > 10 {
		upcoming = upcoming[:10]
	}
	if len(past) > 10 {
		past = past[:10]
	}
	sec.Items = append(append(sec.Items, upcoming...), past...)
	return nil
}

// relatedNotes: notes that link to the contact's page (Pocket Notes' mentions).
func (s *Server) relatedNotes(ctx context.Context, sb *sibling, sec *RelatedSection, ct vcard.Contact, page string) error {
	name := ct.DisplayName()
	q := url.Values{"title": {name}, "url": {page}, "label": {name}, "folder": {"People"}}
	sec.NewURL = sb.public + "/new?" + q.Encode()
	var notes []struct {
		ID        flexID `json:"id"`
		Title     string `json:"title"`
		Excerpt   string `json:"excerpt"`
		UpdatedAt string `json:"updated_at"`
	}
	if err := sb.call(ctx, http.MethodGet, "/api/v1/mentions?limit=20&url="+url.QueryEscape(page), nil, &notes); err != nil {
		return err
	}
	for _, n := range notes {
		sec.Items = append(sec.Items, map[string]any{"id": string(n.ID), "title": n.Title, "snippet": n.Excerpt,
			"updated_at": n.UpdatedAt, "url": sb.public + "/note/" + string(n.ID)})
	}
	return nil
}

// taskJSON is what Pocket Tasks answers for a task.
type taskJSON struct {
	ID     flexID `json:"id"`
	Title  string `json:"title"`
	Notes  string `json:"notes"`
	URL    string `json:"url"`
	Due    string `json:"due"`
	Status string `json:"status"`
}

func taskItem(sb *sibling, t taskJSON) map[string]any {
	return map[string]any{"id": string(t.ID), "title": t.Title, "due": t.Due, "done": t.Status == "done" || t.Status == "cancelled",
		"url": sb.public + "/today?task=" + string(t.ID)}
}

// pageKey is an address compared without scheme, query or trailing slash.
func pageKey(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Host) + strings.TrimRight(u.EscapedPath(), "/")
}

// mentionsPage reports whether a task links to the contact's page (its
// url field, or the address in its notes).
func mentionsPage(t taskJSON, page string) bool {
	want := pageKey(page)
	if want == "" {
		return false
	}
	if pageKey(t.URL) == want {
		return true
	}
	for _, f := range strings.FieldsFunc(t.Notes, func(r rune) bool { return strings.ContainsRune(" \n\t()<>[]\"'", r) }) {
		if k := pageKey(strings.TrimRight(f, ".,;:!?")); k == want {
			return true
		}
	}
	return false
}

// relatedTasks: open tasks that link to the contact's page. Pocket Tasks
// filters by url= once it supports it (docs/integrations.md); until then it
// sends the open tasks and they are filtered here.
func (s *Server) relatedTasks(ctx context.Context, sb *sibling, sec *RelatedSection, page string) error {
	var out struct {
		Tasks []taskJSON `json:"tasks"`
	}
	if err := sb.call(ctx, http.MethodGet, "/api/v1/tasks?view=all&status=open&limit=500&url="+url.QueryEscape(page), nil, &out); err != nil {
		return err
	}
	var items []taskJSON
	for _, t := range out.Tasks {
		if mentionsPage(t, page) {
			items = append(items, t)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i].Due, items[j].Due
		return a != "" && (b == "" || a < b)
	})
	for _, t := range items {
		sec.Items = append(sec.Items, taskItem(sb, t))
	}
	return nil
}

func (s *Server) handleRelated(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	e, _, _, err := s.loadContact(ctx, u.ID, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "contact")
		return
	}
	card, err := vcard.ParseOne([]byte(e.VCF))
	if err != nil {
		s.fail(w, err, "contact")
		return
	}
	ct := card.Contact()
	page := s.contactURL(r, e.ID)
	out := map[string]RelatedSection{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for id, fill := range map[string]func(context.Context, *sibling, *RelatedSection) error{
		"calendar": func(c context.Context, sb *sibling, sec *RelatedSection) error {
			return s.relatedCalendar(c, sb, sec, ct)
		},
		"notes": func(c context.Context, sb *sibling, sec *RelatedSection) error {
			return s.relatedNotes(c, sb, sec, ct, page)
		},
		"tasks": func(c context.Context, sb *sibling, sec *RelatedSection) error {
			return s.relatedTasks(c, sb, sec, page)
		},
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sec := s.relatedSection(ctx, u, id, fill)
			mu.Lock()
			out[id] = sec
			mu.Unlock()
		}()
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, out)
}

// siblingOrFail connects to another app for an action, answering 502 when it can't.
func (s *Server) siblingOrFail(w http.ResponseWriter, r *http.Request, appID string) *sibling {
	app, _ := pocketApp(appID)
	sb, err := s.siblingFor(r.Context(), userFrom(r.Context()), app)
	if err != nil {
		writeErr(w, http.StatusBadGateway, app.Name+": "+err.Error())
		return nil
	}
	return sb
}

// handleRelatedTask adds a to-do about the contact in Pocket Tasks.
func (s *Server) handleRelatedTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	e, _, _, err := s.loadContact(ctx, me(r), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "contact")
		return
	}
	var in struct {
		Title string `json:"title"`
		Due   string `json:"due"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		in.Title = "Call " + e.FN
	}
	if in.Due != "" {
		if _, err := time.Parse("2006-01-02", in.Due); err != nil {
			writeErr(w, http.StatusBadRequest, "due looks like 2026-10-23")
			return
		}
	}
	sb := s.siblingOrFail(w, r, "tasks")
	if sb == nil {
		return
	}
	page := s.contactURL(r, e.ID)
	body := map[string]any{"title": in.Title, "url": page, "notes": "About [" + e.FN + "](" + page + ")"}
	if in.Due != "" {
		body["due"] = in.Due
	}
	c, cancel := context.WithTimeout(ctx, relatedTimeout)
	defer cancel()
	var t taskJSON
	if err := sb.call(c, http.MethodPost, "/api/v1/tasks", body, &t); err != nil {
		writeErr(w, http.StatusBadGateway, siblingErr("Pocket Tasks", err))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"item": taskItem(sb, t)})
}

// handleRelatedEvent adds an event in Pocket Calendar with the contact's
// first email invited.
func (s *Server) handleRelatedEvent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	e, _, _, err := s.loadContact(ctx, me(r), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "contact")
		return
	}
	var in struct {
		Title       string `json:"title"`
		Start       string `json:"start"`
		DurationMin int    `json:"duration_min"`
		Location    string `json:"location"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.DurationMin <= 0 {
		in.DurationMin = 60
	}
	var start, end string
	if t, err := time.Parse(time.RFC3339, in.Start); err == nil {
		start, end = in.Start, t.Add(time.Duration(in.DurationMin)*time.Minute).Format(time.RFC3339)
	} else if t, ok := localTime(in.Start); ok {
		start, end = t.Format("2006-01-02T15:04"), t.Add(time.Duration(in.DurationMin)*time.Minute).Format("2006-01-02T15:04")
	} else {
		writeErr(w, http.StatusBadRequest, "start looks like 2026-10-22T10:00")
		return
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "Meeting with " + e.FN
	}
	sb := s.siblingOrFail(w, r, "calendar")
	if sb == nil {
		return
	}
	body := map[string]any{"title": title, "start": start, "end": end, "location": in.Location,
		"description": "With " + e.FN + " — " + s.contactURL(r, e.ID)}
	if card, err := vcard.ParseOne([]byte(e.VCF)); err == nil {
		if ems := card.Contact().Emails; len(ems) > 0 {
			body["attendees"] = []map[string]string{{"email": ems[0].Value, "name": e.FN}}
		}
	}
	c, cancel := context.WithTimeout(ctx, relatedTimeout)
	defer cancel()
	var o calOccurrence
	if err := sb.call(c, http.MethodPost, "/api/v1/events", body, &o); err != nil {
		writeErr(w, http.StatusBadGateway, siblingErr("Pocket Calendar", err))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"item": s.calendarItem(sb, o, time.Now())})
}

// localTime reads a wall-clock time without a zone ("2026-10-22T10:00[:00]").
func localTime(v string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, strings.TrimSpace(v)); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
