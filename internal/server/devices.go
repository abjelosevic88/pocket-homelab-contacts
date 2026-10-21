package server

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/store"
)

// Phones and other devices (docs/api.md, docs/carddav.md): where to point
// a CardDAV client, an app password per device (an API token named
// "Device: <name>"), and a one-time link to an Apple configuration profile
// that sets the account up on an iPhone or a Mac in one tap. The profile
// uses the legacy /remote.php/carddav/principals/<user>/ path so the
// account shows up under Settings → Contacts → Default Account.

const (
	devicePrefix   = "Device: "
	deviceCodeLife = 15 * time.Minute
)

// deviceCode is a pending profile download: single use, 15 minutes.
type deviceCode struct {
	srv     *Server
	userID  int64
	token   string
	name    string
	expires time.Time
}

var deviceCodes sync.Map // code → *deviceCode

// publicBase is the address people reach the app at: BASE_URL, else what
// the request came in on.
func (s *Server) publicBase(r *http.Request) string {
	if s.cfg.BaseURL != "" {
		return s.cfg.BaseURL
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" && s.cfg.TrustProxy {
		host = h
	}
	return scheme + "://" + host
}

// deviceRoutes are mounted with the account routes (authenticated /api/v1).
func (s *Server) deviceRoutes(r chi.Router) {
	r.Get("/devices", s.handleListDevices)
	r.Post("/devices", s.handleCreateDevice)
	r.Delete("/devices/{id}", s.handleDeleteDevice)
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	base := s.publicBase(r)
	name := principalName(u)
	toks, err := s.store.ListAPITokens(r.Context(), u.ID)
	if err != nil {
		s.fail(w, err, "list devices")
		return
	}
	devices := []store.APIToken{}
	for _, t := range toks {
		if strings.HasPrefix(t.Name, devicePrefix) {
			devices = append(devices, t)
		}
	}
	host := base
	if pu, err := url.Parse(base); err == nil && pu.Host != "" {
		host = pu.Host + strings.TrimRight(pu.Path, "/")
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"carddav_url":   base + "/dav/",
		"username":      name,
		"ios_server":    host + "/remote.php/carddav/principals/" + name + "/",
		"principal_url": base + "/dav/principals/" + name + "/",
		"tokens":        devices,
	})
}

