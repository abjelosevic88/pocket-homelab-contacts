package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/store"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/vcard"
)

// A contact in the other Pocket apps, reached as the person (apps.go):
// meetings with them in Pocket Calendar (an email of theirs is a guest),
// notes that link to their page in Pocket Notes, open tasks that link to it
// in Pocket Tasks, answers they sent to the person's forms in Pocket Forms,
// deals with them or their company in Pocket CRM, the rows about them in
// Pocket Tables, the places they rent from the person in Pocket Rentals.
// Each section stands alone: an app that is off, not linked or down never
// fails the others. docs/integrations.md lists what each app answers.

const relatedTimeout = 8 * time.Second

// RelatedSection is one app's part of GET /contacts/{id}/related.
type RelatedSection struct {
	Status  string           `json:"status"` // ok | off | error
	Detail  string           `json:"detail"`
	AppURL  string           `json:"app_url"`
	NewURL  string           `json:"new_url,omitempty"`
	PageURL string           `json:"page_url,omitempty"` // the person's own page in that app (Rentals: their tenant page)
	Items   []map[string]any `json:"items"`
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

// relatedProjects: projects that list the contact as a stakeholder and
// issues that name them, from Pocket Projects (GET /api/v1/related?url=).
func (s *Server) relatedProjects(ctx context.Context, sb *sibling, sec *RelatedSection, page string) error {
	var out struct {
		Projects []struct {
			ID         flexID  `json:"id"`
			Name       string  `json:"name"`
			Status     string  `json:"status"`
			Health     string  `json:"health"`
			TargetDate string  `json:"target_date"`
			Progress   float64 `json:"progress"`
			URL        string  `json:"url"`
		} `json:"projects"`
		Issues []struct {
			ID         flexID `json:"id"`
			Identifier string `json:"identifier"`
			Title      string `json:"title"`
			Done       bool   `json:"done"`
			DueDate    string `json:"due_date"`
			URL        string `json:"url"`
		} `json:"issues"`
	}
	if err := sb.call(ctx, http.MethodGet, "/api/v1/related?url="+url.QueryEscape(page), nil, &out); err != nil {
		return err
	}
	for _, p := range out.Projects {
		sec.Items = append(sec.Items, map[string]any{"id": "p" + string(p.ID), "kind": "project", "title": p.Name, "status": p.Status, "health": p.Health,
			"target_date": p.TargetDate, "progress": p.Progress, "url": p.URL, "done": p.Status == "completed" || p.Status == "canceled"})
	}
	for _, is := range out.Issues {
		sec.Items = append(sec.Items, map[string]any{"id": "i" + string(is.ID), "kind": "issue", "identifier": is.Identifier, "title": is.Title,
			"due": is.DueDate, "url": is.URL, "done": is.Done})
	}
	return nil
}

// relatedForms: the answers the contact sent to the person's forms in
// Pocket Forms, matched by each of their emails (GET /api/v1/responses?email=),
// newest first; spam is left out.
func (s *Server) relatedForms(ctx context.Context, sb *sibling, sec *RelatedSection, ct vcard.Contact) error {
	if len(ct.Emails) == 0 {
		sec.Detail = "add an email address to see the answers they sent"
		return nil
	}
	seen := map[string]bool{}
	for i, f := range ct.Emails {
		if i == 5 {
			break
		}
		email := strings.ToLower(strings.TrimSpace(f.Value))
		var out struct {
			Items []struct {
				ID        flexID `json:"id"`
				FormID    flexID `json:"form_id"`
				FormTitle string `json:"form_title"`
				FormEmoji string `json:"form_emoji"`
				Number    int    `json:"number"`
				Preview   string `json:"preview"`
				Status    string `json:"status"`
				CreatedAt string `json:"created_at"`
			} `json:"items"`
		}
		if err := sb.call(ctx, http.MethodGet, "/api/v1/responses?limit=20&email="+url.QueryEscape(email), nil, &out); err != nil {
			return err
		}
		for _, a := range out.Items {
			if a.Status == "spam" || seen[string(a.ID)] {
				continue
			}
			seen[string(a.ID)] = true
			sec.Items = append(sec.Items, map[string]any{"id": string(a.ID), "form_id": string(a.FormID), "title": a.FormTitle, "emoji": a.FormEmoji,
				"number": a.Number, "snippet": a.Preview, "status": a.Status, "created_at": a.CreatedAt, "url": sb.public + "/answers/" + string(a.ID)})
		}
	}
	sort.SliceStable(sec.Items, func(i, j int) bool {
		return whenOf(sec.Items[i]["created_at"].(string)).After(whenOf(sec.Items[j]["created_at"].(string)))
	})
	if len(sec.Items) > 20 {
		sec.Items = sec.Items[:20]
	}
	return nil
}

// relatedCRM: the deals with the contact in Pocket CRM, matched by each of
// their emails and, for a company card or a card with an organisation, by
// that company (GET /api/v1/lookup?email=&company=). Open deals first (the
// soonest next step first), then won and lost ones, newest first.
func (s *Server) relatedCRM(ctx context.Context, sb *sibling, sec *RelatedSection, ct vcard.Contact) error {
	company := strings.TrimSpace(ct.Org)
	if ct.Kind == "org" && company == "" {
		company = strings.TrimSpace(ct.FN)
	}
	var emails []string
	for i, f := range ct.Emails {
		if i == 5 {
			break
		}
		if e := strings.ToLower(strings.TrimSpace(f.Value)); e != "" {
			emails = append(emails, e)
		}
	}
	name := ""
	if ct.Kind != "org" {
		name = strings.TrimSpace(ct.FN)
	}
	q := url.Values{}
	for k, v := range map[string]string{"name": name, "company": company} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if len(emails) > 0 {
		q.Set("email", emails[0])
	}
	sec.NewURL = sb.public + "/deals/new"
	if len(q) > 0 {
		sec.NewURL += "?" + q.Encode()
	}
	if len(emails) == 0 && company == "" {
		sec.Detail = "add an email address or a company to see the deals with them"
		return nil
	}
	asks := []url.Values{}
	for i, e := range emails {
		v := url.Values{"email": {e}}
		if i == 0 && company != "" {
			v.Set("company", company)
		}
		asks = append(asks, v)
	}
	if len(asks) == 0 {
		asks = append(asks, url.Values{"company": {company}})
	}
	seen := map[string]bool{}
	for _, v := range asks {
		var out struct {
			Deals []struct {
				ID         flexID   `json:"id"`
				Title      string   `json:"title"`
				StageName  string   `json:"stage_name"`
				StageColor string   `json:"stage_color"`
				Status     string   `json:"status"`
				Amount     *float64 `json:"amount"`
				Currency   string   `json:"currency"`
				NextAction string   `json:"next_action"`
				NextDue    string   `json:"next_due"`
				Overdue    bool     `json:"overdue"`
				Company    string   `json:"company"`
				ClosedAt   string   `json:"closed_at"`
				UpdatedAt  string   `json:"updated_at"`
			} `json:"deals"`
			URL string `json:"url"`
		}
		if err := sb.call(ctx, http.MethodGet, "/api/v1/lookup?"+v.Encode(), nil, &out); err != nil {
			return err
		}
		base := strings.TrimRight(out.URL, "/")
		if base == "" {
			base = sb.public
		}
		for _, d := range out.Deals {
			if seen[string(d.ID)] {
				continue
			}
			seen[string(d.ID)] = true
			status := d.Status
			if status == "" {
				status = "open"
			}
			it := map[string]any{"id": string(d.ID), "title": d.Title, "stage_name": d.StageName, "stage_color": d.StageColor,
				"status": status, "currency": d.Currency, "next_action": d.NextAction, "next_due": d.NextDue,
				"overdue": d.Overdue, "company": d.Company, "closed_at": d.ClosedAt, "updated_at": d.UpdatedAt,
				"url": base + "/deals/" + string(d.ID)}
			if d.Amount != nil {
				it["amount"] = *d.Amount
			}
			sec.Items = append(sec.Items, it)
		}
	}
	str := func(m map[string]any, k string) string { v, _ := m[k].(string); return v }
	sort.SliceStable(sec.Items, func(i, j int) bool {
		a, b := sec.Items[i], sec.Items[j]
		if ao, bo := str(a, "status") == "open", str(b, "status") == "open"; ao != bo {
			return ao
		}
		if str(a, "status") == "open" {
			ad, bd := str(a, "next_due"), str(b, "next_due")
			if (ad == "") != (bd == "") {
				return bd == ""
			}
			return ad < bd
		}
		return firstNonEmpty(str(a, "closed_at"), str(a, "updated_at")) > firstNonEmpty(str(b, "closed_at"), str(b, "updated_at"))
	})
	if len(sec.Items) > 30 {
		sec.Items = sec.Items[:30]
	}
	return nil
}

// relatedTables: the rows about the contact in the person's tables in Pocket
// Tables (GET /api/v1/lookup?email=&contact=&name=): an email of theirs in an
// email field, this card picked in a contact field, or their name. The first
// ask carries the card id and the name; each further email is asked alone.
// Rows come in the order Tables gives them, each once, at most 30.
func (s *Server) relatedTables(ctx context.Context, sb *sibling, sec *RelatedSection, ct vcard.Contact, cardID int64) error {
	var emails []string
	for i, f := range ct.Emails {
		if i == 5 {
			break
		}
		if e := strings.ToLower(strings.TrimSpace(f.Value)); e != "" {
			emails = append(emails, e)
		}
	}
	first := url.Values{"contact": {strconv.FormatInt(cardID, 10)}}
	if n := strings.TrimSpace(ct.FN); n != "" {
		first.Set("name", n)
	}
	asks := []url.Values{first}
	for i, e := range emails {
		if i == 0 {
			first.Set("email", e)
			continue
		}
		asks = append(asks, url.Values{"email": {e}})
	}
	seen := map[string]bool{}
	for _, v := range asks {
		var out struct {
			Items []struct {
				ID    flexID `json:"id"`
				Title string `json:"title"`
				Table string `json:"table"`
				Base  string `json:"base"`
				Field string `json:"field"`
				URL   string `json:"url"`
			} `json:"items"`
		}
		if err := sb.call(ctx, http.MethodGet, "/api/v1/lookup?"+v.Encode(), nil, &out); err != nil {
			return err
		}
		for _, x := range out.Items {
			if x.ID == "" || seen[string(x.ID)] {
				continue
			}
			seen[string(x.ID)] = true
			link := x.URL
			if sb.public != "" {
				link = sb.public + "/r/" + string(x.ID)
			}
			sec.Items = append(sec.Items, map[string]any{"id": string(x.ID), "title": x.Title, "table": x.Table, "base": x.Base,
				"field": x.Field, "url": link})
		}
	}
	if len(sec.Items) > 30 {
		sec.Items = sec.Items[:30]
	}
	return nil
}

// relatedRentals: the leases the contact has with the person in Pocket
// Rentals (GET /api/v1/lookup?email=, one ask per email, up to 5): the place,
// active, draft or ended, the rent and what they owe now (amounts come in
// cents; balance < 0 is paid ahead). Active leases first, then drafts, then
// ended ones, latest first; each lease once.
func (s *Server) relatedRentals(ctx context.Context, sb *sibling, sec *RelatedSection, ct vcard.Contact) error {
	var emails []string
	for _, f := range ct.Emails {
		if len(emails) == 5 {
			break
		}
		if e := strings.ToLower(strings.TrimSpace(f.Value)); e != "" {
			emails = append(emails, e)
		}
	}
	if len(emails) == 0 {
		sec.Detail = "add an email address to see where they rent from you"
		return nil
	}
	seen := map[string]bool{}
	for _, e := range emails {
		var out struct {
			Tenant *struct {
				ID flexID `json:"id"`
			} `json:"tenant"`
			Leases []struct {
				ID       flexID `json:"id"`
				Place    string `json:"place"`
				Status   string `json:"status"`
				Start    string `json:"start_date"`
				End      string `json:"end_date"`
				Rent     int64  `json:"rent"`
				Currency string `json:"currency"`
				Balance  int64  `json:"balance"`
				URL      string `json:"url"`
			} `json:"leases"`
			URL string `json:"url"`
		}
		if err := sb.call(ctx, http.MethodGet, "/api/v1/lookup?"+url.Values{"email": {e}}.Encode(), nil, &out); err != nil {
			return err
		}
		if out.Tenant != nil && sec.PageURL == "" {
			sec.PageURL = out.URL
			if sb.public != "" && out.Tenant.ID != "" {
				sec.PageURL = sb.public + "/tenants/" + string(out.Tenant.ID)
			}
		}
		for _, l := range out.Leases {
			if l.ID == "" || seen[string(l.ID)] {
				continue
			}
			seen[string(l.ID)] = true
			link := l.URL
			if sb.public != "" {
				link = sb.public + "/leases/" + string(l.ID)
			}
			status := l.Status
			if status == "" {
				status = "active"
			}
			sec.Items = append(sec.Items, map[string]any{"id": string(l.ID), "place": l.Place, "status": status,
				"start_date": l.Start, "end_date": l.End, "rent": float64(l.Rent) / 100, "currency": l.Currency,
				"balance": float64(l.Balance) / 100, "url": link})
		}
	}
	rank := map[string]int{"active": 0, "draft": 1}
	str := func(m map[string]any, k string) string { v, _ := m[k].(string); return v }
	sort.SliceStable(sec.Items, func(i, j int) bool {
		a, b := sec.Items[i], sec.Items[j]
		ra, ok := rank[str(a, "status")]
		if !ok {
			ra = 2
		}
		rb, ok := rank[str(b, "status")]
		if !ok {
			rb = 2
		}
		if ra != rb {
			return ra < rb
		}
		return firstNonEmpty(str(a, "end_date"), str(a, "start_date")) > firstNonEmpty(str(b, "end_date"), str(b, "start_date"))
	})
	if len(sec.Items) > 30 {
		sec.Items = sec.Items[:30]
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
		"projects": func(c context.Context, sb *sibling, sec *RelatedSection) error {
			return s.relatedProjects(c, sb, sec, page)
		},
		"forms": func(c context.Context, sb *sibling, sec *RelatedSection) error {
			return s.relatedForms(c, sb, sec, ct)
		},
		"crm": func(c context.Context, sb *sibling, sec *RelatedSection) error {
			return s.relatedCRM(c, sb, sec, ct)
		},
		"tables": func(c context.Context, sb *sibling, sec *RelatedSection) error {
			return s.relatedTables(c, sb, sec, ct, e.ID)
		},
		"rentals": func(c context.Context, sb *sibling, sec *RelatedSection) error {
			return s.relatedRentals(c, sb, sec, ct)
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
