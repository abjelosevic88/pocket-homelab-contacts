package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/config"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/store"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/vcard"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// moveServer is apiServer with a fake Core that knows Pocket Move's link
// tokens for alice: pcx_export (move:export) and pcx_import (move:import).
func moveServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/auth/introspect" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		var in struct{ Token string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		scope := map[string]string{"pcx_export": core.ScopeMoveExport, "pcx_import": core.ScopeMoveImport}[in.Token]
		if scope == "" {
			_, _ = w.Write([]byte(`{"active":false}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "kind": "delegated", "iss": "http://core.test", "scope": scope,
			"audience": "contacts", "app": map[string]any{"id": 9, "slug": "move", "name": "Pocket Move"},
			"user": map[string]any{"id": 1, "sub": "alice-sub", "email": "alice@example.com", "name": "Alice"}})
	}))
	t.Cleanup(fake.Close)
	for _, k := range []string{"CORE_APP_URL", "CORE_REGISTRATION_KEY", "ADMIN_EMAIL", "ADMIN_PASSWORD", "NOTIFY_URL", "OIDC_ISSUER", "DB_PATH", "DEMO_DATA"} {
		t.Setenv(k, "")
	}
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("AUTH_MODE", "local")
	t.Setenv("TZ", "Europe/Sarajevo")
	t.Setenv("CORE_URL", fake.URL)
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
	return s, s.Router()
}

func (c *apiClient) importVCF(text string) (int, ImportResult) {
	code, b, _ := c.raw("POST", "/api/v1/import", "text/vcard", []byte(text))
	var out ImportResult
	_ = json.Unmarshal(b, &out)
	if code != 200 {
		c.t.Logf("import: %d %s", code, b)
	}
	return code, out
}

// contactsOf lists a person's own contacts as "book/name".
func contactsOf(t *testing.T, s *Server, uid int64) []string {
	t.Helper()
	ctx := context.Background()
	own, _ := s.store.OwnAddressBooks(ctx, uid)
	out := []string{}
	for _, b := range own {
		rows, _ := s.store.ContactsInBooks(ctx, []int64{b.ID}, true)
		for _, r := range rows {
			card, _ := vcard.ParseOne([]byte(r.VCF))
			out = append(out, b.Name+"/"+card.Contact().DisplayName())
		}
	}
	sort.Strings(out)
	return out
}

func TestPortableLinkScopes(t *testing.T) {
	s, h := moveServer(t)
	alice := apiPerson(t, s, h, "alice@example.com", "Alice")
	bob := apiPerson(t, s, h, "bob@example.com", "Bob")
	alice.create(vcard.Contact{Given: "Ana", Family: "Kovač"})
	// bob shares his book with alice: it is his, not hers to move
	bob.create(vcard.Contact{Given: "Bobs", Family: "Friend"})
	alice.json("GET", "/api/v1/addressbooks", nil, 200, nil)
	bob.json("POST", fmt.Sprintf("/api/v1/addressbooks/%d/shares", firstBook(bob).ID), map[string]any{"user_id": alice.user.ID, "role": "read"}, 200, nil)
	if code, b, _ := alice.raw("GET", "/api/v1/export.vcf", "", nil); code != 200 || !strings.Contains(string(b), "Bobs") {
		t.Fatalf("alice's own export shows what she sees: %d", code)
	}
	exp := &apiClient{t: t, h: h, token: "pcx_export"}
	imp := &apiClient{t: t, h: h, token: "pcx_import"}

	var desc core.Portable
	exp.json("GET", core.PortablePath, nil, 200, &desc)
	if len(desc.Exports) != 1 || desc.Exports[0].Kind != core.KindVCard || len(desc.Imports) != 1 || desc.Imports[0].Kind != core.KindVCard || desc.Imports[0].ContentType != "text/vcard" {
		t.Fatalf("description: %+v", desc)
	}
	code, b, _ := exp.raw("GET", "/api/v1/export.vcf", "", nil)
	if code != 200 || !strings.Contains(string(b), "Ana") || strings.Contains(string(b), "Bobs") {
		t.Errorf("link export: %d %s", code, b)
	}
	// move:export can't bring anything in or reach anything else
	if code, _, _ := exp.raw("POST", "/api/v1/import", "text/vcard", []byte(googleVCF)); code != 403 {
		t.Errorf("export token imports: %d", code)
	}
	for _, p := range []string{"/api/v1/settings", "/api/v1/contacts", "/api/v1/users", "/api/v1/backup", "/api/v1/addressbooks/1/export.vcf"} {
		if code, _ := exp.call("GET", p, nil); code != 403 {
			t.Errorf("export token GET %s: %d", p, code)
		}
	}
	// move:import reads the description and posts the import, nothing else
	if code, _ := imp.call("GET", core.PortablePath, nil); code != 200 {
		t.Errorf("import token description: %d", code)
	}
	for _, p := range []string{"/api/v1/export.vcf", "/api/v1/settings", "/api/v1/contacts"} {
		if code, _ := imp.call("GET", p, nil); code != 403 {
			t.Errorf("import token GET %s: %d", p, code)
		}
	}
	if code, _ := imp.call("POST", "/api/v1/contacts", map[string]any{"card": vcard.Contact{Given: "nope"}}); code != 403 {
		t.Errorf("import token creates a contact: %d", code)
	}
	if code, _ := imp.call("PUT", "/api/v1/settings", map[string]any{}); code != 403 {
		t.Errorf("import token settings: %d", code)
	}
	code, out := imp.importVCF(googleVCF)
	// her Ana is matched by name and gains the email, phone, photo and labels
	if code != 200 || out.Created != 3 || out.Merged != 1 {
		t.Fatalf("link import: %d %+v", code, out)
	}
	if got := contactsOf(t, s, bob.user.ID); len(got) != 1 {
		t.Errorf("bob: %v", got)
	}
	if got := contactsOf(t, s, alice.user.ID); len(got) != 4 {
		t.Errorf("alice: %v", got)
	}
}

var photoB64 = base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nnot really"))

// a vCard 3.0 and a 4.0 card with photos, an Apple group, one without a UID.
var googleVCF = "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:g-ana\r\nFN:Ana Kovač\r\nN:Kovač;Ana;;;\r\nEMAIL;TYPE=INTERNET:ana@example.com\r\nTEL;TYPE=CELL:+387 65 111 222\r\n" +
	"CATEGORIES:Family,Friends\r\nPHOTO;ENCODING=b;TYPE=PNG:" + photoB64 + "\r\nEND:VCARD\r\n" +
	"BEGIN:VCARD\r\nVERSION:4.0\r\nUID:urn:uuid:4d1d0d3e-0000-4000-8000-000000000001\r\nFN:Marko Petrović\r\nN:Petrović;Marko;;;\r\nEMAIL;TYPE=work:marko@example.com\r\n" +
	"BDAY:19880412\r\nPHOTO:data:image/png;base64," + photoB64 + "\r\nEND:VCARD\r\n" +
	"BEGIN:VCARD\r\nVERSION:3.0\r\nUID:grp-1\r\nFN:Book club\r\nN:Book club;;;;\r\nX-ADDRESSBOOKSERVER-KIND:group\r\nX-ADDRESSBOOKSERVER-MEMBER:urn:uuid:g-ana\r\nEND:VCARD\r\n" +
	"BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Plumber\r\nN:Plumber;;;;\r\nEND:VCARD\r\n"

func TestImportVCardTwice(t *testing.T) {
	s, h := apiServer(t)
	alice := apiPerson(t, s, h, "alice@example.com", "Alice")
	bob := apiPerson(t, s, h, "bob@example.com", "Bob")
	// already here under another UID: matched by name and email, gains the phone
	alice.create(vcard.Contact{Given: "Marko", Family: "Petrović", Emails: []vcard.Field{{Label: "home", Value: "marko@example.com"}}})

	code, out := alice.importVCF(googleVCF)
	if code != 200 || out.Created != 3 || out.Merged != 1 || out.Failed != 0 {
		t.Fatalf("first: %d %+v", code, out)
	}
	book := firstBook(alice).Name
	want := book + "/Ana Kovač," + book + "/Book club," + book + "/Marko Petrović," + book + "/Plumber"
	if got := strings.Join(contactsOf(t, s, alice.user.ID), ","); got != want {
		t.Errorf("after first:\n got %s\nwant %s", got, want)
	}
	var list struct {
		Contacts []contactJSON `json:"contacts"`
	}
	alice.json("GET", "/api/v1/contacts?limit=50", nil, 200, &list)
	for _, c := range list.Contacts {
		switch c.FN {
		case "Ana Kovač":
			if !c.HasPhoto || strings.Join(c.Categories, ",") != "Family,Friends" || len(c.Phones) != 1 {
				t.Errorf("ana: %+v", c)
			}
		case "Marko Petrović":
			if !c.HasPhoto || c.Birthday == "" {
				t.Errorf("marko (merged): %+v", c)
			}
		case "Book club":
			if c.Card.Kind != "group" || len(c.Card.Members) != 1 {
				t.Errorf("group: %+v", c.Card)
			}
		}
	}

	// the same file again changes nothing
	code, out = alice.importVCF(googleVCF)
	if code != 200 || out.Created != 0 || out.Merged != 0 || out.Skipped != 4 {
		t.Fatalf("second: %d %+v", code, out)
	}
	if got := strings.Join(contactsOf(t, s, alice.user.ID), ","); got != want {
		t.Errorf("after second: %s", got)
	}

	// bob gets his own; alice's stay as they are
	if code, out := bob.importVCF(googleVCF); code != 200 || out.Created != 4 {
		t.Fatalf("bob: %d %+v", code, out)
	}
	if got := len(contactsOf(t, s, alice.user.ID)); got != 4 {
		t.Errorf("alice after bob: %d", got)
	}

	// alice's export comes back in carol without changes, and twice is once
	code, b, _ := alice.raw("GET", "/api/v1/export.vcf", "", nil)
	if code != 200 {
		t.Fatalf("export: %d", code)
	}
	carol := apiPerson(t, s, h, "carol@example.com", "Carol")
	for i, created := range []int{4, 0} {
		if code, out := carol.importVCF(string(b)); code != 200 || out.Created != created {
			t.Fatalf("carol run %d: %d %+v", i, code, out)
		}
	}
	if got := len(contactsOf(t, s, carol.user.ID)); got != 4 {
		t.Errorf("carol: %d", got)
	}
}
