package server

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/config"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/store"
)

func davTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("AUTH_MODE", "local")
	t.Setenv("TZ", "Europe/Sarajevo")
	t.Setenv("CORE_URL", "")
	t.Setenv("BASE_URL", "https://contacts.example.com")
	t.Setenv("ADMIN_EMAIL", "")
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
	if err := s.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	return s, s.Router()
}

// davUser is a person on the box with an API token (their CardDAV password).
type davUser struct {
	t     *testing.T
	h     http.Handler
	u     *store.User
	token string
}

func newDavUser(t *testing.T, s *Server, h http.Handler, email, name string) *davUser {
	t.Helper()
	ctx := context.Background()
	u := &store.User{Email: email, Name: name, Role: "member"}
	if err := s.store.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	s.seedUser(ctx, u)
	tok, _, err := s.store.CreateAPIToken(ctx, u.ID, "phone")
	if err != nil {
		t.Fatal(err)
	}
	return &davUser{t: t, h: h, u: u, token: tok}
}

func (d *davUser) do(method, path, body string, headers ...string) *httptest.ResponseRecorder {
	d.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if d.token != "" {
		req.SetBasicAuth(d.u.Username, d.token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	d.h.ServeHTTP(w, req)
	return w
}

// api calls the JSON API with the person's token as a bearer.
func (d *davUser) api(method, path string, body any, want int, out any) {
	d.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Authorization", "Bearer "+d.token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	d.h.ServeHTTP(w, req)
	if w.Code != want {
		d.t.Fatalf("%s %s: HTTP %d (want %d): %s", method, path, w.Code, want, w.Body.String())
	}
	if out != nil {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			d.t.Fatalf("%s %s: %v: %s", method, path, err, w.Body.String())
		}
	}
}

func (d *davUser) mustStatus(w *httptest.ResponseRecorder, want int, what string) {
	d.t.Helper()
	if w.Code != want {
		d.t.Fatalf("%s: status %d, want %d; body:\n%s", what, w.Code, want, w.Body.String())
	}
}

// davResponse returns the <d:response> of a multistatus whose href is href ("" when absent).
func davResponse(body, href string) string {
	for _, part := range strings.Split(body, "<d:response>")[1:] {
		if strings.HasPrefix(part, "<d:href>"+href+"</d:href>") {
			return part
		}
	}
	return ""
}

// addressData reads the (unescaped) address-data of each response of a multistatus.
func addressData(t *testing.T, body string) map[string]string {
	t.Helper()
	var root xnode
	if err := xml.Unmarshal([]byte(body), &root); err != nil {
		t.Fatalf("bad multistatus: %v\n%s", err, body)
	}
	out := map[string]string{}
	for _, r := range root.children("response") {
		for _, ps := range r.children("propstat") {
			if ad := ps.child("prop").child("address-data"); ad != nil {
				out[strings.TrimSpace(r.child("href").Text)] = ad.Text
			}
		}
	}
	return out
}

func vcf(uid, fn string, extra ...string) string {
	var b strings.Builder
	b.WriteString("BEGIN:VCARD\r\nVERSION:3.0\r\nPRODID:-//test//EN\r\nUID:" + uid + "\r\nFN:" + fn + "\r\nN:" + fn + ";;;;\r\n")
	for _, x := range extra {
		b.WriteString(x + "\r\n")
	}
	b.WriteString("END:VCARD\r\n")
	return b.String()
}

// iosCard is what iOS Contacts writes: grouped labels, an Apple address
// extension and a folded base64 photo.
const iosCard = "BEGIN:VCARD\r\n" +
	"VERSION:3.0\r\n" +
	"PRODID:-//Apple Inc.//iPhone OS 18.0//EN\r\n" +
	"N:Petrović;Marko;;;\r\n" +
	"FN:Marko Petrović\r\n" +
	"ORG:Acme d.o.o.;\r\n" +
	"item1.EMAIL;type=INTERNET;type=pref:marko@example.com\r\n" +
	"item1.X-ABLabel:_$!<Other>!$_\r\n" +
	"TEL;type=CELL;type=VOICE;type=pref:+387 65 123 456\r\n" +
	"item2.TEL:+387 51 222 333\r\n" +
	"item2.X-ABLabel:Vikendica\r\n" +
	"item3.ADR;type=HOME;type=pref:;;Kralja Petra 1;Banja Luka;;78000;Bosna i Hercegovina\r\n" +
	"item3.X-ABADR:ba\r\n" +
	"BDAY:1988-05-03\r\n" +
	"NOTE:Prvi red\\nDrugi red\\, sa zarezom\r\n" +
	"PHOTO;ENCODING=b;TYPE=JPEG:/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAAgGBgcGBQgHBwcJCQgKDBQNDAsLDBkSEw8UHRofHh0aHB\r\n" +
	" wgJC4nICIsIxwcKDcpLDAxNDQ0Hyc5PTgyPC4zNDL/wAALCAABAAEBAREA/8QAFAABAAAAAAAAAAAAAAAA\r\n" +
	" AAAACf/EABQQAQAAAAAAAAAAAAAAAAAAAAD/2gAIAQEAAD8AKp//2Q==\r\n" +
	"X-ABShowAs:PERSON\r\n" +
	"UID:4F2A6C1E-9B3D-4E8F-A1B2-C3D4E5F60718\r\n" +
	"REV:2026-10-20T08:00:00Z\r\n" +
	"END:VCARD\r\n"

const propfindDiscovery = `<?xml version="1.0"?><d:propfind xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav" xmlns:cs="http://calendarserver.org/ns/"><d:prop>
<d:current-user-principal/><d:principal-URL/><card:addressbook-home-set/><d:displayname/><d:resourcetype/><cs:email-address-set/></d:prop></d:propfind>`

const propfindBook = `<?xml version="1.0"?><d:propfind xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav" xmlns:cs="http://calendarserver.org/ns/"><d:prop>
<d:displayname/><d:resourcetype/><d:current-user-privilege-set/><cs:getctag/><d:sync-token/><card:supported-address-data/><d:getetag/></d:prop></d:propfind>`

// shareBook makes an address book of owner's and shares it with someone.
func shareBook(t *testing.T, s *Server, owner, with *davUser, name, role string) *store.AddressBook {
	t.Helper()
	ctx := context.Background()
	ab := &store.AddressBook{UserID: owner.u.ID, Name: name, Color: "#10b981"}
	if err := s.store.CreateAddressBook(ctx, ab); err != nil {
		t.Fatal(err)
	}
	uid := with.u.ID
	if err := s.store.UpsertShare(ctx, &store.Share{AddressBookID: ab.ID, UserID: &uid, Role: role}, owner.u.ID); err != nil {
		t.Fatal(err)
	}
	return ab
}

func TestDAVDiscovery(t *testing.T) {
	s, h := davTestServer(t)
	ann := newDavUser(t, s, h, "ann@example.com", "Ann")

	// unauthenticated: 401 with a Basic challenge
	anon := &davUser{t: t, h: h, u: ann.u}
	w := anon.do("PROPFIND", "/remote.php/carddav/principals/ann/", propfindDiscovery, "Depth", "0")
	anon.mustStatus(w, 401, "no credentials")
	if got := w.Header().Get("WWW-Authenticate"); got != `Basic realm="Pocket Contacts"` {
		t.Fatalf("WWW-Authenticate = %q", got)
	}
	wrong := &davUser{t: t, h: h, u: ann.u, token: "pcon_nope"}
	wrong.mustStatus(wrong.do("PROPFIND", "/dav/", propfindDiscovery), 401, "wrong token")

	w = ann.do("OPTIONS", "/dav/", "")
	if !strings.Contains(w.Header().Get("DAV"), "addressbook") {
		t.Fatalf("DAV header = %q", w.Header().Get("DAV"))
	}

	w = ann.do("GET", "/.well-known/carddav", "")
	if w.Code != 301 || w.Header().Get("Location") != "/dav/" {
		t.Fatalf("well-known: %d %q", w.Code, w.Header().Get("Location"))
	}

	// root → principal
	w = ann.do("PROPFIND", "/dav/", propfindDiscovery, "Depth", "0")
	ann.mustStatus(w, 207, "root")
	if !strings.Contains(w.Body.String(), "<d:current-user-principal><d:href>/dav/principals/ann/</d:href>") {
		t.Fatalf("root: %s", w.Body.String())
	}

	// the principal under all three prefixes points at a home under the same prefix
	for path, home := range map[string]string{
		"/dav/principals/ann/":                  "/dav/addressbooks/ann/",
		"/remote.php/dav/principals/users/ann/": "/remote.php/dav/addressbooks/users/ann/",
		"/remote.php/carddav/principals/ann/":   "/remote.php/carddav/addressbooks/ann/",
		"/principals/ann/":                      "/dav/addressbooks/ann/",
	} {
		w = ann.do("PROPFIND", path, propfindDiscovery, "Depth", "0")
		ann.mustStatus(w, 207, path)
		b := w.Body.String()
		if !strings.Contains(b, "<card:addressbook-home-set><d:href>"+home+"</d:href>") || !strings.Contains(b, "<d:principal/>") ||
			!strings.Contains(b, "<cs:email-address>ann@example.com</cs:email-address>") {
			t.Fatalf("%s:\n%s", path, b)
		}
		// and the home lists the default book
		w = ann.do("PROPFIND", home, propfindBook, "Depth", "1")
		ann.mustStatus(w, 207, home)
		if davResponse(w.Body.String(), home+"contacts/") == "" {
			t.Fatalf("%s lists no contacts book:\n%s", home, w.Body.String())
		}
	}

	// the legacy iOS path works all the way down
	w = ann.do("PUT", "/remote.php/carddav/addressbooks/ann/contacts/x1.vcf", vcf("x1", "Ex One"), "If-None-Match", "*")
	ann.mustStatus(w, 201, "put via legacy path")
	w = ann.do("GET", "/dav/addressbooks/ann/contacts/x1.vcf", "")
	ann.mustStatus(w, 200, "get via /dav")

	// another person's principal and home answer 404
	bob := newDavUser(t, s, h, "bob@example.com", "Bob")
	ann.mustStatus(ann.do("PROPFIND", "/dav/principals/bob/", propfindDiscovery, "Depth", "0"), 404, "bob's principal")
	ann.mustStatus(ann.do("PROPFIND", "/remote.php/carddav/addressbooks/bob/contacts/", propfindBook, "Depth", "1"), 404, "bob's book")
	ann.mustStatus(ann.do("GET", "/dav/addressbooks/bob/contacts/x1.vcf", ""), 404, "bob's card")
	_ = bob
}

func TestDAVHomeListsOwnAndShared(t *testing.T) {
	s, h := davTestServer(t)
	ann := newDavUser(t, s, h, "ann@example.com", "Ann")
	bob := newDavUser(t, s, h, "bob@example.com", "Bob")
	carl := newDavUser(t, s, h, "carl@example.com", "Carl")
	family := shareBook(t, s, bob, ann, "Family", store.RoleRead)
	work := shareBook(t, s, bob, ann, "Work", store.RoleWrite)
	secret := shareBook(t, s, bob, carl, "Secret", store.RoleWrite)

	w := ann.do("PROPFIND", "/dav/addressbooks/ann/", propfindBook, "Depth", "1")
	ann.mustStatus(w, 207, "home")
	b := w.Body.String()
	own := davResponse(b, "/dav/addressbooks/ann/contacts/")
	fam := davResponse(b, "/dav/addressbooks/ann/shared-"+strconv.FormatInt(family.ID, 10)+"/")
	wrk := davResponse(b, "/dav/addressbooks/ann/shared-"+strconv.FormatInt(work.ID, 10)+"/")
	if own == "" || fam == "" || wrk == "" {
		t.Fatalf("home:\n%s", b)
	}
	if strings.Index(b, "/dav/addressbooks/ann/contacts/") > strings.Index(b, "shared-") {
		t.Fatal("the person's default book must come first (iOS shows only that one)")
	}
	if !strings.Contains(own, "<card:addressbook/>") || !strings.Contains(own, "<d:write/>") || !strings.Contains(own, `version="4.0"`) {
		t.Fatalf("own book props: %s", own)
	}
	if !strings.Contains(fam, "<d:displayname>Family (Bob)</d:displayname>") || strings.Contains(fam, "<d:write/>") {
		t.Fatalf("read share must advertise read only: %s", fam)
	}
	if !strings.Contains(wrk, "<d:write/>") {
		t.Fatalf("write share: %s", wrk)
	}
	if strings.Contains(b, "Secret") {
		t.Fatal("a book shared with someone else shows up")
	}
	// someone else's share answers 404, not 403
	ann.mustStatus(ann.do("PROPFIND", "/dav/addressbooks/ann/shared-"+strconv.FormatInt(secret.ID, 10)+"/", propfindBook, "Depth", "0"), 404, "unshared book")
	ann.mustStatus(ann.do("PUT", "/dav/addressbooks/ann/shared-"+strconv.FormatInt(secret.ID, 10)+"/z.vcf", vcf("z", "Zed")), 404, "put into an unshared book")

	// read share: no writes; write share: writes
	famPath := "/dav/addressbooks/ann/shared-" + strconv.FormatInt(family.ID, 10) + "/"
	w = ann.do("PUT", famPath+"r1.vcf", vcf("r1", "Read Only"), "If-None-Match", "*")
	ann.mustStatus(w, 403, "put into a read share")
	if !strings.Contains(w.Body.String(), "need-privileges") {
		t.Fatalf("403 body: %s", w.Body.String())
	}
	bobCard := vcf("b1", "Bob's Friend")
	if _, _, err := s.store.SaveContact(context.Background(), store.ContactWrite{AddressBookID: family.ID, Href: "b1.vcf", VCF: bobCard, Source: "web"}); err != nil {
		t.Fatal(err)
	}
	ann.mustStatus(ann.do("GET", famPath+"b1.vcf", ""), 200, "read a shared card")
	ann.mustStatus(ann.do("DELETE", famPath+"b1.vcf", ""), 403, "delete in a read share")
	ann.mustStatus(ann.do("DELETE", famPath, ""), 403, "delete someone else's book")
	ann.mustStatus(ann.do("PUT", "/dav/addressbooks/ann/shared-"+strconv.FormatInt(work.ID, 10)+"/w1.vcf", vcf("w1", "Work Mate"), "If-None-Match", "*"), 201, "put into a write share")

	// a hidden share leaves the home
	hidden := false
	if err := s.store.SavePrefs(context.Background(), ann.u.ID, work.ID, &hidden, nil); err != nil {
		t.Fatal(err)
	}
	w = ann.do("PROPFIND", "/dav/addressbooks/ann/", propfindBook, "Depth", "1")
	if strings.Contains(w.Body.String(), "shared-"+strconv.FormatInt(work.ID, 10)) {
		t.Fatal("a hidden share is still listed")
	}
}

func TestDAVPutGetConditional(t *testing.T) {
	s, h := davTestServer(t)
	ann := newDavUser(t, s, h, "ann@example.com", "Ann")
	base := "/remote.php/dav/addressbooks/users/ann/contacts/"

	w := ann.do("PUT", base+"ios.vcf", iosCard, "If-None-Match", "*", "Content-Type", "text/vcard; charset=utf-8")
	ann.mustStatus(w, 201, "create")
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on create")
	}
	w = ann.do("GET", base+"ios.vcf", "")
	ann.mustStatus(w, 200, "get")
	if w.Body.String() != iosCard {
		t.Fatalf("the vCard changed:\n%q\nwant\n%q", w.Body.String(), iosCard)
	}
	if w.Header().Get("ETag") != etag || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/vcard") {
		t.Fatalf("headers: %v", w.Header())
	}

	ann.mustStatus(ann.do("PUT", base+"ios.vcf", iosCard, "If-None-Match", "*"), 412, "If-None-Match on an existing card")
	ann.mustStatus(ann.do("PUT", base+"ios.vcf", iosCard, "If-Match", `"stale"`), 412, "stale If-Match")
	ann.mustStatus(ann.do("PUT", base+"new.vcf", vcf("n1", "New"), "If-Match", etag), 412, "If-Match on a missing card")

	changed := strings.Replace(iosCard, "Vikendica", "Vikendica na Jahorini", 1)
	w = ann.do("PUT", base+"ios.vcf", changed, "If-Match", etag)
	ann.mustStatus(w, 204, "update")
	etag2 := w.Header().Get("ETag")
	if etag2 == "" || etag2 == etag {
		t.Fatalf("etag after update: %q", etag2)
	}
	row, err := s.store.GetContactByHref(context.Background(), mustBook(t, s, ann.u.ID, "contacts").ID, "ios.vcf")
	if err != nil || row.Source != "carddav" || row.FN != "Marko Petrović" || !row.HasPhoto || row.Kind != "individual" {
		t.Fatalf("stored row = %+v, %v", row, err)
	}

	// a resource holding another UID, and a second resource with the same UID
	ann.mustStatus(ann.do("PUT", base+"ios.vcf", vcf("other", "Other"), "If-Match", etag2), 409, "UID change")
	w = ann.do("PUT", base+"dupe.vcf", changed, "If-None-Match", "*")
	ann.mustStatus(w, 409, "UID taken")
	if !strings.Contains(w.Body.String(), "no-uid-conflict") || !strings.Contains(w.Body.String(), base+"ios.vcf") {
		t.Fatalf("409 body: %s", w.Body.String())
	}
	// garbage and two cards in one resource
	ann.mustStatus(ann.do("PUT", base+"bad.vcf", "hello"), 400, "not a vCard")
	ann.mustStatus(ann.do("PUT", base+"two.vcf", vcf("t1", "One")+vcf("t2", "Two")), 400, "two vCards")
	// no UID: the resource name becomes one, and no ETag is promised
	w = ann.do("PUT", base+"nouid.vcf", "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:No Uid\r\nEND:VCARD\r\n")
	ann.mustStatus(w, 201, "no UID")
	if w.Header().Get("ETag") != "" {
		t.Fatal("an ETag for a rewritten card")
	}
	w = ann.do("GET", base+"nouid.vcf", "")
	if !strings.Contains(w.Body.String(), "UID:nouid\r\nEND:VCARD") {
		t.Fatalf("no-UID card: %q", w.Body.String())
	}

	// deletes: stale If-Match, then for real (into Recently deleted)
	ann.mustStatus(ann.do("DELETE", base+"ios.vcf", "", "If-Match", etag), 412, "stale delete")
	ann.mustStatus(ann.do("DELETE", base+"ios.vcf", "", "If-Match", etag2), 204, "delete")
	ann.mustStatus(ann.do("GET", base+"ios.vcf", ""), 404, "gone")
	ann.mustStatus(ann.do("DELETE", base+"ios.vcf", ""), 404, "delete twice")
	del, err := s.store.ListDeleted(context.Background(), ann.u.ID)
	if err != nil || len(del) != 1 || del[0].FN != "Marko Petrović" || del[0].VCF != changed {
		t.Fatalf("recently deleted = %+v, %v", del, err)
	}
}

