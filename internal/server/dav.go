package server

import (
	"context"
	"database/sql"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/store"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/vcard"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// A CardDAV server (RFC 6352, RFC 6578 and the Apple extensions clients
// ask for) for iOS/macOS Contacts, DAVx5 and Thunderbird. The same tree
// answers under three prefixes:
//
//	/dav/…                  ours
//	/remote.php/dav/…       Nextcloud's (people and homes under users/)
//	/remote.php/carddav/…   Nextcloud's legacy one: iOS only lists an account
//	                        under Settings → Contacts → Default Account when
//	                        it was added with …/remote.php/carddav/principals/<user>/
//	                        (nextcloud/contacts#1870)
//
// and below each:
//
//	/                                       root: current-user-principal
//	/principals/<user>/                     the person (also principals/users/<user>/)
//	/addressbooks/<user>/                   address book home (also addressbooks/users/<user>/)
//	/addressbooks/<user>/<book uid>/        one of the person's own address books
//	/addressbooks/<user>/shared-<id>/       a book someone shared with them
//	/addressbooks/<user>/<book>/<name>.vcf  one vCard, byte for byte as it was written
//
// <user> is the person's CardDAV name (Settings → Profile), their email, or
// "me"; another person's name answers 404, so nobody learns who else is on
// the box. Shared books carry the role of the share: read shares (and books
// mirrored read-only from an account) advertise read privileges only and
// refuse writes with 403.
//
// iOS and macOS show one address book per account: the first in the home,
// which is the person's default book.

const (
	nsDAV   = "DAV:"
	nsCard  = "urn:ietf:params:xml:ns:carddav"
	nsCS    = "http://calendarserver.org/ns/"
	nsApple = "http://apple.com/ns/ical/"
	nsOC    = "http://owncloud.org/ns"

	syncPfx     = "http://pocket-contacts/sync/"
	sharedPfx   = "shared-"
	davCaps     = "1, 3, addressbook, extended-mkcol"
	davRealm    = `Basic realm="Pocket Contacts"`
	maxVCFBytes = 10 << 20
)

// davRoutes mounts the CardDAV tree, the well-known redirect and the iPhone
// profile download (devices.go).
func (s *Server) davRoutes(r chi.Router) {
	r.Handle("/.well-known/carddav", http.RedirectHandler("/dav/", http.StatusMovedPermanently))
	r.Handle("/.well-known/carddav/", http.RedirectHandler("/dav/", http.StatusMovedPermanently))
	for _, p := range []string{"/dav", "/remote.php/dav", "/remote.php/carddav"} {
		r.Handle(p, http.HandlerFunc(s.handleDAV))
		r.Handle(p+"/*", http.HandlerFunc(s.handleDAV))
	}
	r.Handle("/principals/*", http.HandlerFunc(s.handleDAV))
	r.Get("/dav-profile/{file}", s.handleDeviceProfile)
}

type davReq struct {
	prefix string // "/dav", "/remote.php/dav" or "/remote.php/carddav"
	users  bool   // Nextcloud's principals/users/… and addressbooks/users/… shape
	user   *store.User
	parts  []string // path segments after the prefix (users/ dropped)
	depth  string
	cols   []*davColl // what the home holds, loaded once per request
	loaded bool
}

// davColl is one address book of the home.
type davColl struct {
	seg  string
	ab   *store.AddressBook // filled per viewer: role, prefs, owner
	role string
}

// writable reports whether vCards may be written into the collection.
func (c *davColl) writable() bool {
	return store.RoleAtLeast(c.role, store.RoleWrite) && !c.ab.ReadOnly
}

// davPath splits a request path into its prefix and segments.
func davPath(p string) (prefix string, users bool, parts []string) {
	prefix = "/dav"
	rest := p
	switch {
	case p == "/remote.php/dav" || strings.HasPrefix(p, "/remote.php/dav/"):
		prefix, users = "/remote.php/dav", true
		rest = strings.TrimPrefix(p, prefix)
	case p == "/remote.php/carddav" || strings.HasPrefix(p, "/remote.php/carddav/"):
		prefix = "/remote.php/carddav"
		rest = strings.TrimPrefix(p, prefix)
	case p == "/dav" || strings.HasPrefix(p, "/dav/"):
		rest = strings.TrimPrefix(p, prefix)
	}
	// otherwise /principals/<user>/ as Apple sometimes derives it: under /dav
	for _, seg := range strings.Split(rest, "/") {
		if seg != "" {
			parts = append(parts, seg)
		}
	}
	// Nextcloud keeps people and homes under …/users/<user>/
	if len(parts) >= 2 && (parts[0] == "principals" || parts[0] == "addressbooks") && parts[1] == "users" {
		parts = append([]string{parts[0]}, parts[2:]...)
	}
	return prefix, users, parts
}

// handleDAV routes every request under the DAV prefixes.
func (s *Server) handleDAV(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		s.davOptions(w)
		return
	}
	u, err := s.resolveUser(r)
	if err != nil || u == nil {
		w.Header().Set("WWW-Authenticate", davRealm)
		w.Header().Set("DAV", davCaps)
		http.Error(w, "sign in with your CardDAV name and an API token (Settings → Phones)", http.StatusUnauthorized)
		return
	}
	prefix, users, parts := davPath(r.URL.Path)
	req := &davReq{prefix: prefix, users: users, user: u, parts: parts, depth: strings.ToLower(r.Header.Get("Depth"))}
	if req.depth == "" {
		req.depth = "infinity"
	}
	ctx := r.Context()
	w.Header().Set("DAV", davCaps)
	switch {
	case len(parts) == 0:
		s.davRoot(w, r, req)
	case len(parts) == 1 && (parts[0] == "principals" || parts[0] == "addressbooks"):
		s.davRoot(w, r, req)
	case len(parts) == 1:
		s.davNotFound(w)
	case !davUserMatches(parts[1], u):
		s.davNotFound(w)
	case parts[0] == "principals" && len(parts) == 2:
		s.davPrincipal(w, r, req)
	case parts[0] == "addressbooks" && len(parts) == 2:
		s.davHome(ctx, w, r, req)
	case parts[0] == "addressbooks" && len(parts) == 3:
		s.davCollection(ctx, w, r, req, parts[2])
	case parts[0] == "addressbooks" && len(parts) == 4:
		s.davResource(ctx, w, r, req, parts[2], parts[3])
	default:
		s.davNotFound(w)
	}
}

func davUserMatches(seg string, u *store.User) bool {
	seg = strings.ToLower(seg)
	email := strings.ToLower(u.Email)
	return seg == "me" || (u.Username != "" && seg == strings.ToLower(u.Username)) || seg == email ||
		seg == strconv.FormatInt(u.ID, 10) || seg == strings.Split(email, "@")[0]
}

