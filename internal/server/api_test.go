package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/config"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/store"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/vcard"
)

func apiServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("AUTH_MODE", "local")
	t.Setenv("TZ", "Europe/Sarajevo")
	t.Setenv("CORE_URL", "")
	t.Setenv("BASE_URL", "https://contacts.example")
	t.Setenv("DEMO_DATA", "")
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

type apiClient struct {
	t     *testing.T
	h     http.Handler
	token string
	user  *store.User
}

func (c *apiClient) raw(method, path, ctype string, body []byte) (int, []byte, http.Header) {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, req)
	return w.Code, w.Body.Bytes(), w.Header()
}

func (c *apiClient) call(method, path string, body any) (int, []byte) {
	var b []byte
	ct := ""
	if body != nil {
		b, _ = json.Marshal(body)
		ct = "application/json"
	}
	code, out, _ := c.raw(method, path, ct, b)
	return code, out
}

func (c *apiClient) json(method, path string, body any, want int, out any) {
	c.t.Helper()
	code, b := c.call(method, path, body)
	if code != want {
		c.t.Fatalf("%s %s: want %d, got %d: %s", method, path, want, code, b)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			c.t.Fatalf("%s %s: %v: %s", method, path, err, b)
		}
	}
}

func apiPerson(t *testing.T, s *Server, h http.Handler, email, name string) *apiClient {
	t.Helper()
	u, err := s.firstOrSystemUser(context.Background(), email, name)
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := s.store.CreateAPIToken(context.Background(), u.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	return &apiClient{t: t, h: h, token: tok, user: u}
}

type bookJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type contactJSON struct {
	ID         int64         `json:"id"`
	UID        string        `json:"uid"`
	FN         string        `json:"fn"`
	Emails     []string      `json:"emails"`
	Phones     []string      `json:"phones"`
	Birthday   string        `json:"birthday"`
	Categories []string      `json:"categories"`
	HasPhoto   bool          `json:"has_photo"`
	PhotoURL   string        `json:"photo_url"`
	Starred    bool          `json:"starred"`
	ETag       string        `json:"etag"`
	Role       string        `json:"role"`
	URL        string        `json:"url"`
	Source     string        `json:"source"`
	Card       vcard.Contact `json:"card"`
}

func firstBook(c *apiClient) bookJSON {
	var books []bookJSON
	c.json("GET", "/api/v1/addressbooks", nil, 200, &books)
	if len(books) == 0 {
		c.t.Fatal("no address book seeded")
	}
	return books[0]
}

func (c *apiClient) create(card vcard.Contact) contactJSON {
	c.t.Helper()
	var out contactJSON
	c.json("POST", "/api/v1/contacts", map[string]any{"card": card}, 201, &out)
	return out
}

func names(list []contactJSON) string {
	var out []string
	for _, c := range list {
		out = append(out, c.FN)
	}
	return strings.Join(out, ", ")
}

func TestOtherPeoplesContactsAre404(t *testing.T) {
	s, h := apiServer(t)
	alice := apiPerson(t, s, h, "alice@example.com", "Alice")
	bob := apiPerson(t, s, h, "bob@example.com", "Bob")
	carol := apiPerson(t, s, h, "carol@example.com", "Carol")
	book := firstBook(alice)
	ct := alice.create(vcard.Contact{Given: "Marko", Family: "Petrović", Emails: []vcard.Field{{Value: "marko@example.com"}}})
	var shares []store.Share
	alice.json("POST", fmt.Sprintf("/api/v1/addressbooks/%d/shares", book.ID), map[string]any{"user_id": carol.user.ID, "role": "read"}, 200, &shares)
	gone := alice.create(vcard.Contact{Given: "Gone"})
	alice.json("DELETE", fmt.Sprintf("/api/v1/contacts/%d", gone.ID), nil, 204, nil)
	var deleted []store.DeletedContact
	alice.json("GET", "/api/v1/deleted", nil, 200, &deleted)
	if len(deleted) != 1 {
		t.Fatalf("deleted: %+v", deleted)
	}

	c := fmt.Sprintf("/api/v1/contacts/%d", ct.ID)
	b := fmt.Sprintf("/api/v1/addressbooks/%d", book.ID)
	for _, rq := range []struct {
		method, path string
		body         any
	}{
		{"GET", c, nil},
		{"PUT", c, map[string]any{"card": vcard.Contact{Given: "X"}}},
		{"PATCH", c, map[string]any{"starred": true}},
		{"DELETE", c, nil},
		{"GET", c + "/vcf", nil},
		{"GET", c + "/photo", nil},
		{"PUT", c + "/photo", map[string]any{}},
		{"DELETE", c + "/photo", nil},
		{"GET", c + "/related", nil},
		{"POST", c + "/task", map[string]any{"title": "x"}},
		{"POST", c + "/event", map[string]any{"title": "x", "start": "2026-10-22T10:00"}},
		{"GET", c + "/activity", nil},
		{"PUT", b, map[string]any{"visible": false}},
		{"DELETE", b, nil},
		{"POST", b + "/leave", nil},
		{"GET", b + "/shares", nil},
		{"POST", b + "/shares", map[string]any{"everyone": true, "role": "read"}},
		{"GET", b + "/export.vcf", nil},
		{"PUT", fmt.Sprintf("/api/v1/shares/%d", shares[0].ID), map[string]any{"role": "write"}},
		{"DELETE", fmt.Sprintf("/api/v1/shares/%d", shares[0].ID), nil},
		{"GET", fmt.Sprintf("/api/v1/contacts?book=%d", book.ID), nil},
		{"POST", "/api/v1/contacts", map[string]any{"addressbook_id": book.ID, "card": vcard.Contact{Given: "X"}}},
		{"POST", "/api/v1/import", map[string]any{"addressbook_id": book.ID, "text": "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:X\r\nEND:VCARD\r\n"}},
		{"POST", "/api/v1/duplicates/merge", map[string]any{"ids": []int64{ct.ID, gone.ID}}},
		{"POST", "/api/v1/duplicates/ignore", map[string]any{"ids": []int64{ct.ID, ct.ID}}},
		{"POST", fmt.Sprintf("/api/v1/deleted/%d/restore", deleted[0].ID), nil},
		{"DELETE", fmt.Sprintf("/api/v1/deleted/%d", deleted[0].ID), nil},
	} {
		if code, body := bob.call(rq.method, rq.path, rq.body); code != 404 {
			t.Errorf("%s %s as someone else: want 404, got %d: %s", rq.method, rq.path, code, body)
		}
	}
	// nothing of Alice's leaks into Bob's lists
	var list struct {
		Contacts []contactJSON `json:"contacts"`
		Total    int           `json:"total"`
	}
	bob.json("GET", "/api/v1/contacts", nil, 200, &list)
	bob.json("GET", "/api/v1/search?q=marko", nil, 200, &list)
	if len(list.Contacts) != 0 {
		t.Fatalf("bob sees %s", names(list.Contacts))
	}
	var res struct {
		Done   int              `json:"done"`
		Failed []map[string]any `json:"failed"`
	}
	bob.json("POST", "/api/v1/contacts/bulk", map[string]any{"ids": []int64{ct.ID}, "action": "delete"}, 200, &res)
	if res.Done != 0 || len(res.Failed) != 1 || res.Failed[0]["error"] != "not found" {
		t.Fatalf("bulk: %+v", res)
	}
	// and it is all still there
	alice.json("GET", c, nil, 200, nil)
}

func TestSharedBookRoles(t *testing.T) {
	s, h := apiServer(t)
	alice := apiPerson(t, s, h, "alice@example.com", "Alice")
	bob := apiPerson(t, s, h, "bob@example.com", "Bob")
	book := firstBook(alice)
	ct := alice.create(vcard.Contact{Given: "Ana", Family: "Kovač", Phones: []vcard.Field{{Label: "mobile", Value: "+387 65 111 222"}}})
	var shares []store.Share
	alice.json("POST", fmt.Sprintf("/api/v1/addressbooks/%d/shares", book.ID), map[string]any{"user_id": bob.user.ID, "role": "read"}, 200, &shares)

	path := fmt.Sprintf("/api/v1/contacts/%d", ct.ID)
	var got contactJSON
	bob.json("GET", path, nil, 200, &got)
	if got.Role != "read" || got.FN != "Ana Kovač" {
		t.Fatalf("bob reads %+v", got)
	}
	edit := got.Card
	edit.Nickname = "Anči"
	bob.json("PUT", path, map[string]any{"card": edit, "etag": got.ETag}, 403, nil)
	bob.json("DELETE", path, nil, 403, nil)
	bob.json("PUT", fmt.Sprintf("/api/v1/addressbooks/%d", book.ID), map[string]any{"name": "Mine now"}, 403, nil)
	// a star is Bob's own
	bob.json("PATCH", path, map[string]any{"starred": true}, 200, &got)
	if !got.Starred {
		t.Fatal("bob's star did not stick")
	}
	alice.json("GET", path, nil, 200, &got)
	if got.Starred {
		t.Fatal("bob's star shows for alice")
	}
	// hiding a book is Bob's own view too
	bob.json("PUT", fmt.Sprintf("/api/v1/addressbooks/%d", book.ID), map[string]any{"visible": false}, 200, nil)
	bob.json("PUT", fmt.Sprintf("/api/v1/addressbooks/%d", book.ID), map[string]any{"visible": true}, 200, nil)

	alice.json("PUT", fmt.Sprintf("/api/v1/shares/%d", shares[0].ID), map[string]any{"role": "write"}, 200, nil)
	bob.json("GET", path, nil, 200, &got)
	edit = got.Card
	edit.Nickname = "Anči"
	bob.json("PUT", path, map[string]any{"card": edit, "etag": got.ETag}, 200, &got)
	if got.Card.Nickname != "Anči" || got.Role != "write" {
		t.Fatalf("write share edit: %+v", got)
	}
	// Bob may not delete Alice's book, nor her last one
	bob.json("DELETE", fmt.Sprintf("/api/v1/addressbooks/%d", book.ID), nil, 403, nil)
	alice.json("DELETE", fmt.Sprintf("/api/v1/addressbooks/%d", book.ID), nil, 400, nil)
}

const unknownPropsCard = "BEGIN:VCARD\r\nVERSION:3.0\r\nPRODID:-//Apple Inc.//iPhone OS 26.0//EN\r\nN:Petrović;Marko;;;\r\nFN:Marko Petrović\r\n" +
	"TEL;type=CELL;type=VOICE;type=pref:+387 65 123 456\r\nitem3.ADR;type=HOME;type=pref:;;Kralja Petra 12;Banja Luka;;78000;BiH\r\nitem3.X-ABADR:ba\r\n" +
	"X-UNKNOWN-THING;X-FOO=bar:keep me\r\nX-ANDROID-CUSTOM:vnd.android.cursor.item/nickname;Mare;1;;;;;;;;;;;;;\r\nUID:KEEP-1\r\nEND:VCARD\r\n"

func TestPutKeepsUnknownAndChecksETag(t *testing.T) {
	s, h := apiServer(t)
	alice := apiPerson(t, s, h, "alice@example.com", "Alice")
	var res ImportResult
	alice.json("POST", "/api/v1/import", map[string]any{"text": unknownPropsCard}, 200, &res)
	if res.Created != 1 {
		t.Fatalf("import: %+v", res)
	}
	var list struct {
		Contacts []contactJSON `json:"contacts"`
	}
	alice.json("GET", "/api/v1/contacts", nil, 200, &list)
	path := fmt.Sprintf("/api/v1/contacts/%d", list.Contacts[0].ID)
	var got contactJSON
	alice.json("GET", path, nil, 200, &got)
	stale := got.ETag
	edit := got.Card
	edit.Emails = append(edit.Emails, vcard.Field{Label: "work", Value: "marko@acme.example"})
	alice.json("PUT", path, map[string]any{"card": edit, "etag": got.ETag}, 200, &got)
	if len(got.Emails) != 1 || got.ETag == stale {
		t.Fatalf("after put: %+v", got)
	}
	edit.Title = "Boss"
	alice.json("PUT", path, map[string]any{"card": edit, "etag": stale}, 412, nil)
	code, vcf, _ := alice.raw("GET", path+"/vcf", "", nil)
	if code != 200 {
		t.Fatalf("vcf: %d", code)
	}
	for _, want := range []string{"X-UNKNOWN-THING;X-FOO=bar:keep me", "item3.X-ABADR:ba", "X-ANDROID-CUSTOM:", "UID:KEEP-1", "EMAIL;TYPE=INTERNET,WORK:marko@acme.example"} {
		if !strings.Contains(string(vcf), want) {
			t.Errorf("vcf lost %q:\n%s", want, vcf)
		}
	}
	if strings.Contains(string(vcf), "Boss") {
		t.Error("a stale write went through")
	}
}

func multipartBody(t *testing.T, fields map[string]string, files map[string][]byte) (string, []byte) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	for name, data := range files {
		fw, err := mw.CreateFormFile("file", name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write(data)
	}
	_ = mw.Close()
	return mw.FormDataContentType(), b.Bytes()
}

func TestRestoreAndActivity(t *testing.T) {
	s, h := apiServer(t)
	alice := apiPerson(t, s, h, "alice@example.com", "Alice")
	ct := alice.create(vcard.Contact{Given: "Lejla", Family: "Begić"})
	alice.json("DELETE", fmt.Sprintf("/api/v1/contacts/%d", ct.ID), nil, 204, nil)
	var deleted []store.DeletedContact
	alice.json("GET", "/api/v1/deleted", nil, 200, &deleted)
	if len(deleted) != 1 || deleted[0].FN != "Lejla Begić" {
		t.Fatalf("deleted: %+v", deleted)
	}
	var back contactJSON
	alice.json("POST", fmt.Sprintf("/api/v1/deleted/%d/restore", deleted[0].ID), nil, 200, &back)
	if back.UID != ct.UID || back.FN != "Lejla Begić" {
		t.Fatalf("restored: %+v", back)
	}
	var acts []store.Activity
	alice.json("GET", "/api/v1/activity", nil, 200, &acts)
	if len(acts) < 3 || acts[0].Action != "restored" || acts[1].Action != "deleted" || acts[2].Action != "created" {
		t.Fatalf("activity: %+v", acts)
	}
	alice.json("GET", fmt.Sprintf("/api/v1/contacts/%d/activity", back.ID), nil, 200, &acts)
	if len(acts) == 0 || acts[0].Action != "restored" || *acts[0].ContactID != back.ID {
		t.Fatalf("contact activity: %+v", acts)
	}
}

func TestLinkTokenRule(t *testing.T) {
	if linkMayChange("", "web") != nil || linkMayChange("money", "money") != nil {
		t.Fatal("allowed writes refused")
	}
	if linkMayChange("money", "web") == nil || linkMayChange("money", "import") == nil || linkMayChange("link", "") == nil {
		t.Fatal("a link app may change someone else's contact")
	}
	s, h := apiServer(t)
	alice := apiPerson(t, s, h, "alice@example.com", "Alice")
	ctx := context.Background()
	mine := alice.create(vcard.Contact{Given: "Own"}) // source "api"
	ab, err := s.defaultBook(ctx, alice.user)
	if err != nil {
		t.Fatal(err)
	}
	app := actor{u: alice.user, app: "money", source: "money"}
	theirs, err := s.createCard(ctx, app, ab, vcard.New(newUID(), vcard.Contact{Given: "Payee"}))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.deleteContact(ctx, app, mine.ID); !strings.Contains(fmt.Sprint(err), "only change the contacts it created") {
		t.Fatalf("link app deleted a contact it did not create: %v", err)
	}
	if _, err := s.patchContact(ctx, app, mine.ID, patchIn{AddCategories: []string{"x"}}); err == nil {
		t.Fatal("link app labelled a contact it did not create")
	}
	if _, err := s.patchContact(ctx, app, theirs.ID, patchIn{AddCategories: []string{"Payees"}}); err != nil {
		t.Fatalf("link app can't change its own contact: %v", err)
	}
	if err := s.deleteContact(ctx, app, theirs.ID); err != nil {
		t.Fatalf("link app can't delete its own contact: %v", err)
	}
	// the route table: a link with contacts:write reaches PUT/PATCH/DELETE of one contact, nothing else
	for _, rq := range []struct {
		method, path string
		ok           bool
	}{
		{"PUT", "/api/v1/contacts/12", true}, {"PATCH", "/api/v1/contacts/12", true}, {"DELETE", "/api/v1/contacts/12", true},
		{"POST", "/api/v1/contacts", true}, {"POST", "/api/v1/contacts/bulk", false}, {"PUT", "/api/v1/contacts/12/photo", false},
		{"POST", "/api/v1/import", false},
	} {
		req := httptest.NewRequest(rq.method, rq.path, nil)
		if got := linkAllows([]string{"contacts:read", "contacts:write"}, req); got != rq.ok {
			t.Errorf("%s %s: allowed=%v", rq.method, rq.path, got)
		}
	}
}

func TestBulkAndLabels(t *testing.T) {
	s, h := apiServer(t)
	alice := apiPerson(t, s, h, "alice@example.com", "Alice")
	a := alice.create(vcard.Contact{Given: "A", Family: "One", Categories: []string{"Posao"}})
	b := alice.create(vcard.Contact{Given: "B", Family: "Two", Categories: []string{"posao", "Friends"}})
	var res struct {
		Done int `json:"done"`
	}
	alice.json("POST", "/api/v1/contacts/bulk", map[string]any{"ids": []int64{a.ID, b.ID}, "action": "star"}, 200, &res)
	if res.Done != 2 {
		t.Fatalf("bulk star: %+v", res)
	}
	var changed struct {
		Changed int `json:"changed"`
	}
	alice.json("POST", "/api/v1/categories/rename", map[string]any{"from": "Posao", "to": "Work"}, 200, &changed)
	if changed.Changed != 2 {
		t.Fatalf("rename: %+v", changed)
	}
	var cats struct {
		Labels []struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		} `json:"labels"`
	}
	alice.json("GET", "/api/v1/categories", nil, 200, &cats)
	if len(cats.Labels) != 2 || cats.Labels[0].Name != "Friends" || cats.Labels[1].Name != "Work" || cats.Labels[1].Count != 2 {
		t.Fatalf("labels: %+v", cats)
	}
	alice.json("POST", "/api/v1/categories/delete", map[string]any{"name": "work"}, 200, &changed)
	var list struct {
		Contacts []contactJSON `json:"contacts"`
	}
	alice.json("GET", "/api/v1/contacts?starred=1&category=-", nil, 200, &list)
	if len(list.Contacts) != 1 || list.Contacts[0].ID != a.ID {
		t.Fatalf("starred without labels: %s", names(list.Contacts))
	}
	// move into a second book
	var nb bookJSON
	alice.json("POST", "/api/v1/addressbooks", map[string]any{"name": "Work"}, 201, &nb)
	alice.json("POST", "/api/v1/contacts/bulk", map[string]any{"ids": []int64{a.ID}, "action": "move", "addressbook_id": nb.ID}, 200, &res)
	alice.json("GET", fmt.Sprintf("/api/v1/contacts?book=%d", nb.ID), nil, 200, &list)
	if len(list.Contacts) != 1 || list.Contacts[0].UID != a.UID || !list.Contacts[0].Starred {
		t.Fatalf("moved: %+v", list.Contacts)
	}
}