func TestDAVSyncCollectionAndReports(t *testing.T) {
	s, h := davTestServer(t)
	ann := newDavUser(t, s, h, "ann@example.com", "Ann")
	base := "/dav/addressbooks/ann/contacts/"
	ann.mustStatus(ann.do("PUT", base+"a.vcf", vcf("a", "Alma Avdić", "EMAIL;TYPE=work:alma@example.com"), "If-None-Match", "*"), 201, "a")
	ann.mustStatus(ann.do("PUT", base+"b.vcf", vcf("b", "Boris Babić", "TEL;TYPE=cell:+387 61 000 111"), "If-None-Match", "*"), 201, "b")
	group := "BEGIN:VCARD\r\nVERSION:3.0\r\nPRODID:-//Apple Inc.//iPhone OS 18.0//EN\r\nN:Porodica\r\nFN:Porodica\r\n" +
		"X-ADDRESSBOOKSERVER-KIND:group\r\nX-ADDRESSBOOKSERVER-MEMBER:urn:uuid:a\r\nX-ADDRESSBOOKSERVER-MEMBER:urn:uuid:b\r\n" +
		"UID:grp-1\r\nREV:2026-10-20T08:00:00Z\r\nEND:VCARD\r\n"
	ann.mustStatus(ann.do("PUT", base+"grp-1.vcf", group, "If-None-Match", "*"), 201, "group")

	syncBody := func(token string) string {
		return `<?xml version="1.0"?><d:sync-collection xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav"><d:sync-token>` + token +
			`</d:sync-token><d:sync-level>1</d:sync-level><d:prop><d:getetag/></d:prop></d:sync-collection>`
	}
	w := ann.do("REPORT", base, syncBody(""))
	ann.mustStatus(w, 207, "first sync")
	b := w.Body.String()
	for _, n := range []string{"a.vcf", "b.vcf", "grp-1.vcf"} {
		if davResponse(b, base+n) == "" {
			t.Fatalf("first sync lacks %s:\n%s", n, b)
		}
	}
	token := b[strings.Index(b, "<d:sync-token>")+len("<d:sync-token>") : strings.Index(b, "</d:sync-token>")]
	if !strings.HasPrefix(token, syncPfx) {
		t.Fatalf("token %q", token)
	}

	// nothing changed: nothing listed
	w = ann.do("REPORT", base, syncBody(token))
	if strings.Contains(w.Body.String(), "<d:response>") {
		t.Fatalf("no-change sync: %s", w.Body.String())
	}

	ann.mustStatus(ann.do("DELETE", base+"b.vcf", ""), 204, "delete b")
	ann.mustStatus(ann.do("PUT", base+"c.vcf", vcf("c", "Cvijeta"), "If-None-Match", "*"), 201, "c")
	w = ann.do("REPORT", base, syncBody(token))
	ann.mustStatus(w, 207, "delta sync")
	b = w.Body.String()
	if r := davResponse(b, base+"b.vcf"); !strings.Contains(r, "404 Not Found") {
		t.Fatalf("tombstone missing:\n%s", b)
	}
	if davResponse(b, base+"c.vcf") == "" || davResponse(b, base+"a.vcf") != "" {
		t.Fatalf("delta:\n%s", b)
	}

	for _, bad := range []string{"http://example.com/other/5", syncPfx + "abc", syncPfx + "999999"} {
		w = ann.do("REPORT", base, syncBody(bad))
		ann.mustStatus(w, 403, "token "+bad)
		if !strings.Contains(w.Body.String(), "valid-sync-token") {
			t.Fatalf("403 body: %s", w.Body.String())
		}
	}

	// multiget: two there, one gone
	mg := `<?xml version="1.0"?><card:addressbook-multiget xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav"><d:prop><d:getetag/><card:address-data/></d:prop>` +
		`<d:href>` + base + `a.vcf</d:href><d:href>` + base + `grp-1.vcf</d:href><d:href>` + base + `b.vcf</d:href></card:addressbook-multiget>`
	w = ann.do("REPORT", base, mg, "Depth", "1")
	ann.mustStatus(w, 207, "multiget")
	data := addressData(t, w.Body.String())
	if data[base+"grp-1.vcf"] != group || !strings.Contains(data[base+"a.vcf"], "FN:Alma Avdić") {
		t.Fatalf("multiget data: %q", data)
	}
	if !strings.Contains(davResponse(w.Body.String(), base+"b.vcf"), "404") {
		t.Fatalf("multiget of a deleted card:\n%s", w.Body.String())
	}
	row, _ := s.store.GetContactByHref(context.Background(), mustBook(t, s, ann.u.ID, "contacts").ID, "grp-1.vcf")
	if row == nil || row.Kind != "group" || len(row.Members) != 2 {
		t.Fatalf("group card row = %+v", row)
	}

	// addressbook-query: by email, by name prefix, and a filter that matches nothing
	q := func(filter string) string {
		w := ann.do("REPORT", base, `<?xml version="1.0"?><card:addressbook-query xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav"><d:prop><d:getetag/></d:prop>`+filter+`</card:addressbook-query>`, "Depth", "1")
		ann.mustStatus(w, 207, "query")
		return w.Body.String()
	}
	b = q(`<card:filter><card:prop-filter name="EMAIL"><card:text-match collation="i;unicode-casemap" match-type="contains">ALMA@</card:text-match></card:prop-filter></card:filter>`)
	if davResponse(b, base+"a.vcf") == "" || davResponse(b, base+"c.vcf") != "" {
		t.Fatalf("email query:\n%s", b)
	}
	b = q(`<card:filter test="anyof"><card:prop-filter name="FN"><card:text-match match-type="starts-with">cvi</card:text-match></card:prop-filter><card:prop-filter name="UID"><card:text-match match-type="equals">a</card:text-match></card:prop-filter></card:filter>`)
	if davResponse(b, base+"a.vcf") == "" || davResponse(b, base+"c.vcf") == "" || davResponse(b, base+"grp-1.vcf") != "" {
		t.Fatalf("anyof query:\n%s", b)
	}
	b = q(`<card:filter><card:prop-filter name="X-ADDRESSBOOKSERVER-KIND"><card:is-not-defined/></card:prop-filter></card:filter>`)
	if davResponse(b, base+"grp-1.vcf") != "" || davResponse(b, base+"a.vcf") == "" {
		t.Fatalf("is-not-defined query:\n%s", b)
	}
	b = q(``)
	if strings.Count(b, "<d:response>") != 3 {
		t.Fatalf("query without a filter:\n%s", b)
	}
}