func (s *Server) davOptions(w http.ResponseWriter) {
	w.Header().Set("DAV", davCaps)
	w.Header().Set("Allow", "OPTIONS, GET, HEAD, PUT, DELETE, PROPFIND, PROPPATCH, REPORT, MKCOL, MOVE")
	w.WriteHeader(http.StatusOK)
}

func (s *Server) davNotFound(w http.ResponseWriter) {
	http.Error(w, "not found", http.StatusNotFound)
}

func (s *Server) davMethodNotAllowed(w http.ResponseWriter) {
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

// davError answers with a DAV:error body naming the failed precondition.
func davError(w http.ResponseWriter, status int, cond string) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>` + "\n" +
		`<d:error xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav">` + cond + `</d:error>` + "\n"))
}

func (s *Server) davFail(w http.ResponseWriter, err error, what string) {
	s.log.Error("carddav: "+what, "err", err)
	http.Error(w, what+": "+err.Error(), http.StatusInternalServerError)
}

// ---------- XML in ----------

type xnode struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Text    string     `xml:",chardata"`
	Nodes   []xnode    `xml:",any"`
}

func (n *xnode) child(local string) *xnode {
	if n == nil {
		return nil
	}
	for i := range n.Nodes {
		if n.Nodes[i].XMLName.Local == local {
			return &n.Nodes[i]
		}
	}
	return nil
}

func (n *xnode) children(local string) []*xnode {
	if n == nil {
		return nil
	}
	var out []*xnode
	for i := range n.Nodes {
		if n.Nodes[i].XMLName.Local == local {
			out = append(out, &n.Nodes[i])
		}
	}
	return out
}

// propNodes are the properties under a set/remove element.
func (n *xnode) propNodes() []xnode {
	if p := n.child("prop"); p != nil {
		return p.Nodes
	}
	return nil
}

func (n *xnode) attr(name string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func (n *xnode) textOr(def string) string {
	if n == nil {
		return def
	}
	return n.Text
}

func readXML(r *http.Request) (*xnode, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, nil
	}
	var n xnode
	if err := xml.Unmarshal(body, &n); err != nil {
		return nil, err
	}
	return &n, nil
}

// propName is a requested property.
type propName struct{ Space, Local string }

func requestedProps(n *xnode) (props []propName, allprop bool) {
	if n == nil {
		return nil, true
	}
	if n.child("allprop") != nil || n.child("propname") != nil {
		return nil, true
	}
	p := n.child("prop")
	if p == nil {
		return nil, true
	}
	for _, c := range p.Nodes {
		props = append(props, propName{c.XMLName.Space, c.XMLName.Local})
	}
	return props, false
}

func propTag(p xnode) string {
	return `<x:` + p.XMLName.Local + ` xmlns:x="` + xesc(p.XMLName.Space) + `"/>`
}

// ---------- XML out ----------

type propstat struct {
	found    []string // rendered <prop> children
	notFound []propName
}

func (ps *propstat) add(xmlFragment string) { ps.found = append(ps.found, xmlFragment) }

func xesc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func hrefEsc(p string) string {
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		parts[i] = url.PathEscape(seg)
	}
	return strings.Join(parts, "/")
}

func hrefXML(p string) string { return "<d:href>" + xesc(hrefEsc(p)) + "</d:href>" }

type msWriter struct {
	b strings.Builder
}

func newMultistatus() *msWriter {
	m := &msWriter{}
	m.b.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	m.b.WriteString(`<d:multistatus xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav" xmlns:cs="http://calendarserver.org/ns/">` + "\n")
	return m
}

// response writes one resource; href is a path (escaped here).
func (m *msWriter) response(href string, ps *propstat) {
	m.responseRaw(hrefEsc(href), ps)
}

// responseRaw writes one resource under an href already in wire form
// (multiget echoes what the client asked for).
func (m *msWriter) responseRaw(href string, ps *propstat) {
	m.b.WriteString("<d:response><d:href>" + xesc(href) + "</d:href>")
	if len(ps.found) > 0 {
		m.b.WriteString("<d:propstat><d:prop>" + strings.Join(ps.found, "") + "</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>")
	}
	if len(ps.notFound) > 0 {
		m.b.WriteString("<d:propstat><d:prop>")
		for _, p := range ps.notFound {
			m.b.WriteString(`<x:` + p.Local + ` xmlns:x="` + xesc(p.Space) + `"/>`)
		}
		m.b.WriteString("</d:prop><d:status>HTTP/1.1 404 Not Found</d:status></d:propstat>")
	}
	if len(ps.found) == 0 && len(ps.notFound) == 0 {
		m.b.WriteString("<d:propstat><d:prop/><d:status>HTTP/1.1 200 OK</d:status></d:propstat>")
	}
	m.b.WriteString("</d:response>\n")
}

func (m *msWriter) gone(href string) { m.goneRaw(hrefEsc(href)) }

func (m *msWriter) goneRaw(href string) {
	m.b.WriteString("<d:response><d:href>" + xesc(href) + "</d:href><d:status>HTTP/1.1 404 Not Found</d:status></d:response>\n")
}

func (m *msWriter) write(w http.ResponseWriter, extra string) {
	m.b.WriteString(extra)
	m.b.WriteString("</d:multistatus>\n")
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	_, _ = w.Write([]byte(m.b.String()))
}

// ---------- the home's collections ----------

func principalName(u *store.User) string { return firstNonEmpty(u.Username, u.Email) }

func (req *davReq) usersSeg() string {
	if req.users {
		return "users/"
	}
	return ""
}

func (s *Server) principalHref(req *davReq) string {
	return req.prefix + "/principals/" + req.usersSeg() + principalName(req.user) + "/"
}

func (s *Server) homeHref(req *davReq) string {
	return req.prefix + "/addressbooks/" + req.usersSeg() + principalName(req.user) + "/"
}

func sharedSeg(id int64) string { return sharedPfx + strconv.FormatInt(id, 10) }

// reservedSeg reports whether a path segment names a collection the server
// makes up (so MKCOL may not take it).
func reservedSeg(seg string) bool {
	if rest, ok := strings.CutPrefix(strings.ToLower(seg), sharedPfx); ok {
		_, err := strconv.ParseInt(rest, 10, 64)
		return err == nil
	}
	return false
}