func TestReadOnlyBookRefusesWrites(t *testing.T) {
	s, h := apiServer(t)
	alice := apiPerson(t, s, h, "alice@example.com", "Alice")
	ctx := context.Background()
	own := firstBook(alice)
	ro := &store.AddressBook{UserID: alice.user.ID, Name: "iCloud", Kind: "carddav", ReadOnly: true}
	if err := s.store.CreateAddressBook(ctx, ro); err != nil {
		t.Fatal(err)
	}
	put := func(given, email string) int64 {
		card := vcard.New(newUID(), vcard.Contact{Given: given, Family: "Mirror", Emails: []vcard.Field{{Value: email}}, Categories: []string{"Work"}})
		e, _, err := s.store.SaveContact(ctx, store.ContactWrite{AddressBookID: ro.ID, VCF: card.String(), Source: "remote", FromRemote: true})
		if err != nil {
			t.Fatal(err)
		}
		return e.ID
	}
	id := put("Marko", "marko@example.com")
	other := put("Ana", "ana@example.com")
	local := alice.create(vcard.Contact{Given: "Marko", Family: "Mirror", Emails: []vcard.Field{{Value: "marko@example.com"}}, Categories: []string{"Work"}})

	c := fmt.Sprintf("/api/v1/contacts/%d", id)
	var got contactJSON
	alice.json("GET", c, nil, 200, &got)
	const msg = "this address book is read-only (it mirrors an account)"
	for _, rq := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/v1/contacts", map[string]any{"addressbook_id": ro.ID, "card": vcard.Contact{Given: "X"}}},
		{"PUT", c, map[string]any{"card": got.Card, "etag": got.ETag}},
		{"PATCH", c, map[string]any{"add_categories": []string{"x"}}},
		{"PATCH", c, map[string]any{"addressbook_id": own.ID}},
		{"PATCH", fmt.Sprintf("/api/v1/contacts/%d", local.ID), map[string]any{"addressbook_id": ro.ID}},
		{"DELETE", c, nil},
		{"PUT", c + "/photo", map[string]any{}},
		{"DELETE", c + "/photo", nil},
		{"POST", "/api/v1/duplicates/merge", map[string]any{"ids": []int64{id, other}}},
		{"POST", "/api/v1/duplicates/merge", map[string]any{"ids": []int64{local.ID, id}, "primary_id": local.ID}},
		{"POST", "/api/v1/import", map[string]any{"addressbook_id": ro.ID, "text": "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:X\r\nEND:VCARD\r\n"}},
		{"PUT", fmt.Sprintf("/api/v1/addressbooks/%d", ro.ID), map[string]any{"is_default": true}},
	} {
		code, body := alice.call(rq.method, rq.path, rq.body)
		if code != 403 || !strings.Contains(string(body), msg) {
			t.Errorf("%s %s on a read-only book: %d %s", rq.method, rq.path, code, body)
		}
	}
	var res struct {
		Done   int              `json:"done"`
		Failed []map[string]any `json:"failed"`
	}
	alice.json("POST", "/api/v1/contacts/bulk", map[string]any{"ids": []int64{id, local.ID}, "action": "add_category", "category": "VIP"}, 200, &res)
	if res.Done != 1 || len(res.Failed) != 1 || res.Failed[0]["error"] != msg {
		t.Fatalf("bulk: %+v", res)
	}
	// a star is still the person's own mark
	alice.json("PATCH", c, map[string]any{"starred": true}, 200, nil)
	// labels are renamed where the person may write, the mirror is left alone
	var changed struct {
		Changed int `json:"changed"`
	}
	alice.json("POST", "/api/v1/categories/rename", map[string]any{"from": "Work", "to": "Posao"}, 200, &changed)
	alice.json("POST", "/api/v1/categories/delete", map[string]any{"name": "VIP"}, 200, nil)
	alice.json("GET", c, nil, 200, &got)
	if changed.Changed != 1 || strings.Join(got.Categories, ",") != "Work" {
		t.Fatalf("relabel touched the mirror: %d %v", changed.Changed, got.Categories)
	}
	// the same person in the mirror and locally is not offered as a duplicate
	var groups []DupGroup
	alice.json("GET", "/api/v1/duplicates", nil, 200, &groups)
	if len(groups) != 0 {
		t.Fatalf("duplicates include a read-only book: %+v", groups)
	}
	// new contacts never land there by default
	created := alice.create(vcard.Contact{Given: "New"})
	alice.json("GET", fmt.Sprintf("/api/v1/contacts/%d", created.ID), nil, 200, &got)
	if got.Role != "owner" {
		t.Fatalf("default book: %+v", got)
	}
	var full struct {
		AddressBook struct {
			ID int64 `json:"id"`
		} `json:"addressbook"`
	}
	alice.json("GET", fmt.Sprintf("/api/v1/contacts/%d", created.ID), nil, 200, &full)
	if full.AddressBook.ID != own.ID {
		t.Fatalf("new contact went to book %d", full.AddressBook.ID)
	}
	// a contact the account sync removed comes back into a book the person can write
	row, _ := s.store.GetContact(ctx, other)
	if err := s.store.DeleteContact(ctx, row, store.DeleteContactOpts{Actor: alice.user.ID, KeepCopy: true, FromRemote: true}); err != nil {
		t.Fatal(err)
	}
	var deleted []store.DeletedContact
	alice.json("GET", "/api/v1/deleted", nil, 200, &deleted)
	alice.json("POST", fmt.Sprintf("/api/v1/deleted/%d/restore", deleted[0].ID), nil, 200, &full)
	if full.AddressBook.ID != own.ID {
		t.Fatalf("restored into book %d", full.AddressBook.ID)
	}
}
