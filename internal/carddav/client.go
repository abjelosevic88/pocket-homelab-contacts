// Package carddav is a small CardDAV client: enough to find the address
// books of an account on any CardDAV server (Nextcloud, iCloud, Fastmail,
// Radicale, Baïkal…) and to read and write the vCards in them.
//
// Hrefs are kept in a canonical form: the decoded path when the resource
// lives on the client's own server (the usual case), or scheme://host plus
// the decoded path when it lives elsewhere (iCloud hands out an address
// book home on another host). Every method takes and returns hrefs in that
// form.
//
// vCards travel byte for byte: what a server sends is what the caller gets
// (the XML layer keeps CRLF line ends when the server escapes them, as
// Nextcloud and iCloud do), and what the caller puts is sent unchanged.
package carddav

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// Client talks to one server as one user.
type Client struct {
	Base string // https://cloud.example.com/remote.php/dav, https://contacts.icloud.com, …
	User string
	Pass string
	HTTP *http.Client
	UA   string
}

// ErrAuth is a 401.
var ErrAuth = errors.New("the server refused the sign-in")

// ErrInvalidToken says the sync token is no longer known; do a full listing.
var ErrInvalidToken = errors.New("sync token no longer valid")

// ErrPrecondition is a 412 on PUT/DELETE (someone else changed it).
var ErrPrecondition = errors.New("changed on the server meanwhile")

// ErrNotFound is a 404.
var ErrNotFound = errors.New("not found on the server")

// ErrNoAddressBooks says discovery found no address book home at the address.
var ErrNoAddressBooks = errors.New("no CardDAV address books found at this address")

// AddressBook is one collection.
type AddressBook struct {
	Href        string
	Name        string
	Description string
	CTag        string
	SyncToken   string
	ReadOnly    bool
}

// Change is one resource as a listing or a sync reports it.
type Change struct {
	Href    string
	ETag    string
	Deleted bool
}

// Resource is one fetched vCard.
type Resource struct {
	Href         string
	ETag         string
	Data         string
	LastModified time.Time
}

// New returns a client that signs in with Basic auth.
func New(base, user, pass string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), User: user, Pass: pass, HTTP: &http.Client{Timeout: 60 * time.Second}, UA: "PocketContacts"}
}

// ---------- hrefs ----------

func (c *Client) baseURL() *url.URL {
	u, err := url.Parse(c.Base)
	if err != nil || u.Host == "" {
		return &url.URL{Scheme: "https", Host: "invalid"}
	}
	return u
}

// BasePath is the decoded path of the base address ("" for none).
func (c *Client) BasePath() string {
	p := c.baseURL().Path
	if p == "/" {
		return ""
	}
	return p
}

func hostKey(scheme, host string) string {
	h := strings.ToLower(host)
	if (scheme == "https" && strings.HasSuffix(h, ":443")) || (scheme == "http" && strings.HasSuffix(h, ":80")) {
		h = h[:strings.LastIndex(h, ":")]
	}
	return strings.ToLower(scheme) + "://" + h
}

func (c *Client) origin() string {
	b := c.baseURL()
	return hostKey(b.Scheme, b.Host)
}

// splitAbs splits a canonical absolute href into origin and decoded path.
func splitAbs(href string) (scheme, host, p string, ok bool) {
	i := strings.Index(href, "://")
	if i <= 0 || (href[:i] != "http" && href[:i] != "https") {
		return "", "", "", false
	}
	rest := href[i+3:]
	j := strings.Index(rest, "/")
	if j < 0 {
		return href[:i], rest, "/", true
	}
	return href[:i], rest[:j], rest[j:], true
}