// collections lists what the person's address book home holds: their own
// books (the default one first: the one iOS shows) and the books shared
// with them that they have not hidden or left.
func (s *Server) collections(ctx context.Context, req *davReq) ([]*davColl, error) {
	if req.loaded {
		return req.cols, nil
	}
	uid := req.user.ID
	books, err := s.store.ListAddressBooksFor(ctx, uid)
	if err != nil {
		return nil, err
	}
	if len(books) == 0 {
		s.seedUser(ctx, req.user)
		if books, err = s.store.ListAddressBooksFor(ctx, uid); err != nil {
			return nil, err
		}
	}
	own := map[string]bool{}
	for _, b := range books {
		if b.Role == store.RoleOwner {
			own[b.UID] = true
		}
	}
	var mine, shared []*davColl
	for _, b := range books {
		switch {
		case b.Role == store.RoleOwner:
			mine = append(mine, &davColl{seg: b.UID, ab: b, role: b.Role})
		case b.Role == "", !b.Visible:
			// hidden or left: gone from the person's lists, CardDAV included
		case own[sharedSeg(b.ID)]:
			// an own book took the name; the share stays reachable on the web
		default:
			shared = append(shared, &davColl{seg: sharedSeg(b.ID), ab: b, role: b.Role})
		}
	}
	sort.SliceStable(mine, func(i, j int) bool { return mine[i].ab.IsDefault && !mine[j].ab.IsDefault })
	req.cols, req.loaded = append(mine, shared...), true
	return req.cols, nil
}

func (s *Server) resolveCollection(ctx context.Context, req *davReq, seg string) (*davColl, error) {
	cols, err := s.collections(ctx, req)
	if err != nil {
		return nil, err
	}
	for _, c := range cols {
		if c.seg == seg {
			return c, nil
		}
	}
	return nil, store.ErrNotFound
}

// ---------- nodes and properties ----------

type davNode struct {
	kind string // root | principal | home | addressbook | resource
	href string
	coll *davColl
	row  *store.ContactRow
	req  *davReq
}

func (s *Server) collNode(req *davReq, c *davColl) *davNode {
	return &davNode{kind: "addressbook", href: s.homeHref(req) + c.seg + "/", coll: c, req: req}
}

func (s *Server) cardNode(req *davReq, c *davColl, row *store.ContactRow) *davNode {
	return &davNode{kind: "resource", href: s.homeHref(req) + c.seg + "/" + row.Href, coll: c, row: row, req: req}
}

func collDisplayName(c *davColl) string {
	if c.role != store.RoleOwner && c.ab.Owner != nil {
		return c.ab.Name + " (" + firstNonEmpty(c.ab.Owner.Name, c.ab.Owner.Email) + ")"
	}
	return c.ab.Name
}

const (
	privRead  = "<d:privilege><d:read/></d:privilege><d:privilege><d:read-current-user-privilege-set/></d:privilege>"
	privWrite = "<d:privilege><d:write/></d:privilege><d:privilege><d:write-properties/></d:privilege><d:privilege><d:write-content/></d:privilege><d:privilege><d:bind/></d:privilege><d:privilege><d:unbind/></d:privilege>"
	reportSet = "<d:supported-report-set><d:supported-report><d:report><card:addressbook-query/></d:report></d:supported-report><d:supported-report><d:report><card:addressbook-multiget/></d:report></d:supported-report><d:supported-report><d:report><d:sync-collection/></d:report></d:supported-report></d:supported-report-set>"
)

// props answers a property request for one node.
func (s *Server) props(n *davNode, want []propName, all bool) *propstat {
	ps := &propstat{}
	req := n.req
	u := req.user
	book := n.kind == "addressbook"
	get := func(space, local string) (string, bool) {
		switch space + " " + local {
		case nsDAV + " resourcetype":
			switch n.kind {
			case "principal":
				return "<d:resourcetype><d:principal/><d:collection/></d:resourcetype>", true
			case "addressbook":
				return "<d:resourcetype><d:collection/><card:addressbook/></d:resourcetype>", true
			case "resource":
				return "<d:resourcetype/>", true
			}
			return "<d:resourcetype><d:collection/></d:resourcetype>", true
		case nsDAV + " displayname":
			switch n.kind {
			case "principal", "home":
				return "<d:displayname>" + xesc(firstNonEmpty(u.Name, u.Email)) + "</d:displayname>", true
			case "addressbook":
				return "<d:displayname>" + xesc(collDisplayName(n.coll)) + "</d:displayname>", true
			case "resource":
				return "", false
			}
			return "<d:displayname>Pocket Contacts</d:displayname>", true
		case nsDAV + " current-user-principal":
			return "<d:current-user-principal>" + hrefXML(s.principalHref(req)) + "</d:current-user-principal>", true
		case nsDAV + " principal-URL":
			if n.kind == "principal" {
				return "<d:principal-URL>" + hrefXML(s.principalHref(req)) + "</d:principal-URL>", true
			}
		case nsDAV + " principal-collection-set":
			return "<d:principal-collection-set>" + hrefXML(req.prefix+"/principals/"+req.usersSeg()) + "</d:principal-collection-set>", true
		case nsDAV + " owner":
			if n.kind == "principal" || n.kind == "home" || (book && n.coll.role == store.RoleOwner) {
				return "<d:owner>" + hrefXML(s.principalHref(req)) + "</d:owner>", true
			}
		case nsCard + " addressbook-home-set":
			if n.kind == "principal" {
				return "<card:addressbook-home-set>" + hrefXML(s.homeHref(req)) + "</card:addressbook-home-set>", true
			}
		case nsCS + " email-address-set":
			if n.kind == "principal" {
				return "<cs:email-address-set><cs:email-address>" + xesc(u.Email) + "</cs:email-address></cs:email-address-set>", true
			}
		case nsDAV + " current-user-privilege-set":
			priv := privRead
			if n.kind == "home" || (n.coll != nil && n.coll.writable()) {
				priv += privWrite
			}
			return "<d:current-user-privilege-set>" + priv + "</d:current-user-privilege-set>", true
		case nsDAV + " supported-report-set":
			if book || n.kind == "home" {
				return reportSet, true
			}
			return "<d:supported-report-set/>", true
		case nsCard + " supported-address-data":
			if book {
				return `<card:supported-address-data><card:address-data-type content-type="text/vcard" version="3.0"/><card:address-data-type content-type="text/vcard" version="4.0"/></card:supported-address-data>`, true
			}
		case nsCard + " addressbook-description":
			if book {
				return "<card:addressbook-description>" + xesc(n.coll.ab.Description) + "</card:addressbook-description>", true
			}
		case nsCard + " max-resource-size":
			if book {
				return "<card:max-resource-size>" + strconv.Itoa(maxVCFBytes) + "</card:max-resource-size>", true
			}
		case nsCS + " getctag":
			if book {
				return "<cs:getctag>" + strconv.FormatInt(n.coll.ab.Seq, 10) + "</cs:getctag>", true
			}
		case nsDAV + " sync-token":
			if book {
				return "<d:sync-token>" + xesc(syncPfx+strconv.FormatInt(n.coll.ab.Seq, 10)) + "</d:sync-token>", true
			}
		case nsDAV + " quota-used-bytes":
			if book {
				return "<d:quota-used-bytes>0</d:quota-used-bytes>", true
			}
		case nsDAV + " quota-available-bytes":
			if book {
				return "<d:quota-available-bytes>10737418240</d:quota-available-bytes>", true
			}
		case nsDAV + " getetag":
			if n.kind == "resource" {
				return "<d:getetag>" + xesc(n.row.ETag) + "</d:getetag>", true
			}
		case nsDAV + " getcontenttype":
			if n.kind == "resource" {
				return "<d:getcontenttype>text/vcard; charset=utf-8</d:getcontenttype>", true
			}
		case nsDAV + " getcontentlength":
			if n.kind == "resource" {
				return "<d:getcontentlength>" + strconv.Itoa(len(n.row.VCF)) + "</d:getcontentlength>", true
			}
		case nsDAV + " getlastmodified":
			mod := ""
			switch n.kind {
			case "resource":
				mod = n.row.UpdatedAt
			case "addressbook":
				mod = n.coll.ab.UpdatedAt
			}
			if t := store.ParseTime(mod); !t.IsZero() {
				return "<d:getlastmodified>" + t.UTC().Format(http.TimeFormat) + "</d:getlastmodified>", true
			}
		case nsCard + " address-data":
			if n.kind == "resource" {
				// a <card:prop name="…"/> subset is answered with the whole card
				return "<card:address-data>" + xesc(n.row.VCF) + "</card:address-data>", true
			}
		}
		return "", false
	}
	if all {
		for _, p := range defaultProps(n.kind) {
			if v, ok := get(p.Space, p.Local); ok {
				ps.add(v)
			}
		}
		return ps
	}
	for _, p := range want {
		if v, ok := get(p.Space, p.Local); ok {
			ps.add(v)
		} else {
			ps.notFound = append(ps.notFound, p)
		}
	}
	return ps
}

