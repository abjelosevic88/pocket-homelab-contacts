package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/config"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/store"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/vcard"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// fakeCapCore is a Pocket Core that vouches for link tokens
// pcx_<alice|bob>_<search|day|widget|capture|read|write> from Pocket Hub
// and remembers the manifest Contacts registers with.
type fakeCapCore struct {
	*httptest.Server
	mu       sync.Mutex
	manifest map[string]any
}

// capServer is apiServer with that Core behind it.
func capServer(t *testing.T) (*Server, http.Handler, *fakeCapCore) {
	t.Helper()
	fc := &fakeCapCore{}
	scopes := map[string]string{"search": core.ScopeSearch, "day": core.ScopeDay, "widget": core.ScopeWidget, "capture": core.ScopeCapture,
		"read": "contacts:read", "write": "contacts:write"}
	fc.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/register":
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			fc.mu.Lock()
			fc.manifest = m
			fc.mu.Unlock()
			_, _ = io.WriteString(w, `{}`)
		case "/api/v1/auth/introspect":
			var in struct{ Token string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			parts := strings.Split(in.Token, "_")
			if len(parts) != 3 || parts[0] != "pcx" || scopes[parts[2]] == "" {
				_, _ = io.WriteString(w, `{"active":false}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "kind": "delegated", "iss": "http://core.test", "audience": "contacts", "scope": scopes[parts[2]],
				"user": map[string]any{"id": 1, "sub": parts[1] + "-sub", "email": parts[1] + "@example.com", "name": parts[1]},
				"app":  map[string]any{"id": 9, "slug": "hub", "name": "Pocket Hub"}})
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(fc.Close)
	for _, k := range []string{"CORE_APP_URL", "ADMIN_EMAIL", "ADMIN_PASSWORD", "NOTIFY_URL", "OIDC_ISSUER", "DB_PATH", "DEMO_DATA"} {
		t.Setenv(k, "")
	}
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("AUTH_MODE", "local")
	t.Setenv("TZ", "Europe/Sarajevo")
	t.Setenv("CORE_URL", fc.URL)
	t.Setenv("CORE_REGISTRATION_KEY", "key")
	t.Setenv("BASE_URL", "https://contacts.example")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(cfg.DBPath, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := New(cfg, st, log, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	return s, s.Router(), fc
}

// capItem is a row of a capability answer (search, day or widget).
type capItem struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
	Sub     string `json:"sub"`
	Detail  string `json:"detail"`
	Kind    string `json:"kind"`
	Date    string `json:"date"`
	Time    string `json:"time"`
	URL     string `json:"url"`
}

// capAnswer is any of the three answers.
type capAnswer struct {
	Date    string           `json:"date"`
	Summary string           `json:"summary"`
	Line    string           `json:"line"`
	URL     string           `json:"url"`
	Items   []capItem        `json:"items"`
	Metrics []map[string]any `json:"metrics"`
}

func (c *apiClient) pocket(path string) capAnswer {
	c.t.Helper()
	var out capAnswer
	code, b := c.call("GET", "/api/v1/pocket/"+path, nil)
	if code != 200 {
		c.t.Fatalf("GET /pocket/%s: %d %s", path, code, b)
	}
	if !strings.Contains(string(b), `"items":[`) {
		c.t.Fatalf("GET /pocket/%s: no items list in %s", path, b)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		c.t.Fatalf("GET /pocket/%s: %v: %s", path, err, b)
	}
	return out
}

func (a capAnswer) titles() string {
	var out []string
	for _, it := range a.Items {
		out = append(out, it.Title)
	}
	return strings.Join(out, ", ")
}

// item finds the row with that title.
func (a capAnswer) item(t *testing.T, title string) capItem {
	t.Helper()
	for _, it := range a.Items {
		if it.Title == title {
			return it
		}
	}
	t.Fatalf("no %q in %s", title, a.titles())
	return capItem{}
}

func fields(v ...string) []vcard.Field {
	var out []vcard.Field
	for _, s := range v {
		out = append(out, vcard.Field{Value: s})
	}
	return out
}

func idOf(c contactJSON) string { return strconv.FormatInt(c.ID, 10) }

func TestPocketSearch(t *testing.T) {
	s, h := apiServer(t)
	alice := apiPerson(t, s, h, "alice@example.com", "Alice")
	bob := apiPerson(t, s, h, "bob@example.com", "Bob")
	carol := apiPerson(t, s, h, "carol@example.com", "Carol")
	ana := alice.create(vcard.Contact{Given: "Ana", Family: "Petrović", Nickname: "Seka", Org: "Acme", Title: "Designer",
		Emails: fields("ana@example.com", "ana.p@work.example"), Phones: fields("+387 65 123 456")})
	marko := alice.create(vcard.Contact{Given: "Marko", Family: "Ilić", Phones: fields("+387 66 555 444"), Note: "Plumber,\nfixed the boiler", Categories: []string{"Family"},
		Addresses: []vcard.Address{{Street: "Kralja Petra 5", City: "Banja Luka"}}})
	acme := alice.create(vcard.Contact{Kind: "org", Org: "Acme", Emails: fields("office@acme.example")})
	bobs := bob.create(vcard.Contact{Given: "Ana", Family: "Bobić", Emails: fields("ana.b@example.com")})

	res := alice.pocket("search?q=ana")
	if len(res.Items) != 1 {
		t.Fatalf("ana: %+v", res.Items)
	}
	if it := res.Items[0]; it.ID != idOf(ana) || it.Title != "Ana Petrović" || it.Kind != "contact" || it.URL != "/contacts/"+idOf(ana) ||
		it.Snippet != "Acme · Designer · ana@example.com" {
		t.Fatalf("ana: %+v", it)
	}
	// the line shows what the words found: another email, a phone, the nickname, a label, an address, the note
	for q, want := range map[string]string{
		"work.example": "Acme · Designer · ana.p@work.example",
		"065 123":      "Acme · Designer · +387 65 123 456",
		"seka":         "Acme · Designer · ana@example.com · Nickname: Seka",
		"petrovic":     "Acme · Designer · ana@example.com",
	} {
		if got := alice.pocket("search?q=" + url.QueryEscape(q)); len(got.Items) != 1 || got.Items[0].Title != "Ana Petrović" || got.Items[0].Snippet != want {
			t.Errorf("%q: %+v, want %q", q, got.Items, want)
		}
	}
	for q, want := range map[string]string{
		"marko":      "+387 66 555 444",
		"plumber":    "+387 66 555 444 · Note: Plumber, fixed the boiler",
		"family":     "+387 66 555 444 · Label: Family",
		"banja luka": "+387 66 555 444 · Address: Kralja Petra 5, Banja Luka",
	} {
		if got := alice.pocket("search?q=" + url.QueryEscape(q)); len(got.Items) != 1 || got.Items[0].ID != idOf(marko) || got.Items[0].Snippet != want {
			t.Errorf("%q: %+v, want %q", q, got.Items, want)
		}
	}
	// a company's card does not repeat its name; the person who works there says where
	co := alice.pocket("search?q=acme")
	if len(co.Items) != 2 || co.item(t, "Acme").ID != idOf(acme) || co.item(t, "Acme").Snippet != "office@acme.example" ||
		co.item(t, "Ana Petrović").Snippet != "Acme · Designer · ana@example.com" {
		t.Fatalf("acme: %+v", co.Items)
	}
	if got := alice.pocket("search?q=a&limit=1"); len(got.Items) != 1 {
		t.Fatalf("limit 1: %+v", got.Items)
	}
	if got := alice.pocket("search?q="); len(got.Items) != 0 {
		t.Fatalf("no words: %+v", got.Items)
	}

	// the same contacts in the same order as /search, also with a shared address book
	bob.json("POST", fmt.Sprintf("/api/v1/addressbooks/%d/shares", firstBook(bob).ID), map[string]any{"user_id": alice.user.ID, "role": "read"}, 200, nil)
	same := func(c *apiClient, q string) capAnswer {
		t.Helper()
		var old struct {
			Contacts []contactJSON `json:"contacts"`
		}
		c.json("GET", "/api/v1/search?q="+url.QueryEscape(q), nil, 200, &old)
		got := c.pocket("search?limit=50&q=" + url.QueryEscape(q))
		if got.titles() != names(old.Contacts) {
			t.Fatalf("%q: /pocket/search finds %q, /search %q", q, got.titles(), names(old.Contacts))
		}
		return got
	}
	for _, q := range []string{"ana", "a", "example", "acme", "ilic", "387"} {
		same(alice, q)
	}
	if got := same(alice, "ana"); len(got.Items) != 2 || got.item(t, "Ana Bobić").URL != "/contacts/"+idOf(bobs) {
		t.Fatalf("shared with alice: %+v", got.Items)
	}
	// hidden from her list, hidden from her search
	var books []bookJSON
	alice.json("GET", "/api/v1/addressbooks", nil, 200, &books)
	for _, b := range books {
		if b.Role == "read" {
			alice.json("PUT", fmt.Sprintf("/api/v1/addressbooks/%d", b.ID), map[string]any{"visible": false}, 200, nil)
		}
	}
	if got := same(alice, "ana"); len(got.Items) != 1 {
		t.Fatalf("a hidden address book: %+v", got.Items)
	}
	// other people's contacts never appear
	if got := same(bob, "a"); got.titles() != "Ana Bobić" {
		t.Fatalf("bob: %q", got.titles())
	}
	for _, q := range []string{"ana", "acme", "marko", "plumber", "387"} {
		if got := carol.pocket("search?q=" + q); len(got.Items) != 0 {
			t.Fatalf("carol finds %q: %+v", q, got.Items)
		}
	}
	anon := &apiClient{t: t, h: h}
	if code, _ := anon.call("GET", "/api/v1/pocket/search?q=ana", nil); code != 401 {
		t.Fatalf("not signed in: %d", code)
	}
}

func TestPocketDay(t *testing.T) {
	s, h := apiServer(t)
	alice := apiPerson(t, s, h, "alice@example.com", "Alice")
	bob := apiPerson(t, s, h, "bob@example.com", "Bob")
	born := alice.create(vcard.Contact{Given: "Ana", Family: "Petrović", Birthday: "1990-06-15"})
	alice.create(vcard.Contact{Given: "No", Family: "Year", Birthday: "--06-15"})
	wed := alice.create(vcard.Contact{Given: "Wed", Family: "Ding", Anniversary: "2010-06-15"})
	alice.create(vcard.Contact{Given: "Leap", Family: "Day", Birthday: "2000-02-29"})
	alice.create(vcard.Contact{Given: "Not", Family: "Yet", Birthday: "2030-06-15"})
	alice.create(vcard.Contact{Given: "Plain", Family: "Person"})
	bob.create(vcard.Contact{Given: "Bobs", Family: "Friend", Birthday: "1980-06-15"})

	day := alice.pocket("day/2026-06-15")
	if day.Date != "2026-06-15" || day.Summary != "2 birthdays, 1 anniversary" || len(day.Items) != 3 {
		t.Fatalf("day: %+v", day)
	}
	if it := day.item(t, "Ana Petrović's birthday"); it.ID != "birthday:"+idOf(born) || it.Kind != "birthday" || it.Sub != "turns 36" || it.URL != "/contacts/"+idOf(born) || it.Time != "" {
		t.Fatalf("birthday: %+v", it)
	}
	if it := day.item(t, "No Year's birthday"); it.Kind != "birthday" || it.Sub != "" {
		t.Fatalf("without a year: %+v", it)
	}
	if it := day.item(t, "Wed Ding's anniversary"); it.ID != "anniversary:"+idOf(wed) || it.Kind != "anniversary" || it.Sub != "16 years together" || it.URL != "/contacts/"+idOf(wed) {
		t.Fatalf("anniversary: %+v", it)
	}
	// the same day in other years: the age follows, nobody has a birthday before they were born
	if d := alice.pocket("day/2031-06-15?tz=Europe/Sarajevo"); d.Summary != "3 birthdays, 1 anniversary" || d.item(t, "Not Yet's birthday").Sub != "turns 1" ||
		d.item(t, "Ana Petrović's birthday").Sub != "turns 41" {
		t.Fatalf("2031: %+v", d)
	}
	if d := alice.pocket("day/2010-06-15"); d.Summary != "2 birthdays, 1 anniversary" || d.item(t, "Wed Ding's anniversary").Sub != "" || d.item(t, "Ana Petrović's birthday").Sub != "turns 20" {
		t.Fatalf("the wedding day: %+v", d)
	}
	if d := alice.pocket("day/1989-06-15"); d.Summary != "1 birthday" || d.titles() != "No Year's birthday" {
		t.Fatalf("before they were born: %+v", d)
	}
	if d := alice.pocket("day/2011-06-15"); d.item(t, "Wed Ding's anniversary").Sub != "1 year together" {
		t.Fatalf("one year: %+v", d)
	}
	// 29 February falls on the 28th in other years, as on the Birthdays page
	if d := alice.pocket("day/2027-02-28"); d.Summary != "1 birthday" || d.item(t, "Leap Day's birthday").Sub != "turns 27" {
		t.Fatalf("28 February: %+v", d)
	}
	if d := alice.pocket("day/2028-02-28"); d.Summary != "" || len(d.Items) != 0 {
		t.Fatalf("28 February of a leap year: %+v", d)
	}
	if d := alice.pocket("day/2028-02-29"); d.item(t, "Leap Day's birthday").Sub != "turns 28" {
		t.Fatalf("29 February: %+v", d)
	}
	// a day with nothing, and somebody else's day
	if d := alice.pocket("day/2026-06-16"); d.Date != "2026-06-16" || d.Summary != "" || len(d.Items) != 0 {
		t.Fatalf("an empty day: %+v", d)
	}
	if d := bob.pocket("day/2026-06-15"); d.Summary != "1 birthday" || d.titles() != "Bobs Friend's birthday" || d.Items[0].Sub != "turns 46" {
		t.Fatalf("bob's day: %+v", d)
	}
	if code, _ := alice.call("GET", "/api/v1/pocket/day/yesterday", nil); code != 400 {
		t.Fatalf("not a date: %d", code)
	}
	// an address book shared with her counts, as in /birthdays; hidden, it does not
	bob.json("POST", fmt.Sprintf("/api/v1/addressbooks/%d/shares", firstBook(bob).ID), map[string]any{"user_id": alice.user.ID, "role": "read"}, 200, nil)
	if d := alice.pocket("day/2026-06-15"); d.Summary != "3 birthdays, 1 anniversary" || len(d.Items) != 4 || d.item(t, "Bobs Friend's birthday").Sub != "turns 46" {
		t.Fatalf("with bob's address book: %+v", d)
	}
	// /birthdays answers as before: the same ones, and the one who is not born yet
	var old []struct {
		Contact contactJSON `json:"contact"`
		Kind    string      `json:"kind"`
	}
	alice.json("GET", "/api/v1/birthdays?from=2026-06-15&days=1", nil, 200, &old)
	if len(old) != 5 {
		t.Fatalf("/birthdays: %+v", old)
	}
	for _, o := range old {
		if title := o.Contact.FN + "'s " + o.Kind; o.Contact.FN != "Not Yet" {
			alice.pocket("day/2026-06-15").item(t, title)
		}
	}
	var books []bookJSON
	alice.json("GET", "/api/v1/addressbooks", nil, 200, &books)
	for _, b := range books {
		if b.Role == "read" {
			alice.json("PUT", fmt.Sprintf("/api/v1/addressbooks/%d", b.ID), map[string]any{"visible": false}, 200, nil)
		}
	}
	if d := alice.pocket("day/2026-06-15"); d.Summary != "2 birthdays, 1 anniversary" {
		t.Fatalf("a hidden address book: %+v", d)
	}
}

func TestPocketWidget(t *testing.T) {
	s, h := apiServer(t)
	alice := apiPerson(t, s, h, "alice@example.com", "Alice")
	bob := apiPerson(t, s, h, "bob@example.com", "Bob")
	alice.create(vcard.Contact{Given: "Plain", Family: "Person"})
	// nobody with a date: nothing to show
	if w := alice.pocket("widget"); w.Line != "" || w.URL != "" || len(w.Items) != 0 || w.Metrics != nil {
		t.Fatalf("without birthdays: %+v", w)
	}
	loc, _ := time.LoadLocation("Europe/Sarajevo")
	today := time.Now().In(loc)
	// a date n days from today, in a leap year so that 29 February can be written
	on := func(n, year int) (string, int) {
		d := today.AddDate(0, 0, n)
		return fmt.Sprintf("%04d-%02d-%02d", year, d.Month(), d.Day()), d.Year() - year
	}
	noYear := func(n int) string {
		d := today.AddDate(0, 0, n)
		return fmt.Sprintf("--%02d-%02d", d.Month(), d.Day())
	}
	late, lateAge := on(29, 1988)
	far := alice.create(vcard.Contact{Given: "Lana", Family: "Late", Birthday: late})
	alice.create(vcard.Contact{Given: "Olga", Family: "Outside", Birthday: noYear(31)})
	alice.create(vcard.Contact{Given: "Yara", Family: "Yesterday", Birthday: noYear(-1)})
	five := alice.create(vcard.Contact{Given: "Marko", Family: "Ilić", Birthday: noYear(5)})
	if w := alice.pocket("widget"); w.Line != "Next: Marko in 5 days" || w.URL != "/birthdays" || w.titles() != "Marko Ilić, Lana Late" {
		t.Fatalf("the next one: %+v", w)
	}
	wedDate, wedYears := on(3, 2000)
	wed := alice.create(vcard.Contact{Given: "Wed", Family: "Ding", Anniversary: wedDate})
	alice.create(vcard.Contact{Given: "Vera", Family: "Vow", Anniversary: noYear(3)})
	if w := alice.pocket("widget"); w.Line != "Next: Vera's anniversary and Wed's anniversary in 3 days" {
		t.Fatalf("anniversaries next: %q", w.Line)
	}
	bday, age := on(0, 2000)
	tea := alice.create(vcard.Contact{Given: "Tea", Family: "Today", Birthday: bday})
	bob.create(vcard.Contact{Given: "Bobs", Family: "Friend", Birthday: noYear(1)})

	w := alice.pocket("widget")
	if w.Line != "Tea's birthday is today" || w.URL != "/birthdays" || w.Metrics != nil {
		t.Fatalf("widget: %+v", w)
	}
	want := []capItem{
		{ID: "birthday:" + idOf(tea), Title: "Tea Today", Detail: fmt.Sprintf("today · turns %d", age), URL: "/contacts/" + idOf(tea)},
		{Title: "Vera Vow", Detail: "in 3 days · anniversary"},
		{ID: "anniversary:" + idOf(wed), Title: "Wed Ding", Detail: fmt.Sprintf("in 3 days · %d years together", wedYears), URL: "/contacts/" + idOf(wed)},
		{ID: "birthday:" + idOf(five), Title: "Marko Ilić", Detail: "in 5 days", URL: "/contacts/" + idOf(five)},
		{ID: "birthday:" + idOf(far), Title: "Lana Late", Detail: fmt.Sprintf("in 29 days · turns %d", lateAge), URL: "/contacts/" + idOf(far)},
	}
	if len(w.Items) != len(want) {
		t.Fatalf("the soonest first, a month ahead: %+v", w.Items)
	}
	for i, x := range want {
		got := w.Items[i]
		if got.Title != x.Title || got.Detail != x.Detail || (x.ID != "" && (got.ID != x.ID || got.URL != x.URL)) {
			t.Errorf("row %d: %+v, want %+v", i, got, x)
		}
	}
	// the same list as the Birthdays page for the next 30 days
	var old []map[string]any
	alice.json("GET", "/api/v1/birthdays?days=30", nil, 200, &old)
	if len(old) != len(w.Items) {
		t.Fatalf("/birthdays?days=30 has %d, the widget %d", len(old), len(w.Items))
	}
	// more than fits: the eight soonest
	for i := range 6 {
		alice.create(vcard.Contact{Given: "Extra", Family: strconv.Itoa(i), Birthday: noYear(10 + i)})
	}
	if w := alice.pocket("widget"); len(w.Items) != 8 || w.Items[0].Title != "Tea Today" || w.Items[7].Title != "Extra 3" {
		t.Fatalf("eight rows: %s", w.titles())
	}
	// bob has his own, and none of hers
	if w := bob.pocket("widget"); w.Line != "Next: Bobs tomorrow" || w.titles() != "Bobs Friend" || w.Items[0].Detail != "tomorrow" {
		t.Fatalf("bob's widget: %+v", w)
	}
}

func TestBirthdayLine(t *testing.T) {
	up := func(given, kind string, days int) Upcoming {
		return Upcoming{Contact: Summary{Given: given, FN: given + " Family"}, Kind: kind, Days: days}
	}
	company := Upcoming{Contact: Summary{FN: "Acme", Kind: "org"}, Kind: "anniversary", Days: 2}
	for want, list := range map[string][]Upcoming{
		"Ana's birthday is today":                       {up("Ana", "birthday", 0), up("Marko", "birthday", 1)},
		"Ana's anniversary is today":                    {up("Ana", "anniversary", 0)},
		"Birthdays today: Ana and Marko":                {up("Ana", "birthday", 0), up("Marko", "birthday", 0), up("Iva", "birthday", 4)},
		"Birthdays today: Ana, Marko and Iva":           {up("Ana", "birthday", 0), up("Marko", "birthday", 0), up("Iva", "birthday", 0)},
		"Birthdays today: Ana, Marko and 2 more":        {up("Ana", "birthday", 0), up("Marko", "birthday", 0), up("Iva", "birthday", 0), up("Tea", "birthday", 0)},
		"Anniversaries today: Ana and Marko":            {up("Ana", "anniversary", 0), up("Marko", "anniversary", 0)},
		"Today: Ana's birthday and Marko's anniversary": {up("Ana", "birthday", 0), up("Marko", "anniversary", 0)},
		"Next: Marko tomorrow":                          {up("Marko", "birthday", 1), up("Iva", "birthday", 2)},
		"Next: Marko in 5 days":                         {up("Marko", "birthday", 5)},
		"Next: Marko and Iva in 5 days":                 {up("Marko", "birthday", 5), up("Iva", "birthday", 5), up("Tea", "birthday", 9)},
		"Next: Acme's anniversary in 2 days":            {company, up("Tea", "birthday", 9)},
	} {
		if got := birthdayLine(list); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

// A link token with one pocket:* scope reaches its one route and nothing else.
func TestPocketLinkTokens(t *testing.T) {
	s, h, _ := capServer(t)
	alice := apiPerson(t, s, h, "alice@example.com", "Alice")
	apiPerson(t, s, h, "bob@example.com", "Bob")
	loc, _ := time.LoadLocation("Europe/Sarajevo")
	today := time.Now().In(loc)
	ana := alice.create(vcard.Contact{Given: "Ana", Family: "Petrović", Emails: fields("ana@example.com"), Birthday: fmt.Sprintf("--%02d-%02d", today.Month(), today.Day())})
	date := today.Format("2006-01-02")
	line := map[string]any{"request_id": "req-0000000000000001", "text": "Marko Ilić"}
	card := map[string]any{"card": vcard.Contact{Given: "X"}}
	for _, c := range []struct {
		tok, method, path string
		body              any
		want              int
	}{
		{"pcx_alice_search", "GET", "/api/v1/pocket/search?q=ana", nil, 200},
		{"pcx_alice_day", "GET", "/api/v1/pocket/day/" + date, nil, 200},
		{"pcx_alice_day", "GET", "/api/v1/pocket/day/" + date + "?tz=Europe/Sarajevo", nil, 200},
		{"pcx_alice_widget", "GET", "/api/v1/pocket/widget", nil, 200},
		{"pcx_alice_search", "GET", "/api/v1/auth/me", nil, 200},
		{"pcx_alice_widget", "GET", "/api/v1/auth/me", nil, 200},
		// another capability's route
		{"pcx_alice_search", "GET", "/api/v1/pocket/day/" + date, nil, 403},
		{"pcx_alice_search", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/pocket/search?q=ana", nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/pocket/search?q=ana", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/pocket/day/" + date, nil, 403},
		{"pcx_alice_search", "POST", "/api/v1/pocket/search?q=ana", nil, 405},
		// a quick line is not offered here, whatever the link says: there is no such route
		{"pcx_alice_capture", "POST", "/api/v1/pocket/capture", line, 404},
		{"pcx_alice_capture", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_capture", "GET", "/api/v1/pocket/search?q=ana", nil, 403},
		{"pcx_alice_capture", "POST", "/api/v1/contacts", card, 403},
		{"pcx_alice_widget", "POST", "/api/v1/pocket/capture", line, 404},
		// the rest of the app
		{"pcx_alice_search", "GET", "/api/v1/search?q=ana", nil, 403},
		{"pcx_alice_search", "GET", "/api/v1/suggest?q=ana", nil, 403},
		{"pcx_alice_search", "GET", "/api/v1/contacts", nil, 403},
		{"pcx_alice_search", "GET", fmt.Sprintf("/api/v1/contacts/%d", ana.ID), nil, 403},
		{"pcx_alice_search", "GET", fmt.Sprintf("/api/v1/contacts/%d/vcf", ana.ID), nil, 403},
		{"pcx_alice_search", "GET", "/api/v1/export.vcf", nil, 403},
		{"pcx_alice_search", "POST", "/api/v1/contacts", card, 403},
		{"pcx_alice_day", "GET", "/api/v1/birthdays", nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/feeds", nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/activity", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/birthdays?days=30", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/addressbooks", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/settings", nil, 403},
		{"pcx_alice_widget", "PUT", fmt.Sprintf("/api/v1/contacts/%d", ana.ID), card, 403},
		{"pcx_alice_widget", "DELETE", fmt.Sprintf("/api/v1/contacts/%d", ana.ID), nil, 403},
		// the app's own scopes do not open the new routes, and still open their own
		{"pcx_alice_read", "GET", "/api/v1/pocket/search?q=ana", nil, 403},
		{"pcx_alice_read", "GET", "/api/v1/pocket/day/" + date, nil, 403},
		{"pcx_alice_read", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_write", "GET", "/api/v1/pocket/search?q=ana", nil, 403},
		{"pcx_alice_write", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_read", "GET", "/api/v1/search?q=ana", nil, 200},
		{"pcx_alice_read", "GET", "/api/v1/birthdays", nil, 200},
		{"pcx_nobody", "GET", "/api/v1/pocket/widget", nil, 401},
	} {
		link := &apiClient{t: t, h: h, token: c.tok}
		if code, b := link.call(c.method, c.path, c.body); code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%s)", c.tok, c.method, c.path, code, c.want, b)
		}
	}
	// a signed-in person has no quick line to post either, and nothing was added
	if code, _ := alice.call("POST", "/api/v1/pocket/capture", line); code != 404 {
		t.Errorf("POST /pocket/capture as a person: %d", code)
	}
	if got := contactsOf(t, s, alice.user.ID); len(got) != 1 {
		t.Fatalf("alice's contacts: %v", got)
	}
	// the token acts for its person only
	as := func(tok string) *apiClient { return &apiClient{t: t, h: h, token: tok} }
	if got := as("pcx_alice_search").pocket("search?q=ana"); got.titles() != "Ana Petrović" || got.Items[0].URL != "/contacts/"+idOf(ana) {
		t.Fatalf("alice through the link: %+v", got.Items)
	}
	if d := as("pcx_alice_day").pocket("day/" + date); d.Summary != "1 birthday" || d.titles() != "Ana Petrović's birthday" {
		t.Fatalf("alice's day through the link: %+v", d)
	}
	if w := as("pcx_alice_widget").pocket("widget"); w.Line != "Ana's birthday is today" || len(w.Items) != 1 || w.Items[0].Detail != "today" {
		t.Fatalf("alice's widget through the link: %+v", w)
	}
	if got := as("pcx_bob_search").pocket("search?q=ana"); len(got.Items) != 0 {
		t.Fatalf("bob through the link finds alice's contact: %+v", got.Items)
	}
	if d := as("pcx_bob_day").pocket("day/" + date); d.Summary != "" || len(d.Items) != 0 {
		t.Fatalf("bob's day through the link: %+v", d)
	}
	if w := as("pcx_bob_widget").pocket("widget"); w.Line != "" || len(w.Items) != 0 {
		t.Fatalf("bob's widget through the link: %+v", w)
	}
}

func TestPocketLinkScopes(t *testing.T) {
	req := func(m, p string) *http.Request { return httptest.NewRequest(m, p, nil) }
	for _, c := range []struct {
		scope string
		r     *http.Request
		want  bool
	}{
		{core.ScopeSearch, req("GET", "/api/v1/pocket/search?q=x"), true},
		{core.ScopeDay, req("GET", "/api/v1/pocket/day/2026-01-02"), true},
		{core.ScopeWidget, req("GET", "/api/v1/pocket/widget"), true},
		{core.ScopeWidget, req("GET", "/api/v1/auth/me"), true},
		{core.ScopeSearch, req("POST", "/api/v1/pocket/search"), false},
		{core.ScopeSearch, req("GET", "/api/v1/search?q=x"), false},
		{core.ScopeDay, req("GET", "/api/v1/pocket/day/today"), false},
		{core.ScopeDay, req("GET", "/api/v1/birthdays"), false},
		{core.ScopeWidget, req("GET", "/api/v1/birthdays"), false},
		{core.ScopeCapture, req("POST", "/api/v1/pocket/capture"), false},
		{core.ScopeCapture, req("POST", "/api/v1/contacts"), false},
		{"contacts:read", req("GET", "/api/v1/pocket/search?q=x"), false},
		{"contacts:read", req("GET", "/api/v1/pocket/day/2026-01-02"), false},
		{"contacts:read", req("GET", "/api/v1/pocket/widget"), false},
		{"contacts:write", req("POST", "/api/v1/pocket/capture"), false},
		{"contacts:read", req("GET", "/api/v1/search"), true},
		{"contacts:write", req("POST", "/api/v1/contacts"), true},
		{core.ScopeMoveExport, req("GET", "/api/v1/pocket/search?q=x"), false},
	} {
		if got := linkAllows([]string{c.scope}, c.r); got != c.want {
			t.Errorf("%s %s %s = %v, want %v", c.scope, c.r.Method, c.r.URL.Path, got, c.want)
		}
	}
}

// Contacts tells Core what it can do, so Search, Day and Hub find it on their own.
func TestManifestCapabilities(t *testing.T) {
	s, _, fc := capServer(t)
	if err := s.registerWithCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	fc.mu.Lock()
	m := fc.manifest
	fc.mu.Unlock()
	if m["slug"] != "contacts" || m["category"] != "Everyday" {
		t.Fatalf("manifest: %v %v", m["slug"], m["category"])
	}
	search, _ := m["search"].(map[string]any)
	day, _ := m["day"].(map[string]any)
	widget, _ := m["widget"].(map[string]any)
	if search == nil || fmt.Sprint(search["bangs"]) != "[contacts people]" || fmt.Sprint(search["kinds"]) != "[contact]" || search["what"] == "" {
		t.Fatalf("search: %v", search)
	}
	if day == nil || day["label"] != "Contacts" || day["one"] != "birthday" || day["many"] != "birthdays" || day["unit"] != "count" || day["what"] == "" {
		t.Fatalf("day: %v", day)
	}
	if widget == nil || widget["title"] != "Birthdays" || widget["what"] == "" {
		t.Fatalf("widget: %v", widget)
	}
	if m["capture"] != nil {
		t.Fatalf("a quick line is not offered: %v", m["capture"])
	}
	offered := map[string]bool{}
	for _, o := range m["offers"].([]any) {
		offered[o.(map[string]any)["scope"].(string)] = true
	}
	for _, sc := range []string{"contacts:read", "contacts:write", core.ScopeMoveExport, core.ScopeMoveImport, core.ScopeSearch, core.ScopeDay, core.ScopeWidget} {
		if !offered[sc] {
			t.Errorf("%s is not offered", sc)
		}
	}
	if offered[core.ScopeCapture] {
		t.Errorf("%s is offered", core.ScopeCapture)
	}
}