func (s *Server) handleCreateDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var in struct {
		Name string `json:"name"`
	}
	_ = decode(r, &in)
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		in.Name = "Phone"
	}
	if len(in.Name) > 60 {
		in.Name = in.Name[:60]
	}
	plain, info, err := s.store.CreateAPIToken(ctx, u.ID, devicePrefix+in.Name)
	if err != nil {
		s.fail(w, err, "create device")
		return
	}
	code := store.RandomToken(16)
	deviceCodes.Store(code, &deviceCode{srv: s, userID: u.ID, token: plain, name: in.Name, expires: time.Now().Add(deviceCodeLife)})
	// forget codes nobody fetched
	deviceCodes.Range(func(k, v any) bool {
		if time.Now().After(v.(*deviceCode).expires) {
			deviceCodes.Delete(k)
		}
		return true
	})
	s.store.AddActivity(ctx, u.ID, nil, nil, "device_added", in.Name, "")
	writeJSON(w, http.StatusOK, map[string]any{
		"token":       plain,
		"profile_url": s.publicBase(r) + "/dav-profile/" + code + ".mobileconfig",
		"expires_in":  int(deviceCodeLife.Seconds()),
		"device":      info,
	})
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	id := idParam(r, "id")
	toks, err := s.store.ListAPITokens(ctx, u.ID)
	if err != nil {
		s.fail(w, err, "list devices")
		return
	}
	for _, t := range toks {
		if t.ID == id && strings.HasPrefix(t.Name, devicePrefix) {
			if err := s.store.DeleteAPIToken(ctx, u.ID, id); err != nil {
				s.fail(w, err, "delete device")
				return
			}
			s.store.AddActivity(ctx, u.ID, nil, nil, "device_removed", strings.TrimPrefix(t.Name, devicePrefix), "")
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeErr(w, http.StatusNotFound, "not found")
}

// handleDeviceProfile serves GET /dav-profile/<code>.mobileconfig: no
// sign-in, once, within 15 minutes of POST /devices.
func (s *Server) handleDeviceProfile(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSuffix(chi.URLParam(r, "file"), ".mobileconfig")
	v, ok := deviceCodes.LoadAndDelete(code)
	if !ok || v.(*deviceCode).srv != s || time.Now().After(v.(*deviceCode).expires) {
		http.Error(w, "this link was used already or has expired — make a new one in Pocket Contacts (Settings → Phones)", http.StatusNotFound)
		return
	}
	dc := v.(*deviceCode)
	u, err := s.store.GetUser(r.Context(), dc.userID)
	if err != nil {
		http.Error(w, "this link is no longer valid", http.StatusNotFound)
		return
	}
	base, _ := url.Parse(s.publicBase(r))
	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-contacts.mobileconfig"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(appleProfile(base, u, dc)))
}

// appleProfile is an unsigned configuration profile with one CardDAV account.
func appleProfile(base *url.URL, u *store.User, dc *deviceCode) string {
	ssl := base.Scheme == "https"
	port := 80
	if ssl {
		port = 443
	}
	if p, err := strconv.Atoi(base.Port()); err == nil {
		port = p
	}
	name := principalName(u)
	prefix := strings.TrimRight(base.Path, "/")
	ident := "app.pocket.contacts." + reverseHost(base.Hostname()) + "." + store.Slug(name)
	plistString := func(s string) string { return "<string>" + xesc(s) + "</string>" }
	boolean := func(b bool) string {
		if b {
			return "<true/>"
		}
		return "<false/>"
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>CardDAVAccountDescription</key>
			<string>Pocket Contacts</string>
			<key>CardDAVHostName</key>
			` + plistString(base.Hostname()) + `
			<key>CardDAVPort</key>
			<integer>` + strconv.Itoa(port) + `</integer>
			<key>CardDAVPrincipalURL</key>
			` + plistString(prefix+"/remote.php/carddav/principals/"+name+"/") + `
			<key>CardDAVUseSSL</key>
			` + boolean(ssl) + `
			<key>CardDAVUsername</key>
			` + plistString(name) + `
			<key>CardDAVPassword</key>
			` + plistString(dc.token) + `
			<key>PayloadDescription</key>
			<string>Your Pocket Contacts address book over CardDAV</string>
			<key>PayloadDisplayName</key>
			<string>Pocket Contacts (CardDAV)</string>
			<key>PayloadIdentifier</key>
			` + plistString(ident+".carddav") + `
			<key>PayloadType</key>
			<string>com.apple.carddav.account</string>
			<key>PayloadUUID</key>
			` + plistString(newUUID()) + `
			<key>PayloadVersion</key>
			<integer>1</integer>
		</dict>
	</array>
	<key>PayloadDescription</key>
	` + plistString("Adds the contacts of "+firstNonEmpty(u.Name, u.Email)+" from Pocket Contacts ("+dc.name+").") + `
	<key>PayloadDisplayName</key>
	<string>Pocket Contacts</string>
	<key>PayloadIdentifier</key>
	` + plistString(ident) + `
	<key>PayloadRemovalDisallowed</key>
	<false/>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadUUID</key>
	` + plistString(newUUID()) + `
	<key>PayloadVersion</key>
	<integer>1</integer>
</dict>
</plist>
`
}

// reverseHost turns contacts.example.com into com.example.contacts.
func reverseHost(h string) string {
	parts := strings.Split(strings.ToLower(h), ".")
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return store.Slug(strings.Join(parts, "."))
}

// newUUID is a random (version 4) UUID in upper case, as profiles carry them.
func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]))
}