func defaultProps(kind string) []propName {
	common := []propName{{nsDAV, "resourcetype"}, {nsDAV, "displayname"}, {nsDAV, "current-user-principal"}, {nsDAV, "owner"}, {nsDAV, "supported-report-set"}, {nsDAV, "current-user-privilege-set"}}
	switch kind {
	case "principal":
		return append(common, propName{nsCard, "addressbook-home-set"}, propName{nsDAV, "principal-URL"})
	case "addressbook":
		return append(common, propName{nsCard, "addressbook-description"}, propName{nsCard, "supported-address-data"},
			propName{nsCS, "getctag"}, propName{nsDAV, "sync-token"})
	case "resource":
		return []propName{{nsDAV, "resourcetype"}, {nsDAV, "getetag"}, {nsDAV, "getcontenttype"}, {nsDAV, "getcontentlength"}, {nsDAV, "getlastmodified"}}
	}
	return common
}

// ---------- root, principal, home ----------

func (s *Server) davRoot(w http.ResponseWriter, r *http.Request, req *davReq) {
	if r.Method != "PROPFIND" {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintf(w, "Pocket Contacts CardDAV. Address book home: %s\n", s.homeHref(req))
			return
		}
		s.davMethodNotAllowed(w)
		return
	}
	body, err := readXML(r)
	if err != nil {
		http.Error(w, "bad xml", http.StatusBadRequest)
		return
	}
	want, all := requestedProps(body)
	ms := newMultistatus()
	self := r.URL.Path
	if !strings.HasSuffix(self, "/") {
		self += "/"
	}
	ms.response(self, s.props(&davNode{kind: "root", req: req}, want, all))
	if req.depth != "0" {
		switch {
		case len(req.parts) == 0:
			ms.response(req.prefix+"/principals/", s.props(&davNode{kind: "root", req: req}, want, all))
			ms.response(req.prefix+"/addressbooks/", s.props(&davNode{kind: "root", req: req}, want, all))
		case req.parts[0] == "principals":
			ms.response(s.principalHref(req), s.props(&davNode{kind: "principal", req: req}, want, all))
		case req.parts[0] == "addressbooks":
			ms.response(s.homeHref(req), s.props(&davNode{kind: "home", req: req}, want, all))
		}
	}
	ms.write(w, "")
}

func (s *Server) davPrincipal(w http.ResponseWriter, r *http.Request, req *davReq) {
	switch r.Method {
	case "PROPFIND", "REPORT":
		// a REPORT here is principal-property-search or expand-property:
		// answer with the one principal there is
		body, err := readXML(r)
		if err != nil {
			http.Error(w, "bad xml", http.StatusBadRequest)
			return
		}
		want, all := requestedProps(body)
		ms := newMultistatus()
		ms.response(s.principalHref(req), s.props(&davNode{kind: "principal", req: req}, want, all))
		ms.write(w, "")
	case "PROPPATCH":
		s.davTakeProps(w, r, s.principalHref(req))
	case http.MethodGet, http.MethodHead:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "Principal %s\n", principalName(req.user))
	default:
		s.davMethodNotAllowed(w)
	}
}

// davTakeProps answers a PROPPATCH of something whose properties are not
// kept (clients store their defaults on the home and the principal).
func (s *Server) davTakeProps(w http.ResponseWriter, r *http.Request, href string) {
	body, err := readXML(r)
	if err != nil || body == nil {
		http.Error(w, "bad xml", http.StatusBadRequest)
		return
	}
	var ok []string
	for _, part := range append(body.children("set"), body.children("remove")...) {
		for _, p := range part.propNodes() {
			ok = append(ok, propTag(p))
		}
	}
	writeProppatch(w, href, ok, nil)
}

func (s *Server) davHome(ctx context.Context, w http.ResponseWriter, r *http.Request, req *davReq) {
	switch r.Method {
	case "PROPFIND":
		body, err := readXML(r)
		if err != nil {
			http.Error(w, "bad xml", http.StatusBadRequest)
			return
		}
		want, all := requestedProps(body)
		cols, err := s.collections(ctx, req)
		if err != nil {
			s.davFail(w, err, "list address books")
			return
		}
		ms := newMultistatus()
		ms.response(s.homeHref(req), s.props(&davNode{kind: "home", req: req}, want, all))
		if req.depth != "0" {
			for _, c := range cols {
				n := s.collNode(req, c)
				ms.response(n.href, s.props(n, want, all))
			}
		}
		ms.write(w, "")
	case "PROPPATCH":
		s.davTakeProps(w, r, s.homeHref(req))
	case "REPORT":
		newMultistatus().write(w, "")
	case http.MethodGet, http.MethodHead:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "Address books of %s\n", req.user.Email)
	default:
		s.davMethodNotAllowed(w)
	}
}

