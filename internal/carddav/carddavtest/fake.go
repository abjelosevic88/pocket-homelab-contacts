// Package carddavtest is a small in-memory CardDAV server for tests, shaped
// like Nextcloud: principal discovery (with a /.well-known/carddav
// redirect), address book listing, sync-collection, addressbook-multiget,
// GET, PUT and DELETE with If-Match / If-None-Match. vCards are kept and
// served byte for byte (CR escaped in XML, as Nextcloud does).
package carddavtest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server is the fake. Lock Mu when reading its maps from a test.
type Server struct {
	*httptest.Server
	Mu       sync.Mutex
	User     string
	Pass     string
	Root     string // path prefix of everything (e.g. "/remote.php/dav")
	NoSync   bool   // refuse sync-collection like an old server
	NoETag   bool   // answer PUT without an ETag (as when the server rewrote the card)
	Books    map[string]*Book
	Requests []Request
	seq      int
}

// Book is one collection.
type Book struct {
	Name     string // path segment
	Display  string
	ReadOnly bool
	Objects  map[string]*Object
	gone     map[string]int // object name → seq of its delete
	seq      int
}

// Object is one stored vCard.
type Object struct {
	Data     string
	ETag     string
	Modified time.Time
	seq      int
}

// Request is what the fake saw (for assertions).
type Request struct {
	Method      string
	Path        string
	IfMatch     string
	IfNoneMatch string
	Kind        string // REPORT kind: sync-collection, addressbook-multiget, addressbook-query
	Hrefs       int    // hrefs asked for in a multiget
}

// New starts a fake with user/pass and no address books.
func New(user, pass string) *Server {
	f := &Server{User: user, Pass: pass, Books: map[string]*Book{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *Server) principal() string { return f.Root + "/principals/users/" + f.User + "/" }
func (f *Server) home() string      { return f.Root + "/addressbooks/users/" + f.User + "/" }

// BookHref is the path of an address book.
func (f *Server) BookHref(name string) string { return f.home() + name + "/" }

// AddBook adds a collection.
func (f *Server) AddBook(name, display string, readOnly bool) *Book {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.seq++
	b := &Book{Name: name, Display: display, ReadOnly: readOnly, Objects: map[string]*Object{}, gone: map[string]int{}, seq: f.seq}
	f.Books[name] = b
	return b
}

// RemoveBook drops a collection.
func (f *Server) RemoveBook(name string) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	delete(f.Books, name)
}

func etagOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

// PutObject stores a vCard as if another client wrote it.
func (f *Server) PutObject(book, name, data string) *Object {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	return f.put(f.Books[book], name, data)
}

func (f *Server) put(b *Book, name, data string) *Object {
	f.seq++
	o := &Object{Data: data, ETag: etagOf(data + strconv.Itoa(f.seq)), Modified: time.Now().UTC(), seq: f.seq}
	b.Objects[name] = o
	delete(b.gone, name)
	b.seq = f.seq
	return o
}

// DeleteObject removes a vCard as if another client did.
func (f *Server) DeleteObject(book, name string) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.del(f.Books[book], name)
}

func (f *Server) del(b *Book, name string) {
	f.seq++
	delete(b.Objects, name)
	b.gone[name] = f.seq
	b.seq = f.seq
}

// Object returns a stored vCard (nil when missing).
func (f *Server) Object(book, name string) *Object {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if b := f.Books[book]; b != nil {
		if o := b.Objects[name]; o != nil {
			cp := *o
			return &cp
		}
	}
	return nil
}

// Count returns how many requests matched method (and REPORT kind when given).
func (f *Server) Count(method, kind string) int {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	n := 0
	for _, r := range f.Requests {
		if r.Method == method && (kind == "" || r.Kind == kind) {
			n++
		}
	}
	return n
}

// Last returns the last request with that method.
func (f *Server) Last(method string) *Request {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for i := len(f.Requests) - 1; i >= 0; i-- {
		if f.Requests[i].Method == method {
			r := f.Requests[i]
			return &r
		}
	}
	return nil
}

// ResetRequests forgets the requests seen so far.
func (f *Server) ResetRequests() {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.Requests = nil
}

func (f *Server) authorized(r *http.Request) bool {
	u, p, ok := r.BasicAuth()
	return ok && f.User != "" && u == f.User && p == f.Pass
}

var hrefRe = regexp.MustCompile(`<d:href>([^<]*)</d:href>`)
var tokenRe = regexp.MustCompile(`<d:sync-token>([^<]*)</d:sync-token>`)