// URL turns a canonical href into the address to request.
func (c *Client) URL(href string) string {
	if scheme, host, p, ok := splitAbs(href); ok {
		return (&url.URL{Scheme: scheme, Host: host, Path: p}).String()
	}
	b := c.baseURL()
	u := url.URL{Scheme: b.Scheme, Host: b.Host}
	switch {
	case strings.HasPrefix(href, "/"):
		u.Path = href
	case href == "":
		u.Path = b.Path
	default:
		dir := b.Path
		if !strings.HasSuffix(dir, "/") {
			dir += "/"
		}
		u.Path = path.Join(dir, href)
		if strings.HasSuffix(href, "/") {
			u.Path += "/"
		}
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String()
}

// canon turns an address the server gave into the canonical href.
func (c *Client) canon(u *url.URL) string {
	p := u.Path
	if p == "" {
		p = "/"
	}
	if u.Host == "" || hostKey(u.Scheme, u.Host) == c.origin() {
		return p
	}
	return hostKey(u.Scheme, u.Host) + p
}

// resolveHref reads an href of a multistatus relative to the request address.
func (c *Client) resolveHref(reqURL *url.URL, href string) string {
	href = strings.TrimSpace(href)
	u, err := reqURL.Parse(href)
	if err != nil {
		if p, err := url.PathUnescape(href); err == nil {
			return p
		}
		return href
	}
	return c.canon(u)
}

// escapedPath is the path of a canonical href as it goes into a request body.
func escapedPath(href string) string {
	p := href
	if _, _, ap, ok := splitAbs(href); ok {
		p = ap
	}
	return (&url.URL{Path: p}).EscapedPath()
}

// SameHref compares two canonical hrefs, ignoring a trailing slash.
func SameHref(a, b string) bool {
	return strings.TrimSuffix(a, "/") == strings.TrimSuffix(b, "/")
}

// LastSegment is the last path segment of an href ("contacts" for …/contacts/).
func LastSegment(href string) string {
	h := strings.TrimSuffix(href, "/")
	return h[strings.LastIndex(h, "/")+1:]
}

// ---------- requests ----------

func (c *Client) do(ctx context.Context, method, href string, headers map[string]string, body string) (int, http.Header, []byte, *url.URL, error) {
	target := c.URL(href)
	hc := http.Client{Timeout: 60 * time.Second}
	if c.HTTP != nil {
		hc = *c.HTTP
	}
	// WebDAV methods must not turn into GETs on a redirect: follow them here.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for hop := 0; ; hop++ {
		req, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
		if err != nil {
			return 0, nil, nil, nil, err
		}
		if c.User != "" || c.Pass != "" {
			req.SetBasicAuth(c.User, c.Pass)
		}
		req.Header.Set("User-Agent", c.UA)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		res, err := hc.Do(req)
		if err != nil {
			return 0, nil, nil, nil, err
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 128<<20))
		res.Body.Close()
		switch res.StatusCode {
		case 301, 302, 303, 307, 308:
			loc := res.Header.Get("Location")
			if loc != "" && hop < 5 {
				next, err := req.URL.Parse(loc)
				// never follow to plain http from https (the sign-in goes along)
				if err == nil && (next.Scheme == "https" || req.URL.Scheme == "http") {
					target = next.String()
					continue
				}
			}
		case 401:
			return res.StatusCode, res.Header, b, req.URL, ErrAuth
		}
		return res.StatusCode, res.Header, b, req.URL, nil
	}
}

type xnode struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Text    string     `xml:",chardata"`
	Nodes   []xnode    `xml:",any"`
}

func (n *xnode) child(local string) *xnode {
	for i := range n.Nodes {
		if n.Nodes[i].XMLName.Local == local {
			return &n.Nodes[i]
		}
	}
	return nil
}

func (n *xnode) children(local string) []*xnode {
	var out []*xnode
	for i := range n.Nodes {
		if n.Nodes[i].XMLName.Local == local {
			out = append(out, &n.Nodes[i])
		}
	}
	return out
}

type response struct {
	href   string
	status string
	props  map[string]*xnode // local name → node, from 200 propstats
}