// ---------- collections ----------

func (s *Server) davCollection(ctx context.Context, w http.ResponseWriter, r *http.Request, req *davReq, seg string) {
	c, err := s.resolveCollection(ctx, req, seg)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.davFail(w, err, "list address books")
			return
		}
		if r.Method == "MKCOL" {
			s.davMkcol(ctx, w, r, req, seg)
			return
		}
		s.davNotFound(w)
		return
	}
	n := s.collNode(req, c)
	switch r.Method {
	case "MKCOL":
		davError(w, http.StatusMethodNotAllowed, "<d:resource-must-be-null/>")
	case "PROPFIND":
		body, err := readXML(r)
		if err != nil {
			http.Error(w, "bad xml", http.StatusBadRequest)
			return
		}
		want, all := requestedProps(body)
		ms := newMultistatus()
		ms.response(n.href, s.props(n, want, all))
		if req.depth != "0" {
			rows, err := s.store.ListContacts(ctx, c.ab.ID)
			if err != nil {
				s.davFail(w, err, "list contacts")
				return
			}
			for _, row := range rows {
				cn := s.cardNode(req, c, row)
				ms.response(cn.href, s.props(cn, want, all))
			}
		}
		ms.write(w, "")
	case "PROPPATCH":
		s.davProppatch(ctx, w, r, c, n.href)
	case "REPORT":
		s.davReport(ctx, w, r, req, c)
	case http.MethodDelete:
		s.davDeleteCollection(ctx, w, req, c)
	case http.MethodGet, http.MethodHead:
		// the whole book as one .vcf
		rows, err := s.store.ListContacts(ctx, c.ab.ID)
		if err != nil {
			s.davFail(w, err, "list contacts")
			return
		}
		var b strings.Builder
		for _, row := range rows {
			b.WriteString(row.VCF)
			if !strings.HasSuffix(row.VCF, "\n") {
				b.WriteString("\r\n")
			}
		}
		w.Header().Set("Content-Type", "text/vcard; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(b.Len()))
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, b.String())
		}
	default:
		s.davMethodNotAllowed(w)
	}
}

// davMkcol creates an address book (extended MKCOL, RFC 5689).
func (s *Server) davMkcol(ctx context.Context, w http.ResponseWriter, r *http.Request, req *davReq, seg string) {
	if reservedSeg(seg) {
		http.Error(w, "that name is taken by the server", http.StatusForbidden)
		return
	}
	body, err := readXML(r)
	if err != nil {
		http.Error(w, "bad xml", http.StatusBadRequest)
		return
	}
	var prop *xnode
	if body != nil {
		prop = body.child("set").child("prop")
	}
	if prop.child("resourcetype").child("addressbook") == nil {
		// only address books live here
		davError(w, http.StatusForbidden, "<d:valid-resourcetype/>")
		return
	}
	ab := &store.AddressBook{UserID: req.user.ID, UID: seg, Name: seg, Color: "#e11d48"}
	if dn := prop.child("displayname"); dn != nil && strings.TrimSpace(dn.Text) != "" {
		ab.Name = strings.TrimSpace(dn.Text)
	}
	if d := prop.child("addressbook-description"); d != nil {
		ab.Description = strings.TrimSpace(d.Text)
	}
	if err := s.store.CreateAddressBook(ctx, ab); err != nil {
		if errors.Is(err, store.ErrConflict) {
			davError(w, http.StatusMethodNotAllowed, "<d:resource-must-be-null/>")
			return
		}
		s.davFail(w, err, "create address book")
		return
	}
	id := ab.ID
	s.store.AddActivity(ctx, req.user.ID, &id, nil, "addressbook_created", ab.Name, "carddav")
	s.touch()
	w.WriteHeader(http.StatusCreated)
}

// davDeleteCollection deletes one of the person's own books (never the
// last one); a shared book is the owner's to delete.
func (s *Server) davDeleteCollection(ctx context.Context, w http.ResponseWriter, req *davReq, c *davColl) {
	uid := req.user.ID
	if c.role != store.RoleOwner {
		davError(w, http.StatusForbidden, "<d:need-privileges/>")
		return
	}
	if own, _ := s.store.OwnAddressBooks(ctx, uid); len(own) <= 1 {
		http.Error(w, "you need at least one address book", http.StatusForbidden)
		return
	}
	if err := s.store.DeleteAddressBook(ctx, c.ab.ID); err != nil {
		s.davFail(w, err, "delete address book")
		return
	}
	s.store.AddActivity(ctx, uid, nil, nil, "addressbook_deleted", c.ab.Name, "carddav")
	s.touch()
	w.WriteHeader(http.StatusNoContent)
}

func writeProppatch(w http.ResponseWriter, href string, ok, failed []string) {
	ms := newMultistatus()
	ms.b.WriteString("<d:response><d:href>" + xesc(hrefEsc(href)) + "</d:href>")
	if len(ok) > 0 {
		ms.b.WriteString("<d:propstat><d:prop>" + strings.Join(ok, "") + "</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>")
	}
	if len(failed) > 0 {
		ms.b.WriteString("<d:propstat><d:prop>" + strings.Join(failed, "") + "</d:prop><d:status>HTTP/1.1 403 Forbidden</d:status></d:propstat>")
	}
	ms.b.WriteString("</d:response>\n")
	ms.write(w, "")
}

// davProppatch renames an address book or changes its description (manage
// role); the colour and order clients like to keep are taken and dropped.
func (s *Server) davProppatch(ctx context.Context, w http.ResponseWriter, r *http.Request, c *davColl, href string) {
	body, err := readXML(r)
	if err != nil || body == nil {
		http.Error(w, "bad xml", http.StatusBadRequest)
		return
	}
	var ok, failed []string
	changed := false
	manage := store.RoleAtLeast(c.role, store.RoleManage)
	for _, set := range body.children("set") {
		for _, p := range set.propNodes() {
			v := strings.TrimSpace(p.Text)
			accepted := false
			switch p.XMLName.Space + " " + p.XMLName.Local {
			case nsDAV + " displayname":
				switch {
				case v == "" || v == collDisplayName(c) || v == c.ab.Name:
					accepted = true
				case manage:
					c.ab.Name, changed, accepted = v, true, true
				}
			case nsCard + " addressbook-description":
				switch {
				case v == c.ab.Description:
					accepted = true
				case manage:
					c.ab.Description, changed, accepted = v, true, true
				}
			case nsApple + " calendar-color", nsApple + " calendar-order", nsOC + " enabled", "http://inf-it.com/ns/ab/ addressbook-color":
				accepted = true // taken, nothing kept
			}
			if accepted {
				ok = append(ok, propTag(p))
			} else {
				failed = append(failed, propTag(p))
			}
		}
	}
	for _, rm := range body.children("remove") {
		for _, p := range rm.propNodes() {
			failed = append(failed, propTag(p))
		}
	}
	if changed {
		if err := s.store.UpdateAddressBook(ctx, c.ab); err != nil {
			s.davFail(w, err, "update address book")
			return
		}
		s.touch()
	}
	writeProppatch(w, href, ok, failed)
}