func TestDAVIOSCardRoundTrip(t *testing.T) {
	s, h := davTestServer(t)
	ann := newDavUser(t, s, h, "ann@example.com", "Ann")
	base := "/remote.php/carddav/addressbooks/ann/contacts/"
	ann.mustStatus(ann.do("PUT", base+"4F2A6C1E.vcf", iosCard, "If-None-Match", "*"), 201, "put")

	// GET, multiget and the whole-book export give the same bytes
	w := ann.do("GET", base+"4F2A6C1E.vcf", "")
	if w.Body.String() != iosCard {
		t.Fatalf("GET changed the card:\n%q", w.Body.String())
	}
	mg := `<?xml version="1.0"?><card:addressbook-multiget xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav"><d:prop><d:getetag/><card:address-data/></d:prop><d:href>` + base + `4F2A6C1E.vcf</d:href></card:addressbook-multiget>`
	w = ann.do("REPORT", base, mg)
	if got := addressData(t, w.Body.String())[base+"4F2A6C1E.vcf"]; got != iosCard {
		t.Fatalf("multiget changed the card:\n%q", got)
	}
	w = ann.do("GET", base, "")
	if w.Body.String() != iosCard {
		t.Fatalf("book export:\n%q", w.Body.String())
	}
	row, _ := s.store.GetContactByHref(context.Background(), mustBook(t, s, ann.u.ID, "contacts").ID, "4F2A6C1E.vcf")
	if row == nil || row.Bday != "1988-05-03" || !strings.Contains(row.Phones, "38751222333") {
		t.Fatalf("index of the iOS card: %+v", row)
	}
}