func (c *Client) parseMultistatus(reqURL *url.URL, b []byte) ([]response, *xnode, error) {
	var root xnode
	if err := xml.Unmarshal(b, &root); err != nil {
		return nil, nil, fmt.Errorf("unexpected answer: %w", err)
	}
	var out []response
	for _, r := range root.children("response") {
		resp := response{props: map[string]*xnode{}}
		if h := r.child("href"); h != nil {
			resp.href = c.resolveHref(reqURL, h.Text)
		}
		if st := r.child("status"); st != nil {
			resp.status = strings.TrimSpace(st.Text)
		}
		for _, ps := range r.children("propstat") {
			st := ps.child("status")
			if st == nil || !strings.Contains(st.Text, " 200") {
				continue
			}
			if p := ps.child("prop"); p != nil {
				for i := range p.Nodes {
					resp.props[p.Nodes[i].XMLName.Local] = &p.Nodes[i]
				}
			}
		}
		out = append(out, resp)
	}
	return out, &root, nil
}

func (c *Client) hrefProp(reqURL *url.URL, n *xnode) string {
	if n == nil {
		return ""
	}
	h := n.child("href")
	if h == nil || strings.TrimSpace(h.Text) == "" {
		return ""
	}
	return c.resolveHref(reqURL, h.Text)
}

func propText(r response, local string) string {
	if n := r.props[local]; n != nil {
		return strings.TrimSpace(n.Text)
	}
	return ""
}

func lastModified(r response) time.Time {
	if v := propText(r, "getlastmodified"); v != "" {
		if t, err := http.ParseTime(v); err == nil {
			return t
		}
	}
	return time.Time{}
}

// ---------- discovery ----------

// Home finds the address book home of the signed-in user (RFC 6764): the
// address itself, /.well-known/carddav and the server root are asked for
// the current-user-principal, then the principal for its addressbook-home-set.
func (c *Client) Home(ctx context.Context) (string, error) {
	starts := []string{}
	if bp := c.BasePath(); bp != "" {
		starts = append(starts, bp)
	}
	starts = append(starts, "/.well-known/carddav", "/")
	var lastErr error
	for _, start := range starts {
		home, principal, err := c.askPrincipal(ctx, start)
		if errors.Is(err, ErrAuth) {
			return "", err
		}
		if err != nil {
			lastErr = err
		}
		if home == "" && principal != "" {
			home, err = c.homeSet(ctx, principal)
			if errors.Is(err, ErrAuth) {
				return "", err
			}
			if err != nil {
				lastErr = err
			}
		}
		if home != "" {
			if !strings.HasSuffix(home, "/") {
				home += "/"
			}
			return home, nil
		}
	}
	// Nextcloud's layout when nothing answered
	if bp := c.BasePath(); strings.Contains(bp, "/remote.php/dav") && c.User != "" {
		root := bp[:strings.Index(bp, "/remote.php/dav")]
		return root + "/remote.php/dav/addressbooks/users/" + c.User + "/", nil
	}
	// the address may point straight at an address book home
	if bp := c.BasePath(); bp != "" {
		if books, err := c.AddressBooks(ctx, bp); err == nil && len(books) > 0 {
			if !strings.HasSuffix(bp, "/") {
				bp += "/"
			}
			return bp, nil
		}
	}
	if lastErr != nil {
		return "", fmt.Errorf("%w (%v)", ErrNoAddressBooks, lastErr)
	}
	return "", ErrNoAddressBooks
}