// ---------- REPORT ----------

func (s *Server) davReport(ctx context.Context, w http.ResponseWriter, r *http.Request, req *davReq, c *davColl) {
	body, err := readXML(r)
	if err != nil || body == nil {
		http.Error(w, "bad xml", http.StatusBadRequest)
		return
	}
	want, all := requestedProps(body)
	switch body.XMLName.Local {
	case "addressbook-query":
		rows, err := s.store.ListContacts(ctx, c.ab.ID)
		if err != nil {
			s.davFail(w, err, "list contacts")
			return
		}
		filter := body.child("filter")
		limit := 0
		if nr := body.child("limit").child("nresults"); nr != nil {
			limit, _ = strconv.Atoi(strings.TrimSpace(nr.Text))
		}
		ms := newMultistatus()
		n := 0
		for _, row := range rows {
			if filter != nil && !matchFilter(filter, row.VCF) {
				continue
			}
			if limit > 0 && n >= limit {
				break
			}
			n++
			cn := s.cardNode(req, c, row)
			ms.response(cn.href, s.props(cn, want, all))
		}
		ms.write(w, "")
	case "addressbook-multiget":
		ms := newMultistatus()
		for _, h := range body.children("href") {
			raw := strings.TrimSpace(h.Text)
			p := raw
			if u, err := url.Parse(raw); err == nil && u.Path != "" {
				p = u.Path
			}
			var row *store.ContactRow
			if path.Base(path.Dir(p)) == c.seg {
				row, _ = s.store.GetContactByHref(ctx, c.ab.ID, path.Base(p))
			}
			if row == nil {
				ms.goneRaw(raw)
				continue
			}
			ms.responseRaw(raw, s.props(s.cardNode(req, c, row), want, all))
		}
		ms.write(w, "")
	case "sync-collection":
		s.davSync(ctx, w, req, c, strings.TrimSpace(body.child("sync-token").textOr("")), want, all)
	default:
		davError(w, http.StatusForbidden, "<d:supported-report/>")
	}
}

// davSync answers sync-collection (RFC 6578) from the change numbers
// contacts and tombstones carry. An unknown token, or one older than the
// deletes still remembered, answers 403 valid-sync-token: start over.
func (s *Server) davSync(ctx context.Context, w http.ResponseWriter, req *davReq, c *davColl, token string, want []propName, all bool) {
	since := int64(0)
	if token != "" {
		n, err := strconv.ParseInt(strings.TrimPrefix(token, syncPfx), 10, 64)
		if err != nil || !strings.HasPrefix(token, syncPfx) || n < 0 || n > s.store.Seq(ctx) || s.tokenTooOld(ctx, c.ab.ID, n) {
			davError(w, http.StatusForbidden, "<d:valid-sync-token/>")
			return
		}
		since = n
	}
	// the token handed out is read before the changes, so nothing slips between
	cur, err := s.store.GetAddressBook(ctx, c.ab.ID)
	if err != nil {
		s.davFail(w, err, "read address book")
		return
	}
	changes, err := s.store.ChangesSince(ctx, c.ab.ID, since)
	if err != nil {
		s.davFail(w, err, "sync")
		return
	}
	ms := newMultistatus()
	base := s.homeHref(req) + c.seg + "/"
	for _, ch := range changes {
		var row *store.ContactRow
		if !ch.Deleted {
			row, _ = s.store.GetContactByHref(ctx, c.ab.ID, ch.Href)
		}
		if row == nil {
			// a first sync lists what is there, not what went
			if token != "" {
				ms.gone(base + ch.Href)
			}
			continue
		}
		cn := s.cardNode(req, c, row)
		ms.response(cn.href, s.props(cn, want, all))
	}
	ms.write(w, "<d:sync-token>"+xesc(syncPfx+strconv.FormatInt(max(cur.Seq, since), 10))+"</d:sync-token>\n")
}

// tombstoneKeep is how many deletes per book housekeeping remembers
// (server.go prunes with the same number).
const tombstoneKeep = 5000