func TestDAVMkcolProppatchMove(t *testing.T) {
	s, h := davTestServer(t)
	ann := newDavUser(t, s, h, "ann@example.com", "Ann")
	home := "/dav/addressbooks/ann/"

	mk := `<?xml version="1.0"?><d:mkcol xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav"><d:set><d:prop><d:resourcetype><d:collection/><card:addressbook/></d:resourcetype><d:displayname>Posao</d:displayname><card:addressbook-description>Kolege</card:addressbook-description></d:prop></d:set></d:mkcol>`
	ann.mustStatus(ann.do("MKCOL", home+"posao/", mk), 201, "mkcol")
	ann.mustStatus(ann.do("MKCOL", home+"posao/", mk), 405, "mkcol twice")
	ann.mustStatus(ann.do("MKCOL", home+"plain/", ""), 403, "plain mkcol")
	ann.mustStatus(ann.do("MKCOL", home+"shared-3/", mk), 403, "reserved name")
	posao := mustBook(t, s, ann.u.ID, "posao")
	if posao.Name != "Posao" || posao.Description != "Kolege" {
		t.Fatalf("book = %+v", posao)
	}

	pp := `<?xml version="1.0"?><d:propertyupdate xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav"><d:set><d:prop><d:displayname>Posao 2026</d:displayname><x:unknown xmlns:x="urn:x"/></d:prop></d:set></d:propertyupdate>`
	w := ann.do("PROPPATCH", home+"posao/", pp)
	ann.mustStatus(w, 207, "proppatch")
	if !strings.Contains(w.Body.String(), "200 OK") || !strings.Contains(w.Body.String(), "403 Forbidden") {
		t.Fatalf("proppatch: %s", w.Body.String())
	}
	if mustBook(t, s, ann.u.ID, "posao").Name != "Posao 2026" {
		t.Fatal("rename not kept")
	}

	// move a card between books
	ann.mustStatus(ann.do("PUT", home+"contacts/m.vcf", vcf("m", "Mover"), "If-None-Match", "*"), 201, "put")
	ann.mustStatus(ann.do("MOVE", home+"contacts/m.vcf", "", "Destination", "https://contacts.example.com"+home+"posao/m.vcf"), 201, "move")
	ann.mustStatus(ann.do("GET", home+"contacts/m.vcf", ""), 404, "moved away")
	ann.mustStatus(ann.do("GET", home+"posao/m.vcf", ""), 200, "moved here")

	// delete a book; the last one stays
	ann.mustStatus(ann.do("DELETE", home+"posao/", ""), 204, "delete book")
	ann.mustStatus(ann.do("DELETE", home+"contacts/", ""), 403, "delete the last book")
}