func (f *Server) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	b := string(body)
	f.Mu.Lock()
	defer f.Mu.Unlock()
	req := Request{Method: r.Method, Path: r.URL.Path, IfMatch: r.Header.Get("If-Match"), IfNoneMatch: r.Header.Get("If-None-Match")}
	switch {
	case strings.Contains(b, "sync-collection"):
		req.Kind = "sync-collection"
	case strings.Contains(b, "addressbook-multiget"):
		req.Kind = "addressbook-multiget"
		req.Hrefs = len(hrefRe.FindAllString(b, -1))
	case strings.Contains(b, "addressbook-query"):
		req.Kind = "addressbook-query"
	}
	f.Requests = append(f.Requests, req)

	if r.URL.Path == "/.well-known/carddav" || r.URL.Path == "/.well-known/carddav/" {
		http.Redirect(w, r, f.Root+"/", http.StatusMovedPermanently)
		return
	}
	if !f.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Basic realm="fake"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	p := r.URL.Path
	switch r.Method {
	case "PROPFIND":
		f.propfind(w, r, p)
	case "REPORT":
		f.report(w, r, p, b, req.Kind)
	case http.MethodGet, http.MethodPut, http.MethodDelete:
		bk, name := f.locate(p)
		if bk == nil || name == "" {
			http.NotFound(w, r)
			return
		}
		f.object(w, r, bk, name, b)
	default:
		http.Error(w, "no", http.StatusMethodNotAllowed)
	}
}

// locate maps a path to an address book and an object name.
func (f *Server) locate(p string) (*Book, string) {
	if !strings.HasPrefix(p, f.home()) {
		return nil, ""
	}
	parts := strings.SplitN(strings.TrimPrefix(p, f.home()), "/", 2)
	b := f.Books[parts[0]]
	if b == nil {
		return nil, ""
	}
	if len(parts) == 1 {
		return b, ""
	}
	return b, parts[1]
}

func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func hrefEsc(p string) string { return esc((&url.URL{Path: p}).EscapedPath()) }

const msOpen = `<?xml version="1.0" encoding="utf-8"?><d:multistatus xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav" xmlns:cs="http://calendarserver.org/ns/">`

