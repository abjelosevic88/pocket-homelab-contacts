package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/carddav/carddavtest"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/config"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/store"
)

// ---------- harness ----------

type accEnv struct {
	t   *testing.T
	s   *Server
	h   http.Handler
	st  *store.Store
	ann *davUser
	bob *davUser
}

func newAccEnv(t *testing.T) *accEnv {
	t.Helper()
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(filepath.Join(dir, "contacts.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &config.Config{DataDir: dir, AuthMode: "local", Timezone: "UTC", SessionTTL: time.Hour}
	s := New(cfg, st, log, nil)
	if err := s.loadSecretKey(); err != nil {
		t.Fatal(err)
	}
	e := &accEnv{t: t, s: s, h: s.Router(), st: st}
	e.ann = newDavUser(t, s, e.h, "ann@example.com", "Ann")
	e.bob = newDavUser(t, s, e.h, "bob@example.com", "Bob")
	return e
}

func (e *accEnv) call(who *davUser, method, path string, body any) (int, []byte) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+who.token)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func (e *accEnv) sync(id int64, pushOnly bool) {
	e.t.Helper()
	if err := e.s.syncAccount(context.Background(), id, pushOnly); err != nil {
		e.t.Fatalf("sync: %v", err)
	}
}

func (e *accEnv) account(id int64) *store.Account {
	e.t.Helper()
	a, err := e.st.GetAccount(context.Background(), 0, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return a
}

func (e *accEnv) bookByName(acc int64, name string) *store.AddressBook {
	e.t.Helper()
	books, err := e.st.AddressBooksOfAccount(context.Background(), acc)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, b := range books {
		if b.Name == name {
			return b
		}
	}
	e.t.Fatalf("no book %q", name)
	return nil
}

func (e *accEnv) contacts(abID int64) map[string]*store.ContactRow {
	e.t.Helper()
	rows, err := e.st.ListContacts(context.Background(), abID)
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[string]*store.ContactRow{}
	for _, r := range rows {
		out[r.UID] = r
	}
	return out
}

func (e *accEnv) deleted(who *davUser) []store.DeletedContact {
	e.t.Helper()
	d, err := e.st.ListDeleted(context.Background(), who.u.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return d
}

type accJSON struct {
	ID           int64  `json:"id"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	Mode         string `json:"mode"`
	Via          string `json:"via"`
	AddressBooks int    `json:"addressbooks"`
	LastError    string `json:"last_error"`
	HasSecret    bool   `json:"has_secret"`
	List         []struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		ReadOnly bool   `json:"read_only"`
		Contacts int    `json:"contacts"`
	} `json:"addressbook_list"`
}

func (e *accEnv) create(body map[string]any) accJSON {
	e.t.Helper()
	code, b := e.call(e.ann, "POST", "/api/v1/accounts", body)
	if code != 201 {
		e.t.Fatalf("create account: HTTP %d: %s", code, b)
	}
	var a accJSON
	_ = json.Unmarshal(b, &a)
	return a
}

// nextcloudFake is a Nextcloud-shaped server with the two system books.
func nextcloudFake(t *testing.T) *carddavtest.Server {
	f := carddavtest.New("ann", "nc-app-pw")
	f.Root = "/remote.php/dav"
	t.Cleanup(f.Close)
	f.AddBook("contacts", "Contacts", false)
	f.AddBook("z-server-generated--system", "System address book", true)
	f.AddBook("z-app-generated--contactsinteraction--recent", "Recently contacted", false)
	f.PutObject("z-server-generated--system", "Database:ann.vcf", carddavtest.Card("sys-ann", "Ann"))
	f.PutObject("z-app-generated--contactsinteraction--recent", "r1.vcf", carddavtest.Card("recent-1", "Someone I mailed"))
	return f
}

// objectByUID finds a remote vCard by its UID.
func objectByUID(f *carddavtest.Server, book, uid string) (string, *carddavtest.Object) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for name, o := range f.Books[book].Objects {
		if strings.Contains(o.Data, "UID:"+uid+"\r\n") {
			cp := *o
			return name, &cp
		}
	}
	return "", nil
}

// ---------- discovery ----------

func TestAccountDiscoverSkipsNextcloudSystemBooks(t *testing.T) {
	e := newAccEnv(t)
	f := nextcloudFake(t)
	f.AddBook("family", "Porodica", false)
	f.PutObject("contacts", "a.vcf", carddavtest.Card("a", "Alma"))
	f.PutObject("contacts", "b.vcf", carddavtest.Card("b", "Boris"))

	// the address as copied from the browser
	code, b := e.call(e.ann, "POST", "/api/v1/accounts/discover", map[string]any{"kind": "nextcloud", "via": "direct",
		"url": f.URL + "/index.php/apps/contacts/", "username": "ann", "password": "nc-app-pw"})
	if code != 200 {
		t.Fatalf("discover: %d %s", code, b)
	}
	var out struct {
		AddressBooks []struct {
			Href  string `json:"href"`
			Name  string `json:"name"`
			Count int    `json:"count"`
		} `json:"addressbooks"`
	}
	_ = json.Unmarshal(b, &out)
	if len(out.AddressBooks) != 2 || out.AddressBooks[0].Name != "Contacts" || out.AddressBooks[0].Count != 2 ||
		out.AddressBooks[0].Href != "/remote.php/dav/addressbooks/users/ann/contacts/" || out.AddressBooks[1].Name != "Porodica" {
		t.Fatalf("discover = %s", b)
	}

	code, b = e.call(e.ann, "POST", "/api/v1/accounts/discover", map[string]any{"kind": "nextcloud", "url": f.URL, "username": "ann", "password": "wrong"})
	if code != 400 || !strings.Contains(string(b), "refused the user name or password") {
		t.Fatalf("wrong password: %d %s", code, b)
	}
	code, b = e.call(e.ann, "POST", "/api/v1/accounts/discover", map[string]any{"kind": "nextcloud", "via": "core"})
	if code != 400 || !strings.Contains(string(b), "Pocket Core is not configured") {
		t.Fatalf("core without Core: %d %s", code, b)
	}
	code, _ = e.call(e.ann, "POST", "/api/v1/accounts/discover", map[string]any{"kind": "exchange"})
	if code != 400 {
		t.Fatalf("unknown kind: %d", code)
	}
	code, b = e.call(e.ann, "GET", "/api/v1/accounts/nextcloud", nil)
	if code != 200 || !strings.Contains(string(b), `"core":false`) {
		t.Fatalf("nextcloud status: %d %s", code, b)
	}
}

// ---------- import ----------

func TestAccountImportMode(t *testing.T) {
	e := newAccEnv(t)
	f := nextcloudFake(t)
	f.AddBook("family", "Porodica", false)
	for i := 0; i < 120; i++ {
		f.PutObject("contacts", fmt.Sprintf("c%03d.vcf", i), carddavtest.Card(fmt.Sprintf("c%03d", i), fmt.Sprintf("Kontakt %d", i)))
	}
	f.PutObject("contacts", "ios.vcf", iosCard)
	group := "BEGIN:VCARD\r\nVERSION:3.0\r\nN:Porodica\r\nFN:Porodica\r\nX-ADDRESSBOOKSERVER-KIND:group\r\nX-ADDRESSBOOKSERVER-MEMBER:urn:uuid:c001\r\nUID:grp\r\nEND:VCARD\r\n"
	f.PutObject("family", "grp.vcf", group)
	// a card without a UID still comes over
	f.PutObject("family", "nouid.vcf", "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Bez UID-a\r\nEND:VCARD\r\n")

	acc := e.create(map[string]any{"kind": "nextcloud", "url": f.URL, "username": "ann", "password": "nc-app-pw", "mode": "import"})
	if acc.Name != "Nextcloud" || acc.Mode != "import" || acc.AddressBooks != 2 || !acc.HasSecret {
		t.Fatalf("account = %+v", acc)
	}
	f.ResetRequests()
	e.sync(acc.ID, false)

	a := e.account(acc.ID)
	if a.Mode != "off" || a.LastError != "" || a.LastSyncAt == "" {
		t.Fatalf("after the import: %+v", a)
	}
	book := e.bookByName(acc.ID, "Contacts")
	fam := e.bookByName(acc.ID, "Porodica")
	if book.Kind != "nextcloud" || book.ReadOnly || book.RemoteHref != "/remote.php/dav/addressbooks/users/ann/contacts/" {
		t.Fatalf("book = %+v", book)
	}
	got := e.contacts(book.ID)
	if len(got) != 121 {
		t.Fatalf("imported %d contacts", len(got))
	}
	ios := got["4F2A6C1E-9B3D-4E8F-A1B2-C3D4E5F60718"]
	if ios == nil || ios.VCF != iosCard || ios.Source != "remote" || ios.RemoteHref != "/remote.php/dav/addressbooks/users/ann/contacts/ios.vcf" || ios.Dirty {
		t.Fatalf("the iOS card did not come over byte for byte: %+v", ios)
	}
	famRows := e.contacts(fam.ID)
	if famRows["grp"] == nil || famRows["grp"].VCF != group || famRows["grp"].Kind != "group" || famRows["nouid"] == nil {
		t.Fatalf("family = %+v", famRows)
	}
	// 121 cards in batches of 50 → 3 multigets for Contacts
	f.Mu.Lock()
	multigets := 0
	for _, r := range f.Requests {
		if r.Kind == "addressbook-multiget" {
			if r.Hrefs > 50 {
				t.Errorf("a multiget of %d hrefs", r.Hrefs)
			}
			if strings.HasSuffix(r.Path, "/contacts/") {
				multigets++
			}
		}
	}
	f.Mu.Unlock()
	if multigets != 3 {
		t.Fatalf("%d multigets for 121 cards", multigets)
	}
	for _, b := range []string{"System address book", "Recently contacted"} {
		books, _ := e.st.AddressBooksOfAccount(context.Background(), acc.ID)
		for _, x := range books {
			if x.Name == b {
				t.Fatalf("%s was mirrored", b)
			}
		}
	}

	// later edits stay here: no dirty flags, nothing pending, nothing pushed
	f.ResetRequests()
	base := "/dav/addressbooks/ann/" + book.UID + "/"
	e.ann.mustStatus(e.ann.do("PUT", base+ios.Href, strings.Replace(iosCard, "Vikendica", "Selo", 1), "If-Match", ios.ETag), 204, "edit an imported card")
	e.ann.mustStatus(e.ann.do("PUT", base+"new.vcf", carddavtest.Card("new", "Novi"), "If-None-Match", "*"), 201, "new card")
	e.ann.mustStatus(e.ann.do("DELETE", base+got["c000"].Href, ""), 204, "delete an imported card")
	if rows := e.contacts(book.ID); rows["4F2A6C1E-9B3D-4E8F-A1B2-C3D4E5F60718"].Dirty || rows["new"].Dirty {
		t.Fatal("dirty flags in an imported book")
	}
	if dels, _ := e.st.RemoteDeletes(context.Background(), book.ID); len(dels) != 0 {
		t.Fatalf("pending remote deletes: %+v", dels)
	}
	if ids := e.s.accountsWithLocalChanges(context.Background()); len(ids) != 0 {
		t.Fatalf("accounts with changes: %v", ids)
	}
	e.sync(acc.ID, false)
	e.sync(acc.ID, true)
	if n := len(f.Requests); n != 0 {
		t.Fatalf("an account that is off talked to the server %d times", n)
	}
	code, b := e.call(e.ann, "POST", "/api/v1/accounts/"+strconv.FormatInt(acc.ID, 10)+"/sync", nil)
	if code != 400 || !strings.Contains(string(b), "switched off") {
		t.Fatalf("sync of an off account: %d %s", code, b)
	}
}

// ---------- two-way ----------

func TestAccountTwoWay(t *testing.T) {
	for _, noSync := range []bool{false, true} {
		t.Run(fmt.Sprintf("noSync=%v", noSync), func(t *testing.T) {
			e := newAccEnv(t)
			f := nextcloudFake(t)
			f.NoSync = noSync
			f.PutObject("contacts", "a.vcf", carddavtest.Card("a", "Alma"))
			f.PutObject("contacts", "b.vcf", carddavtest.Card("b", "Boris"))
			f.PutObject("contacts", "c.vcf", carddavtest.Card("c", "Cvijeta"))

			acc := e.create(map[string]any{"kind": "nextcloud", "url": f.URL + "/remote.php/dav/", "username": "ann", "password": "nc-app-pw",
				"mode": "two_way", "hrefs": []string{"/remote.php/dav/addressbooks/users/ann/contacts/"}})
			e.sync(acc.ID, false)
			book := e.bookByName(acc.ID, "Contacts")
			rows := e.contacts(book.ID)
			if len(rows) != 3 || e.account(acc.ID).Mode != "two_way" {
				t.Fatalf("first pull: %d contacts", len(rows))
			}
			base := "/dav/addressbooks/ann/" + book.UID + "/"

			// a local edit goes over with If-Match
			edited := carddavtest.Card("a", "Alma Avdić", "EMAIL:alma@example.com")
			e.ann.mustStatus(e.ann.do("PUT", base+rows["a"].Href, edited, "If-Match", rows["a"].ETag), 204, "local edit")
			if !e.contacts(book.ID)["a"].Dirty {
				t.Fatal("a local edit in a two-way book is not dirty")
			}
			before := f.Object("contacts", "a.vcf")
			e.sync(acc.ID, true)
			if o := f.Object("contacts", "a.vcf"); o == nil || o.Data != edited {
				t.Fatalf("remote after push: %+v", o)
			}
			if last := f.Last("PUT"); last.IfMatch != before.ETag {
				t.Fatalf("push If-Match = %q, want %q", last.IfMatch, before.ETag)
			}
			if r := e.contacts(book.ID)["a"]; r.Dirty || r.RemoteETag != f.Object("contacts", "a.vcf").ETag {
				t.Fatalf("after push: %+v", r)
			}

			// a new local contact is created over there with If-None-Match
			e.ann.mustStatus(e.ann.do("PUT", base+"d.vcf", carddavtest.Card("d", "Dino"), "If-None-Match", "*"), 201, "local create")
			e.sync(acc.ID, false)
			if name, o := objectByUID(f, "contacts", "d"); o == nil || name != "d.vcf" || f.Last("PUT").IfNoneMatch != "*" {
				t.Fatalf("new contact over there: %q %+v", name, o)
			}

			// a remote edit comes here byte for byte
			remote := carddavtest.Card("b", "Boris Babić", "TEL;TYPE=cell:+387 61 000 111", "NOTE:sa Nextclouda")
			f.PutObject("contacts", "b.vcf", remote)
			e.sync(acc.ID, false)
			if r := e.contacts(book.ID)["b"]; r.VCF != remote || r.Dirty {
				t.Fatalf("remote edit here: %q", r.VCF)
			}

			// a remote delete deletes here (into Recently deleted)
			f.DeleteObject("contacts", "c.vcf")
			e.sync(acc.ID, false)
			if e.contacts(book.ID)["c"] != nil {
				t.Fatal("a remote delete did not delete here")
			}
			if d := e.deleted(e.ann); len(d) != 1 || d[0].UID != "c" {
				t.Fatalf("recently deleted = %+v", d)
			}

			// a local delete deletes over there, with If-Match
			bRow := e.contacts(book.ID)["b"]
			e.ann.mustStatus(e.ann.do("DELETE", base+bRow.Href, ""), 204, "local delete")
			if ids := e.s.accountsWithLocalChanges(context.Background()); len(ids) != 1 {
				t.Fatalf("accounts with changes: %v", ids)
			}
			e.sync(acc.ID, true)
			if f.Object("contacts", "b.vcf") != nil {
				t.Fatal("a local delete did not reach the server")
			}
			if last := f.Last("DELETE"); last.IfMatch != bRow.RemoteETag {
				t.Fatalf("delete If-Match = %q", last.IfMatch)
			}
			if dels, _ := e.st.RemoteDeletes(context.Background(), book.ID); len(dels) != 0 {
				t.Fatalf("pending deletes left: %+v", dels)
			}
			if a := e.account(acc.ID); a.LastError != "" {
				t.Fatalf("last error: %s", a.LastError)
			}
			if !noSync && f.Count("REPORT", "sync-collection") == 0 {
				t.Fatal("sync-collection was never used")
			}
		})
	}
}

func TestAccountConflicts(t *testing.T) {
	e := newAccEnv(t)
	f := nextcloudFake(t)
	f.PutObject("contacts", "a.vcf", carddavtest.Card("a", "Alma", "REV:20261020T080000Z"))
	f.PutObject("contacts", "b.vcf", carddavtest.Card("b", "Boris", "REV:20261020T080000Z"))
	acc := e.create(map[string]any{"kind": "nextcloud", "url": f.URL, "username": "ann", "password": "nc-app-pw", "mode": "two_way"})
	e.sync(acc.ID, false)
	book := e.bookByName(acc.ID, "Contacts")
	base := "/dav/addressbooks/ann/" + book.UID + "/"
	rows := e.contacts(book.ID)

	// both changed, theirs has the newer REV: theirs wins, ours goes to Recently deleted
	ours := carddavtest.Card("a", "Alma (ovdje)", "REV:20261021T100000Z")
	theirs := carddavtest.Card("a", "Alma (tamo)", "REV:20261021T120000Z")
	e.ann.mustStatus(e.ann.do("PUT", base+rows["a"].Href, ours, "If-Match", rows["a"].ETag), 204, "local edit a")
	f.PutObject("contacts", "a.vcf", theirs)
	e.sync(acc.ID, false)
	if r := e.contacts(book.ID)["a"]; r.VCF != theirs || r.Dirty {
		t.Fatalf("the newer remote edit lost: %q dirty=%v", r.VCF, r.Dirty)
	}
	if o := f.Object("contacts", "a.vcf"); o.Data != theirs {
		t.Fatal("the losing local edit was pushed")
	}
	if d := e.deleted(e.ann); len(d) != 1 || d[0].VCF != ours || d[0].FN != "Alma (ovdje)" {
		t.Fatalf("recently deleted = %+v", d)
	}

	// both changed, ours is newer: a push-only sync meets a 412, keeps ours and theirs goes aside
	ours = carddavtest.Card("b", "Boris (ovdje)", "REV:20261021T140000Z")
	theirs = carddavtest.Card("b", "Boris (tamo)", "REV:20261021T130000Z")
	e.ann.mustStatus(e.ann.do("PUT", base+rows["b"].Href, ours, "If-Match", rows["b"].ETag), 204, "local edit b")
	f.PutObject("contacts", "b.vcf", theirs)
	f.ResetRequests()
	e.sync(acc.ID, true)
	if o := f.Object("contacts", "b.vcf"); o.Data != ours {
		t.Fatalf("the newer local edit did not win: %q", o.Data)
	}
	puts := 0
	f.Mu.Lock()
	for _, r := range f.Requests {
		if r.Method == "PUT" {
			puts++
		}
	}
	f.Mu.Unlock()
	if puts != 2 || f.Count("GET", "") != 1 {
		t.Fatalf("412 handling: %d PUTs, %d GETs", puts, f.Count("GET", ""))
	}
	if r := e.contacts(book.ID)["b"]; r.Dirty || r.VCF != ours || r.RemoteETag != f.Object("contacts", "b.vcf").ETag {
		t.Fatalf("after the push: %+v", r)
	}
	found := false
	for _, d := range e.deleted(e.ann) {
		if d.VCF == theirs {
			found = true
		}
	}
	if !found {
		t.Fatal("the losing remote version is not in Recently deleted")
	}

	// a local delete of something changed over there: the change wins, the contact comes back
	rows = e.contacts(book.ID)
	e.ann.mustStatus(e.ann.do("DELETE", base+rows["b"].Href, ""), 204, "local delete b")
	again := carddavtest.Card("b", "Boris (opet)")
	f.PutObject("contacts", "b.vcf", again)
	e.sync(acc.ID, true)
	if r := e.contacts(book.ID)["b"]; r == nil || r.VCF != again {
		t.Fatalf("the remotely edited contact did not come back: %+v", r)
	}
	if f.Object("contacts", "b.vcf") == nil {
		t.Fatal("the remote edit was deleted")
	}

	// a remote delete of something edited here: ours goes back as new
	rows = e.contacts(book.ID)
	mine := carddavtest.Card("b", "Boris (moj)")
	e.ann.mustStatus(e.ann.do("PUT", base+rows["b"].Href, mine, "If-Match", rows["b"].ETag), 204, "edit b")
	f.DeleteObject("contacts", "b.vcf")
	e.sync(acc.ID, false)
	if _, o := objectByUID(f, "contacts", "b"); o == nil || o.Data != mine {
		t.Fatalf("the local edit did not go back as new: %+v", o)
	}
}

// ---------- read-only, modes, ownership ----------

func TestAccountReadOnlyAndModes(t *testing.T) {
	e := newAccEnv(t)
	f := carddavtest.New("ann@icloud.com", "app-specific")
	t.Cleanup(f.Close)
	f.AddBook("card", "card", false)
	f.PutObject("card", "a.vcf", carddavtest.Card("a", "Alma"))
	old := icloudURL
	icloudURL = f.URL
	t.Cleanup(func() { icloudURL = old })

	acc := e.create(map[string]any{"kind": "icloud", "username": "ann@icloud.com", "password": "app-specific", "mode": "read_only"})
	if acc.Name != "iCloud" || acc.Kind != "icloud" || len(acc.List) != 1 || !acc.List[0].ReadOnly {
		t.Fatalf("account = %+v", acc)
	}
	e.sync(acc.ID, false)
	book := e.bookByName(acc.ID, "card")
	if book.Kind != "icloud" || !book.ReadOnly || len(e.contacts(book.ID)) != 1 {
		t.Fatalf("read-only mirror = %+v", book)
	}
	base := "/dav/addressbooks/ann/" + book.UID + "/"
	w := e.ann.do("PROPFIND", base, propfindBook, "Depth", "0")
	if strings.Contains(w.Body.String(), "<d:write/>") {
		t.Fatal("a read-only mirror advertises write")
	}
	a := e.contacts(book.ID)["a"]
	e.ann.mustStatus(e.ann.do("PUT", base+a.Href, carddavtest.Card("a", "Alma X")), 403, "edit a read-only mirror")
	e.ann.mustStatus(e.ann.do("DELETE", base+a.Href, ""), 403, "delete in a read-only mirror")
	f.PutObject("card", "a.vcf", carddavtest.Card("a", "Alma Y"))
	e.sync(acc.ID, false)
	if e.contacts(book.ID)["a"].FN != "Alma Y" {
		t.Fatal("a read-only mirror does not pull")
	}
	if f.Count("PUT", "") != 0 {
		t.Fatal("a read-only mirror pushed")
	}

	// to two-way: writable here, edits go over
	id := strconv.FormatInt(acc.ID, 10)
	code, b := e.call(e.ann, "PUT", "/api/v1/accounts/"+id, map[string]any{"mode": "two_way"})
	if code != 200 || !strings.Contains(string(b), `"mode":"two_way"`) {
		t.Fatalf("mode change: %d %s", code, b)
	}
	a = e.contacts(book.ID)["a"]
	e.ann.mustStatus(e.ann.do("PUT", base+a.Href, carddavtest.Card("a", "Alma Z"), "If-Match", a.ETag), 204, "edit after two-way")
	e.sync(acc.ID, true)
	if f.Object("card", "a.vcf").Data != carddavtest.Card("a", "Alma Z") {
		t.Fatal("two-way after read-only did not push")
	}

	// to off: unsent changes are dropped, nothing talks to the server
	a = e.contacts(book.ID)["a"]
	e.ann.mustStatus(e.ann.do("PUT", base+a.Href, carddavtest.Card("a", "Alma W"), "If-Match", a.ETag), 204, "edit before off")
	code, _ = e.call(e.ann, "PUT", "/api/v1/accounts/"+id, map[string]any{"mode": "off"})
	if code != 200 || e.contacts(book.ID)["a"].Dirty {
		t.Fatal("switching off kept a dirty flag")
	}

	// other people see none of it
	for _, req := range [][2]string{{"GET", ""}, {"PUT", ""}, {"DELETE", ""}, {"POST", "/sync"}} {
		code, _ := e.call(e.bob, req[0], "/api/v1/accounts/"+id+req[1], map[string]any{"name": "mine"})
		if code != 404 {
			t.Fatalf("bob %s account: %d", req[0], code)
		}
	}
	code, b = e.call(e.bob, "GET", "/api/v1/accounts", nil)
	if code != 200 || strings.TrimSpace(string(b)) != "[]" {
		t.Fatalf("bob's accounts: %s", b)
	}

	// removing the account but keeping the books makes them ordinary books
	code, _ = e.call(e.ann, "DELETE", "/api/v1/accounts/"+id+"?keep=1", nil)
	if code != 204 {
		t.Fatalf("delete account: %d", code)
	}
	kept, err := e.st.GetAddressBook(context.Background(), book.ID)
	if err != nil || kept.Kind != "local" || kept.AccountID != nil || kept.ReadOnly || len(e.contacts(book.ID)) != 1 {
		t.Fatalf("kept book = %+v, %v", kept, err)
	}

	// a duplicate account is refused
	e.create(map[string]any{"kind": "icloud", "username": "ann@icloud.com", "password": "app-specific", "mode": "read_only"})
	code, b = e.call(e.ann, "POST", "/api/v1/accounts", map[string]any{"kind": "icloud", "username": "ann@icloud.com", "password": "app-specific"})
	if code != 409 {
		t.Fatalf("duplicate account: %d %s", code, b)
	}
}

func TestAccountSyncNowAndRemoteBookGone(t *testing.T) {
	e := newAccEnv(t)
	f := nextcloudFake(t)
	f.AddBook("family", "Porodica", false)
	f.PutObject("family", "x.vcf", carddavtest.Card("x", "Xenia"))
	acc := e.create(map[string]any{"kind": "carddav", "url": f.URL, "username": "ann", "password": "nc-app-pw", "mode": "two_way"})
	if acc.AddressBooks != 2 {
		t.Fatalf("account = %+v", acc)
	}
	code, b := e.call(e.ann, "POST", "/api/v1/accounts/"+strconv.FormatInt(acc.ID, 10)+"/sync", nil)
	var view accJSON
	_ = json.Unmarshal(b, &view)
	if code != 200 || view.LastError != "" {
		t.Fatalf("sync now: %d %s", code, b)
	}
	fam := e.bookByName(acc.ID, "Porodica")
	if len(e.contacts(fam.ID)) != 1 {
		t.Fatal("sync now did not pull")
	}

	// the book is deleted over there: it stays here as an ordinary book
	f.RemoveBook("family")
	if err := e.s.syncAccount(context.Background(), acc.ID, false); err == nil || !strings.Contains(err.Error(), "ordinary address book") {
		t.Fatalf("sync after a remote book went: %v", err)
	}
	kept, _ := e.st.GetAddressBook(context.Background(), fam.ID)
	if kept.Kind != "local" || kept.AccountID != nil || len(e.contacts(fam.ID)) != 1 {
		t.Fatalf("kept book = %+v", kept)
	}

	// an empty listing never empties a book here
	book := e.bookByName(acc.ID, "Contacts")
	f.PutObject("contacts", "y.vcf", carddavtest.Card("y", "Yara"))
	e.sync(acc.ID, false)
	f.DeleteObject("contacts", "y.vcf")
	e.st.DB.Exec(`UPDATE addressbooks SET remote_token = '', remote_ctag = '' WHERE id = ?`, book.ID)
	if err := e.s.syncAccount(context.Background(), acc.ID, false); err == nil || !strings.Contains(err.Error(), "listed no contacts") {
		t.Fatalf("empty listing: %v", err)
	}
	if len(e.contacts(book.ID)) != 1 {
		t.Fatal("an empty listing emptied the book")
	}
}