func (c *Client) askPrincipal(ctx context.Context, href string) (home, principal string, err error) {
	body := `<?xml version="1.0"?><d:propfind xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav"><d:prop><d:current-user-principal/><card:addressbook-home-set/></d:prop></d:propfind>`
	status, _, b, reqURL, err := c.do(ctx, "PROPFIND", href, map[string]string{"Depth": "0", "Content-Type": "application/xml; charset=utf-8"}, body)
	if err != nil {
		return "", "", err
	}
	if status != 207 {
		return "", "", fmt.Errorf("PROPFIND %s: HTTP %d", href, status)
	}
	rs, _, err := c.parseMultistatus(reqURL, b)
	if err != nil {
		return "", "", err
	}
	for _, r := range rs {
		if h := c.hrefProp(reqURL, r.props["addressbook-home-set"]); h != "" {
			home = h
		}
		if p := c.hrefProp(reqURL, r.props["current-user-principal"]); p != "" {
			principal = p
		}
	}
	return home, principal, nil
}

func (c *Client) homeSet(ctx context.Context, principal string) (string, error) {
	body := `<?xml version="1.0"?><d:propfind xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav"><d:prop><card:addressbook-home-set/></d:prop></d:propfind>`
	status, _, b, reqURL, err := c.do(ctx, "PROPFIND", principal, map[string]string{"Depth": "0", "Content-Type": "application/xml; charset=utf-8"}, body)
	if err != nil {
		return "", err
	}
	if status != 207 {
		return "", fmt.Errorf("PROPFIND %s: HTTP %d", principal, status)
	}
	rs, _, err := c.parseMultistatus(reqURL, b)
	if err != nil {
		return "", err
	}
	for _, r := range rs {
		if h := c.hrefProp(reqURL, r.props["addressbook-home-set"]); h != "" {
			return h, nil
		}
	}
	return "", nil
}

const bookProps = `<d:prop><d:displayname/><d:resourcetype/><d:sync-token/><d:current-user-privilege-set/><card:addressbook-description/><cs:getctag/></d:prop>`

const bookNS = `xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav" xmlns:cs="http://calendarserver.org/ns/"`

// AddressBooks lists the address book collections of the home.
func (c *Client) AddressBooks(ctx context.Context, home string) ([]AddressBook, error) {
	rs, err := c.propfindBooks(ctx, home, "1")
	if err != nil {
		return nil, err
	}
	var out []AddressBook
	for _, r := range rs {
		if ab, ok := readBook(r); ok {
			out = append(out, ab)
		}
	}
	return out, nil
}

// Collection reads one address book's properties (name, ctag, sync token).
func (c *Client) Collection(ctx context.Context, href string) (*AddressBook, error) {
	rs, err := c.propfindBooks(ctx, href, "0")
	if err != nil {
		return nil, err
	}
	for _, r := range rs {
		if ab, ok := readBook(r); ok {
			ab.Href = href
			return &ab, nil
		}
	}
	return nil, ErrNotFound
}

func (c *Client) propfindBooks(ctx context.Context, href, depth string) ([]response, error) {
	body := `<?xml version="1.0"?><d:propfind ` + bookNS + `>` + bookProps + `</d:propfind>`
	status, _, b, reqURL, err := c.do(ctx, "PROPFIND", href, map[string]string{"Depth": depth, "Content-Type": "application/xml; charset=utf-8"}, body)
	if err != nil {
		return nil, err
	}
	if status == 404 {
		return nil, ErrNotFound
	}
	if status != 207 {
		return nil, fmt.Errorf("listing address books: HTTP %d", status)
	}
	rs, _, err := c.parseMultistatus(reqURL, b)
	return rs, err
}

func readBook(r response) (AddressBook, bool) {
	rt := r.props["resourcetype"]
	if rt == nil || rt.child("addressbook") == nil {
		return AddressBook{}, false
	}
	ab := AddressBook{Href: r.href, Name: propText(r, "displayname"), Description: propText(r, "addressbook-description"),
		CTag: propText(r, "getctag"), SyncToken: propText(r, "sync-token")}
	if !strings.HasSuffix(ab.Href, "/") {
		ab.Href += "/"
	}
	if priv := r.props["current-user-privilege-set"]; priv != nil {
		writable := false
		for _, p := range priv.children("privilege") {
			if p.child("write") != nil || p.child("write-content") != nil || p.child("all") != nil || p.child("bind") != nil {
				writable = true
			}
		}
		ab.ReadOnly = !writable
	}
	return ab, true
}