func okProp(href, props string) string {
	return `<d:response><d:href>` + hrefEsc(href) + `</d:href><d:propstat><d:prop>` + props + `</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}

func (f *Server) token(n int) string { return "http://fake.example/sync/" + strconv.Itoa(n) }

func (f *Server) bookProps(b *Book) string {
	priv := `<d:privilege><d:read/></d:privilege>`
	if !b.ReadOnly {
		priv += `<d:privilege><d:write/></d:privilege>`
	}
	return `<d:displayname>` + esc(b.Display) + `</d:displayname><d:resourcetype><d:collection/><card:addressbook/></d:resourcetype>` +
		`<d:current-user-privilege-set>` + priv + `</d:current-user-privilege-set>` +
		`<cs:getctag>ctag-` + strconv.Itoa(b.seq) + `</cs:getctag><d:sync-token>` + f.token(f.seq) + `</d:sync-token>`
}

func objectProps(o *Object) string {
	return `<d:getetag>` + esc(o.ETag) + `</d:getetag><d:getlastmodified>` + o.Modified.Format(http.TimeFormat) + `</d:getlastmodified><d:resourcetype/>`
}

func (f *Server) propfind(w http.ResponseWriter, r *http.Request, p string) {
	depth := r.Header.Get("Depth")
	var out strings.Builder
	out.WriteString(msOpen)
	switch {
	case p == f.Root+"/" || p == f.Root || p == "/" || p == f.principal():
		props := `<d:current-user-principal><d:href>` + hrefEsc(f.principal()) + `</d:href></d:current-user-principal>`
		if p == f.principal() {
			props += `<card:addressbook-home-set><d:href>` + hrefEsc(f.home()) + `</d:href></card:addressbook-home-set>`
		}
		out.WriteString(okProp(p, props))
	case p == f.home():
		out.WriteString(okProp(p, `<d:resourcetype><d:collection/></d:resourcetype>`))
		if depth == "1" {
			names := make([]string, 0, len(f.Books))
			for n := range f.Books {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				out.WriteString(okProp(f.BookHref(n), f.bookProps(f.Books[n])))
			}
		}
	default:
		b, name := f.locate(p)
		if b == nil {
			http.NotFound(w, r)
			return
		}
		if name != "" {
			o := b.Objects[name]
			if o == nil {
				http.NotFound(w, r)
				return
			}
			out.WriteString(okProp(p, objectProps(o)))
			break
		}
		out.WriteString(okProp(f.BookHref(b.Name), f.bookProps(b)))
		if depth == "1" {
			for _, n := range sortedNames(b) {
				out.WriteString(okProp(f.BookHref(b.Name)+n, objectProps(b.Objects[n])))
			}
		}
	}
	out.WriteString(`</d:multistatus>`)
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(207)
	_, _ = io.WriteString(w, out.String())
}

func sortedNames(b *Book) []string {
	names := make([]string, 0, len(b.Objects))
	for n := range b.Objects {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (f *Server) report(w http.ResponseWriter, r *http.Request, p, body, kind string) {
	b, name := f.locate(p)
	if b == nil || name != "" {
		http.NotFound(w, r)
		return
	}
	base := f.BookHref(b.Name)
	var out strings.Builder
	out.WriteString(msOpen)
	switch kind {
	case "sync-collection":
		if f.NoSync {
			http.Error(w, "not supported", http.StatusNotImplemented)
			return
		}
		since := 0
		if m := tokenRe.FindStringSubmatch(body); m != nil && m[1] != "" {
			n, err := strconv.Atoi(strings.TrimPrefix(m[1], "http://fake.example/sync/"))
			if err != nil || n > f.seq {
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, `<?xml version="1.0"?><d:error xmlns:d="DAV:"><d:valid-sync-token/></d:error>`)
				return
			}
			since = n
		}
		for _, n := range sortedNames(b) {
			if o := b.Objects[n]; o.seq > since {
				out.WriteString(okProp(base+n, `<d:getetag>`+esc(o.ETag)+`</d:getetag>`))
			}
		}
		if since > 0 {
			for n, s := range b.gone {
				if s > since {
					out.WriteString(`<d:response><d:href>` + hrefEsc(base+n) + `</d:href><d:status>HTTP/1.1 404 Not Found</d:status></d:response>`)
				}
			}
		}
		out.WriteString(`<d:sync-token>` + f.token(f.seq) + `</d:sync-token>`)
	case "addressbook-multiget":
		for _, m := range hrefRe.FindAllStringSubmatch(body, -1) {
			h, _ := url.PathUnescape(m[1])
			ob, on := f.locate(h)
			if ob == nil || ob.Objects[on] == nil {
				out.WriteString(`<d:response><d:href>` + hrefEsc(h) + `</d:href><d:status>HTTP/1.1 404 Not Found</d:status></d:response>`)
				continue
			}
			o := ob.Objects[on]
			out.WriteString(okProp(h, objectProps(o)+`<card:address-data>`+esc(o.Data)+`</card:address-data>`))
		}
	default:
		http.Error(w, "unknown report", http.StatusBadRequest)
		return
	}
	out.WriteString(`</d:multistatus>`)
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(207)
	_, _ = io.WriteString(w, out.String())
}

func (f *Server) object(w http.ResponseWriter, r *http.Request, b *Book, name, body string) {
	o := b.Objects[name]
	switch r.Method {
	case http.MethodGet:
		if o == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", o.ETag)
		w.Header().Set("Last-Modified", o.Modified.Format(http.TimeFormat))
		w.Header().Set("Content-Type", "text/vcard; charset=utf-8")
		_, _ = io.WriteString(w, o.Data)
	case http.MethodPut:
		if b.ReadOnly {
			http.Error(w, "read-only", http.StatusForbidden)
			return
		}
		if im := r.Header.Get("If-Match"); im != "" && (o == nil || o.ETag != im) {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		if r.Header.Get("If-None-Match") == "*" && o != nil {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		created := o == nil
		n := f.put(b, name, body)
		if !f.NoETag {
			w.Header().Set("ETag", n.ETag)
		}
		if created {
			w.WriteHeader(http.StatusCreated)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	case http.MethodDelete:
		if o == nil {
			http.NotFound(w, r)
			return
		}
		if im := r.Header.Get("If-Match"); im != "" && o.ETag != im {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		f.del(b, name)
		w.WriteHeader(http.StatusNoContent)
	}
}

// Card returns a minimal vCard 3.0 (CRLF line ends); extra lines go before END.
func Card(uid, fn string, extra ...string) string {
	var b strings.Builder
	b.WriteString("BEGIN:VCARD\r\nVERSION:3.0\r\nPRODID:-//fake//EN\r\nUID:" + uid + "\r\nFN:" + fn + "\r\nN:" + fn + ";;;;\r\n")
	for _, x := range extra {
		b.WriteString(x + "\r\n")
	}
	b.WriteString("END:VCARD\r\n")
	return b.String()
}