// tokenTooOld reports whether deletes after seq n may have been forgotten:
// only once housekeeping pruned the book's tombstones, and only for tokens
// from before the oldest one left.
func (s *Server) tokenTooOld(ctx context.Context, abID, n int64) bool {
	var count int
	_ = s.store.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM tombstones WHERE addressbook_id = ?`, abID).Scan(&count)
	if count < tombstoneKeep {
		return false
	}
	oldest := s.store.OldestTombstone(ctx, abID)
	return oldest > 0 && n < oldest-1
}

// matchFilter evaluates an addressbook-query filter (RFC 6352 10.5) against
// a vCard: prop-filters with is-not-defined, text-match (equals, contains,
// starts-with, ends-with; negate-condition) and param-filter, combined by
// test="anyof" (the default) or "allof".
func matchFilter(filter *xnode, vcf string) bool {
	pfs := filter.children("prop-filter")
	if len(pfs) == 0 {
		return true
	}
	card, err := vcard.ParseOne([]byte(vcf))
	if err != nil {
		return false
	}
	results := make([]bool, len(pfs))
	for i, pf := range pfs {
		results[i] = matchPropFilter(pf, card)
	}
	return combine(filter.attr("test"), results)
}

// combine applies test="anyof" (default) or "allof" to the results.
func combine(test string, results []bool) bool {
	allOf := strings.EqualFold(test, "allof")
	for _, ok := range results {
		if allOf && !ok {
			return false
		}
		if !allOf && ok {
			return true
		}
	}
	return allOf
}

func matchPropFilter(pf *xnode, card *vcard.Card) bool {
	props := card.All(strings.ToUpper(strings.TrimSpace(pf.attr("name"))))
	if pf.child("is-not-defined") != nil {
		return len(props) == 0
	}
	if len(props) == 0 {
		return false
	}
	tms, pms := pf.children("text-match"), pf.children("param-filter")
	if len(tms) == 0 && len(pms) == 0 {
		return true
	}
	var results []bool
	for _, tm := range tms {
		hit := false
		for _, p := range props {
			if textMatch(tm, p.Text()) {
				hit = true
				break
			}
		}
		results = append(results, hit)
	}
	for _, pm := range pms {
		hit := false
		for _, p := range props {
			if matchParamFilter(pm, p) {
				hit = true
				break
			}
		}
		results = append(results, hit)
	}
	return combine(pf.attr("test"), results)
}

func matchParamFilter(pm *xnode, p *vcard.Property) bool {
	name := strings.TrimSpace(pm.attr("name"))
	var values []string
	defined := false
	for _, pr := range p.Params {
		if strings.EqualFold(pr.Name, name) {
			defined = true
			values = append(values, pr.Values...)
		}
	}
	if pm.child("is-not-defined") != nil {
		return !defined
	}
	if !defined {
		return false
	}
	tm := pm.child("text-match")
	if tm == nil {
		return true
	}
	for _, v := range values {
		for _, x := range strings.Split(v, ",") {
			if textMatch(tm, x) {
				return true
			}
		}
	}
	return false
}

// textMatch applies one text-match (i;unicode-casemap unless i;octet).
func textMatch(tm *xnode, value string) bool {
	needle := strings.TrimSpace(tm.Text)
	if !strings.EqualFold(tm.attr("collation"), "i;octet") {
		needle, value = strings.ToLower(needle), strings.ToLower(value)
	}
	var hit bool
	switch strings.ToLower(tm.attr("match-type")) {
	case "equals":
		hit = value == needle
	case "starts-with":
		hit = strings.HasPrefix(value, needle)
	case "ends-with":
		hit = strings.HasSuffix(value, needle)
	default:
		hit = strings.Contains(value, needle)
	}
	return hit != strings.EqualFold(tm.attr("negate-condition"), "yes")
}

// ---------- resources ----------

func (s *Server) davResource(ctx context.Context, w http.ResponseWriter, r *http.Request, req *davReq, seg, name string) {
	c, err := s.resolveCollection(ctx, req, seg)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.davNotFound(w)
		} else {
			s.davFail(w, err, "list address books")
		}
		return
	}
	row, err := s.store.GetContactByHref(ctx, c.ab.ID, name)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.davFail(w, err, "read contact")
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if row == nil {
			s.davNotFound(w)
			return
		}
		w.Header().Set("ETag", row.ETag)
		w.Header().Set("Content-Type", "text/vcard; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(row.VCF)))
		if t := store.ParseTime(row.UpdatedAt); !t.IsZero() {
			w.Header().Set("Last-Modified", t.UTC().Format(http.TimeFormat))
		}
		if r.Method == http.MethodHead {
			return
		}
		_, _ = io.WriteString(w, row.VCF)
	case "PROPFIND":
		if row == nil {
			s.davNotFound(w)
			return
		}
		body, err := readXML(r)
		if err != nil {
			http.Error(w, "bad xml", http.StatusBadRequest)
			return
		}
		want, all := requestedProps(body)
		cn := s.cardNode(req, c, row)
		ms := newMultistatus()
		ms.response(cn.href, s.props(cn, want, all))
		ms.write(w, "")
	case http.MethodPut:
		s.davPut(ctx, w, r, req, c, name, row)
	case http.MethodDelete:
		s.davDelete(ctx, w, r, req, c, row)
	case "MOVE":
		s.davMove(ctx, w, r, req, c, row)
	default:
		s.davMethodNotAllowed(w)
	}
}

func (s *Server) davDelete(ctx context.Context, w http.ResponseWriter, r *http.Request, req *davReq, c *davColl, row *store.ContactRow) {
	if row == nil {
		s.davNotFound(w)
		return
	}
	if !c.writable() {
		davError(w, http.StatusForbidden, "<d:need-privileges/>")
		return
	}
	ifMatch := ""
	if im := r.Header.Get("If-Match"); im != "" && im != "*" {
		if !etagMatches(im, row.ETag) {
			http.Error(w, "the contact changed meanwhile", http.StatusPreconditionFailed)
			return
		}
		ifMatch = row.ETag
	}
	err := s.store.DeleteContact(ctx, row, store.DeleteContactOpts{Actor: req.user.ID, KeepCopy: true, IfMatch: ifMatch},
		s.davHook(ctx, "deleted", c.ab, row.ID, row.UID, row.FN, "carddav"))
	if errors.Is(err, store.ErrPrecondition) {
		http.Error(w, "the contact changed meanwhile", http.StatusPreconditionFailed)
		return
	}
	if err != nil {
		s.davFail(w, err, "delete contact")
		return
	}
	s.davLog(ctx, req.user, c.ab, nil, "deleted", row.FN)
	w.WriteHeader(http.StatusNoContent)
}

// davPut stores one vCard exactly as the client sent it.
func (s *Server) davPut(ctx context.Context, w http.ResponseWriter, r *http.Request, req *davReq, c *davColl, name string, row *store.ContactRow) {
	if !c.writable() {
		davError(w, http.StatusForbidden, "<d:need-privileges/>")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxVCFBytes+1))
	if err != nil {
		http.Error(w, "could not read the body", http.StatusBadRequest)
		return
	}
	if len(body) > maxVCFBytes {
		davError(w, http.StatusRequestEntityTooLarge, "<card:max-resource-size/>")
		return
	}
	card, err := vcard.ParseOne(body)
	if err != nil {
		davError(w, http.StatusBadRequest, "<card:valid-address-data/><d:responsedescription>"+xesc(err.Error())+"</d:responsedescription>")
		return
	}
	vcf := string(body)
	rewritten := false
	if strings.TrimSpace(card.UID()) == "" {
		// a card without a UID gets its resource name as one
		vcf, rewritten = withUID(vcf, strings.TrimSuffix(name, ".vcf")), true
		if card, err = vcard.ParseOne([]byte(vcf)); err != nil {
			davError(w, http.StatusBadRequest, "<card:valid-address-data/>")
			return
		}
	}
	ifMatch := ""
	if im := strings.TrimSpace(r.Header.Get("If-Match")); im != "" {
		if im == "*" {
			ifMatch = "*"
		} else {
			if row == nil || !etagMatches(im, row.ETag) {
				http.Error(w, "the contact changed meanwhile", http.StatusPreconditionFailed)
				return
			}
			ifMatch = row.ETag
		}
	}
	ifNoneMatch := strings.TrimSpace(r.Header.Get("If-None-Match")) == "*"
	if ifNoneMatch && row != nil {
		http.Error(w, "there is a contact here already", http.StatusPreconditionFailed)
		return
	}
	uid, fn := card.UID(), card.Contact().DisplayName()
	kind := "created"
	if row != nil {
		kind = "updated"
	}
	saved, created, err := s.store.SaveContact(ctx, store.ContactWrite{AddressBookID: c.ab.ID, Href: name, VCF: vcf, Source: "carddav",
		Actor: req.user.ID, IfMatch: ifMatch, IfNoneMatch: ifNoneMatch}, s.davHook(ctx, kind, c.ab, 0, uid, fn, "carddav"))
	switch {
	case errors.Is(err, store.ErrPrecondition):
		http.Error(w, "the contact changed meanwhile", http.StatusPreconditionFailed)
		return
	case errors.Is(err, store.ErrUIDTaken):
		cond := "<card:no-uid-conflict/>"
		if other, e := s.store.GetContactByUID(ctx, c.ab.ID, uid); e == nil {
			cond = "<card:no-uid-conflict>" + hrefXML(s.homeHref(req)+c.seg+"/"+other.Href) + "</card:no-uid-conflict>"
		}
		davError(w, http.StatusConflict, cond)
		return
	case errors.Is(err, store.ErrConflict):
		http.Error(w, "conflict", http.StatusConflict)
		return
	case err != nil:
		s.davFail(w, err, "store contact")
		return
	}
	s.davLog(ctx, req.user, c.ab, &saved.ID, kind, saved.FN)
	if !rewritten {
		// the stored text is what the client sent, so its copy is current
		w.Header().Set("ETag", saved.ETag)
	}
	if created {
		w.WriteHeader(http.StatusCreated)
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

// davMove moves a vCard to another address book (or another name) the
// person may write; Overwrite: F refuses to replace one.
func (s *Server) davMove(ctx context.Context, w http.ResponseWriter, r *http.Request, req *davReq, c *davColl, row *store.ContactRow) {
	if row == nil {
		s.davNotFound(w)
		return
	}
	dest, err := url.Parse(r.Header.Get("Destination"))
	if err != nil || dest.Path == "" {
		http.Error(w, "a Destination header is needed", http.StatusBadRequest)
		return
	}
	_, _, parts := davPath(dest.Path)
	if len(parts) != 4 || parts[0] != "addressbooks" || !davUserMatches(parts[1], req.user) {
		http.Error(w, "the destination is not one of your address books", http.StatusBadGateway)
		return
	}
	to, err := s.resolveCollection(ctx, req, parts[2])
	if err != nil {
		s.davNotFound(w)
		return
	}
	if !c.writable() || !to.writable() {
		davError(w, http.StatusForbidden, "<d:need-privileges/>")
		return
	}
	name := parts[3]
	if to.ab.ID == c.ab.ID && name == row.Href {
		http.Error(w, "source and destination are the same", http.StatusForbidden)
		return
	}
	existing, _ := s.store.GetContactByHref(ctx, to.ab.ID, name)
	if existing != nil && strings.EqualFold(r.Header.Get("Overwrite"), "F") {
		http.Error(w, "there is a contact there already", http.StatusPreconditionFailed)
		return
	}
	actor := req.user.ID
	save := func() (*store.ContactRow, error) {
		saved, _, err := s.store.SaveContact(ctx, store.ContactWrite{AddressBookID: to.ab.ID, Href: name, VCF: row.VCF,
			Source: firstNonEmpty(row.Source, "carddav"), Actor: actor}, s.davHook(ctx, "updated", to.ab, 0, row.UID, row.FN, "carddav"))
		return saved, err
	}
	var saved *store.ContactRow
	if to.ab.ID == c.ab.ID {
		// a rename: the UID must leave its old name first
		if err = s.store.DeleteContact(ctx, row, store.DeleteContactOpts{Actor: actor}); err == nil {
			if saved, err = save(); err != nil {
				_, _, _ = s.store.SaveContact(ctx, store.ContactWrite{AddressBookID: c.ab.ID, Href: row.Href, VCF: row.VCF, Source: row.Source, Actor: actor})
			}
		}
	} else if saved, err = save(); err == nil {
		err = s.store.DeleteContact(ctx, row, store.DeleteContactOpts{Actor: actor})
	}
	if errors.Is(err, store.ErrUIDTaken) {
		davError(w, http.StatusConflict, "<card:no-uid-conflict/>")
		return
	}
	if err != nil {
		s.davFail(w, err, "move contact")
		return
	}
	s.davLog(ctx, req.user, to.ab, &saved.ID, "moved", saved.FN)
	if existing != nil {
		w.WriteHeader(http.StatusNoContent)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
}

// withUID adds a UID line before the card's END:VCARD, keeping the rest of
// the bytes as they were.
func withUID(vcf, uid string) string {
	eol := "\n"
	if strings.Contains(vcf, "\r\n") {
		eol = "\r\n"
	}
	i := strings.LastIndex(strings.ToUpper(vcf), "END:VCARD")
	if i < 0 {
		return vcf
	}
	return vcf[:i] + "UID:" + vcard.Escape(uid) + eol + vcf[i:]
}

func etagMatches(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		part = strings.TrimPrefix(part, "W/")
		if part == "*" || part == etag || part == strings.Trim(etag, `"`) || `"`+part+`"` == etag {
			return true
		}
	}
	return false
}