// ---------- listing and sync ----------

// ListETags lists every vCard of a collection with its etag.
func (c *Client) ListETags(ctx context.Context, href string) ([]Change, error) {
	body := `<?xml version="1.0"?><d:propfind xmlns:d="DAV:"><d:prop><d:getetag/><d:resourcetype/></d:prop></d:propfind>`
	status, _, b, reqURL, err := c.do(ctx, "PROPFIND", href, map[string]string{"Depth": "1", "Content-Type": "application/xml; charset=utf-8"}, body)
	if err != nil {
		return nil, err
	}
	if status == 404 {
		return nil, ErrNotFound
	}
	if status != 207 {
		return nil, fmt.Errorf("listing %s: HTTP %d", href, status)
	}
	rs, _, err := c.parseMultistatus(reqURL, b)
	if err != nil {
		return nil, err
	}
	var out []Change
	for _, r := range rs {
		if SameHref(r.href, href) || strings.HasSuffix(r.href, "/") {
			continue
		}
		if rt := r.props["resourcetype"]; rt != nil && rt.child("collection") != nil {
			continue
		}
		if strings.Contains(r.status, " 404") {
			continue
		}
		out = append(out, Change{Href: r.href, ETag: propText(r, "getetag")})
	}
	return out, nil
}

// Sync asks what changed since token (RFC 6578). An empty token lists everything.
func (c *Client) Sync(ctx context.Context, href, token string) ([]Change, string, error) {
	body := `<?xml version="1.0"?><d:sync-collection xmlns:d="DAV:"><d:sync-token>` + xmlEsc(token) + `</d:sync-token><d:sync-level>1</d:sync-level><d:prop><d:getetag/></d:prop></d:sync-collection>`
	status, _, b, reqURL, err := c.do(ctx, "REPORT", href, map[string]string{"Depth": "0", "Content-Type": "application/xml; charset=utf-8"}, body)
	if err != nil {
		return nil, "", err
	}
	if status == 403 || status == 400 || status == 409 || status == 410 {
		return nil, "", ErrInvalidToken
	}
	if status != 207 {
		return nil, "", fmt.Errorf("sync of %s: HTTP %d", href, status)
	}
	rs, root, err := c.parseMultistatus(reqURL, b)
	if err != nil {
		return nil, "", err
	}
	var out []Change
	for _, r := range rs {
		if SameHref(r.href, href) || strings.HasSuffix(r.href, "/") {
			continue
		}
		if strings.Contains(r.status, " 404") {
			out = append(out, Change{Href: r.href, Deleted: true})
			continue
		}
		out = append(out, Change{Href: r.href, ETag: propText(r, "getetag")})
	}
	newToken := ""
	if st := root.child("sync-token"); st != nil {
		newToken = strings.TrimSpace(st.Text)
	}
	return out, newToken, nil
}

// MultigetBatch is how many vCards one addressbook-multiget asks for.
const MultigetBatch = 50

// Multiget fetches vCards by href, in batches of MultigetBatch.
func (c *Client) Multiget(ctx context.Context, collection string, hrefs []string) ([]Resource, error) {
	var out []Resource
	for i := 0; i < len(hrefs); i += MultigetBatch {
		end := min(i+MultigetBatch, len(hrefs))
		var hb bytes.Buffer
		for _, h := range hrefs[i:end] {
			hb.WriteString("<d:href>" + xmlEsc(escapedPath(h)) + "</d:href>")
		}
		body := `<?xml version="1.0"?><card:addressbook-multiget xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav"><d:prop><d:getetag/><d:getlastmodified/><card:address-data/></d:prop>` + hb.String() + `</card:addressbook-multiget>`
		status, _, b, reqURL, err := c.do(ctx, "REPORT", collection, map[string]string{"Depth": "1", "Content-Type": "application/xml; charset=utf-8"}, body)
		if err != nil {
			return nil, err
		}
		if status != 207 {
			return nil, fmt.Errorf("fetching contacts: HTTP %d", status)
		}
		rs, _, err := c.parseMultistatus(reqURL, b)
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			data := r.props["address-data"]
			if data == nil {
				continue
			}
			out = append(out, Resource{Href: r.href, ETag: propText(r, "getetag"), Data: data.Text, LastModified: lastModified(r)})
		}
	}
	return out, nil
}