func TestDevicesProfile(t *testing.T) {
	s, h := davTestServer(t)
	ann := newDavUser(t, s, h, "ann@example.com", "Ann")

	var info struct {
		CardDAVURL   string           `json:"carddav_url"`
		Username     string           `json:"username"`
		IOSServer    string           `json:"ios_server"`
		PrincipalURL string           `json:"principal_url"`
		Tokens       []store.APIToken `json:"tokens"`
	}
	ann.api("GET", "/api/v1/devices", nil, 200, &info)
	if info.CardDAVURL != "https://contacts.example.com/dav/" || info.Username != "ann" ||
		info.IOSServer != "contacts.example.com/remote.php/carddav/principals/ann/" ||
		info.PrincipalURL != "https://contacts.example.com/dav/principals/ann/" || len(info.Tokens) != 0 {
		t.Fatalf("devices = %+v", info)
	}

	var made struct {
		Token      string `json:"token"`
		ProfileURL string `json:"profile_url"`
		ExpiresIn  int    `json:"expires_in"`
	}
	ann.api("POST", "/api/v1/devices", map[string]any{"name": "iPhone"}, 200, &made)
	if !strings.HasPrefix(made.Token, "pcon_") || made.ExpiresIn != 900 || !strings.HasPrefix(made.ProfileURL, "https://contacts.example.com/dav-profile/") {
		t.Fatalf("device = %+v", made)
	}
	path := strings.TrimPrefix(made.ProfileURL, "https://contacts.example.com")
	req := httptest.NewRequest("GET", path, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/x-apple-aspen-config" {
		t.Fatalf("profile: %d %v", w.Code, w.Header())
	}
	p := w.Body.String()
	for _, want := range []string{
		"<string>com.apple.carddav.account</string>",
		"<key>CardDAVHostName</key>\n\t\t\t<string>contacts.example.com</string>",
		"<integer>443</integer>",
		"<key>CardDAVUseSSL</key>\n\t\t\t<true/>",
		"<string>/remote.php/carddav/principals/ann/</string>",
		"<key>CardDAVUsername</key>\n\t\t\t<string>ann</string>",
		"<string>" + made.Token + "</string>",
		"<string>app.pocket.contacts.com.example.contacts.ann</string>",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("profile lacks %q:\n%s", want, p)
		}
	}
	var plist struct{}
	if err := xml.Unmarshal([]byte(p), &plist); err != nil {
		t.Fatalf("profile is not XML: %v", err)
	}
	// single use
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	if w.Code != 404 {
		t.Fatalf("second download: %d", w.Code)
	}

	// the device's token signs in to CardDAV
	phone := &davUser{t: t, h: h, u: ann.u, token: made.Token}
	phone.mustStatus(phone.do("PROPFIND", "/remote.php/carddav/principals/ann/", propfindDiscovery, "Depth", "0"), 207, "device token")

	ann.api("GET", "/api/v1/devices", nil, 200, &info)
	if len(info.Tokens) != 1 || info.Tokens[0].Name != "Device: iPhone" {
		t.Fatalf("tokens = %+v", info.Tokens)
	}
	ann.api("DELETE", "/api/v1/devices/"+strconv.FormatInt(info.Tokens[0].ID, 10), nil, 204, nil)
	phone.mustStatus(phone.do("PROPFIND", "/remote.php/carddav/principals/ann/", propfindDiscovery, "Depth", "0"), 401, "revoked device")
	// the "phone" token of the test harness is not a device: 404
	toks, _ := s.store.ListAPITokens(context.Background(), ann.u.ID)
	ann.api("DELETE", "/api/v1/devices/"+strconv.FormatInt(toks[0].ID, 10), nil, 404, nil)
}

func mustBook(t *testing.T, s *Server, userID int64, uid string) *store.AddressBook {
	t.Helper()
	ab, err := s.store.GetAddressBookByUID(context.Background(), userID, uid)
	if err != nil {
		t.Fatalf("book %s: %v", uid, err)
	}
	return ab
}