// ---------- after a write ----------

// davLog records a change in the book owner's log and bumps the stamp the
// web app polls.
func (s *Server) davLog(ctx context.Context, actor *store.User, ab *store.AddressBook, contactID *int64, action, fn string) {
	s.touch()
	detail := "carddav"
	if actor != nil && actor.ID != ab.UserID {
		detail += ", by " + firstNonEmpty(actor.Name, actor.Email)
	}
	abID := ab.ID
	s.store.AddActivity(ctx, ab.UserID, &abID, contactID, action, fn, detail)
}

// davHook queues the Core event (contacts.created|updated|deleted) in the
// same transaction as the change. id 0 is looked up by UID inside it.
func (s *Server) davHook(ctx context.Context, kind string, ab *store.AddressBook, id int64, uid, fn, source string) store.Hook {
	if !s.core.Configured() || ab == nil {
		return nil
	}
	owner, err := s.store.GetUser(ctx, ab.UserID)
	return func(tx *sql.Tx) error {
		if err != nil {
			return err
		}
		cid := id
		if cid == 0 {
			_ = tx.QueryRowContext(ctx, `SELECT id FROM contacts WHERE addressbook_id = ? AND uid = ?`, ab.ID, uid).Scan(&cid)
		}
		return core.QueueEvent(ctx, tx, "contacts."+kind, "contact:"+uid, map[string]any{
			"id": cid, "uid": uid, "fn": fn, "addressbook_id": ab.ID, "source": source, "user_email": owner.Email,
		})
	}
}