// Get fetches one vCard.
func (c *Client) Get(ctx context.Context, href string) (*Resource, error) {
	status, h, b, _, err := c.do(ctx, http.MethodGet, href, nil, "")
	if err != nil {
		return nil, err
	}
	if status == 404 || status == 410 {
		return nil, ErrNotFound
	}
	if status != 200 {
		return nil, fmt.Errorf("GET %s: HTTP %d", href, status)
	}
	res := &Resource{Href: href, ETag: h.Get("ETag"), Data: string(b)}
	if t, err := http.ParseTime(h.Get("Last-Modified")); err == nil {
		res.LastModified = t
	}
	return res, nil
}

// ETag asks the server for one resource's current etag.
func (c *Client) ETag(ctx context.Context, href string) (string, error) {
	body := `<?xml version="1.0"?><d:propfind xmlns:d="DAV:"><d:prop><d:getetag/></d:prop></d:propfind>`
	status, _, b, reqURL, err := c.do(ctx, "PROPFIND", href, map[string]string{"Depth": "0", "Content-Type": "application/xml; charset=utf-8"}, body)
	if err != nil {
		return "", err
	}
	if status == 404 {
		return "", ErrNotFound
	}
	if status != 207 {
		return "", fmt.Errorf("PROPFIND %s: HTTP %d", href, status)
	}
	rs, _, err := c.parseMultistatus(reqURL, b)
	if err != nil {
		return "", err
	}
	for _, r := range rs {
		if et := propText(r, "getetag"); et != "" {
			return et, nil
		}
	}
	return "", nil
}

// Put writes a vCard; ifMatch guards an update, create=true a new one
// (If-None-Match: *). It returns the new etag (asked for when the server
// did not say, which it does when it changed what it stored).
func (c *Client) Put(ctx context.Context, href, vcf, ifMatch string, create bool) (string, error) {
	hdr := map[string]string{"Content-Type": "text/vcard; charset=utf-8"}
	if create {
		hdr["If-None-Match"] = "*"
	} else if ifMatch != "" {
		hdr["If-Match"] = ifMatch
	}
	status, h, b, _, err := c.do(ctx, http.MethodPut, href, hdr, vcf)
	if err != nil {
		return "", err
	}
	switch {
	case status == 412:
		return "", ErrPrecondition
	case status == 404 || status == 410:
		return "", ErrNotFound
	case status < 200 || status >= 300:
		return "", fmt.Errorf("PUT %s: HTTP %d %s", href, status, strings.TrimSpace(truncate(string(b), 300)))
	}
	if et := h.Get("ETag"); et != "" && !strings.HasPrefix(et, "W/") {
		return et, nil
	}
	et, _ := c.ETag(ctx, href)
	return et, nil
}

// Delete removes a vCard (404 is fine).
func (c *Client) Delete(ctx context.Context, href, ifMatch string) error {
	hdr := map[string]string{}
	if ifMatch != "" {
		hdr["If-Match"] = ifMatch
	}
	status, _, _, _, err := c.do(ctx, http.MethodDelete, href, hdr, "")
	if err != nil {
		return err
	}
	switch {
	case status == 412:
		return ErrPrecondition
	case status == 404 || status == 410:
		return ErrNotFound
	case status != 204 && status != 200:
		return fmt.Errorf("DELETE %s: HTTP %d", href, status)
	}
	return nil
}

func xmlEsc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
