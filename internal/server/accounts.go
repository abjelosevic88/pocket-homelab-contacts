package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/carddav"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/store"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// External address book accounts (docs/api.md "External accounts"): a
// Nextcloud (through Pocket Core with the person's own credential, or an
// address and app password), iCloud (Apple ID and an app-specific
// password) or any CardDAV server. Each chosen remote address book is
// mirrored as a local one (kind = the account's kind). The sync engine is
// in accounts_sync.go. Accounts are strictly per person: other people's
// ids answer 404.
//
// Modes:
//
//	import     pull everything once, keep the mapping, then switch to off
//	two_way    pull and push; local edits are sent with If-Match
//	read_only  pull only; the mirrored books are read-only here (the web app
//	           and CardDAV refuse edits), so nothing is ever lost on the
//	           next pull
//	off        nothing syncs; the books are ordinary books (an imported
//	           account ends here)
//
// Only two_way keeps dirty flags and pending deletes (store.tracksChanges).

func (s *Server) accountRoutes(r chi.Router) {
	r.Get("/accounts", s.handleListAccounts)
	r.Post("/accounts", s.handleCreateAccount)
	r.Get("/accounts/nextcloud", s.handleNextcloudStatus)
	r.Post("/accounts/discover", s.handleDiscoverAccount)
	r.Get("/accounts/{id}", s.handleGetAccount)
	r.Put("/accounts/{id}", s.handleUpdateAccount)
	r.Delete("/accounts/{id}", s.handleDeleteAccount)
	r.Post("/accounts/{id}/sync", s.handleSyncAccount)
	// Phones and other devices: devices.go.
	s.deviceRoutes(r)
}

var accountKinds = map[string]bool{"nextcloud": true, "carddav": true, "icloud": true}

var accountModes = map[string]bool{"import": true, "two_way": true, "read_only": true, "off": true}

// icloudURL is where iCloud's CardDAV lives (a test points it at a fake).
var icloudURL = "https://contacts.icloud.com"

// Nextcloud's own address books nobody wants mirrored: everyone on the
// Nextcloud ("system") and the people one mailed recently.
var skippedBooks = map[string]bool{"z-server-generated--system": true, "z-app-generated--contactsinteraction--recent": true}

func isSkippedBook(href string) bool { return skippedBooks[carddav.LastSegment(href)] }

// syncPalette colours the books an account brings.
var syncPalette = []string{"#0ea5e9", "#10b981", "#f59e0b", "#8b5cf6", "#ef4444", "#14b8a6", "#ec4899", "#64748b"}

type accountIn struct {
	Kind        *string  `json:"kind"`
	Via         *string  `json:"via"`
	Name        *string  `json:"name"`
	URL         *string  `json:"url"`
	Username    *string  `json:"username"`
	Password    *string  `json:"password"`
	Mode        *string  `json:"mode"`
	Hrefs       []string `json:"hrefs"`
	SyncMinutes *int     `json:"sync_minutes"`
	Enabled     *bool    `json:"enabled"`
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

// accountView is the JSON of an account (never its secrets).
func (s *Server) accountView(ctx context.Context, a *store.Account) map[string]any {
	books, _ := s.store.AddressBooksOfAccount(ctx, a.ID)
	list := []map[string]any{}
	for _, b := range books {
		var n int
		_ = s.store.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM contacts WHERE addressbook_id = ? AND kind <> 'group'`, b.ID).Scan(&n)
		list = append(list, map[string]any{"id": b.ID, "name": b.Name, "color": b.Color, "remote_href": b.RemoteHref, "read_only": b.ReadOnly, "contacts": n})
	}
	return map[string]any{
		"id": a.ID, "kind": a.Kind, "name": a.Name, "url": a.URL, "username": a.Username, "via": a.Via, "mode": a.Mode,
		"enabled": a.Enabled, "sync_minutes": a.SyncMinutes, "last_sync_at": a.LastSyncAt, "last_error": a.LastError,
		"has_secret": a.HasSecret, "addressbooks": len(books), "addressbook_list": list, "syncing": s.isSyncing(a.ID),
		"created_at": a.CreatedAt, "updated_at": a.UpdatedAt,
	}
}

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accs, err := s.store.ListAccounts(ctx, me(r))
	if err != nil {
		s.fail(w, err, "list accounts")
		return
	}
	out := []map[string]any{}
	for _, a := range accs {
		out = append(out, s.accountView(ctx, a))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetAccount(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetAccount(r.Context(), me(r), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "account")
		return
	}
	writeJSON(w, http.StatusOK, s.accountView(r.Context(), a))
}

// normalizeAccountURL checks an address: http(s) only, a missing scheme
// means https, no credentials inside.
func normalizeAccountURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", badRequest("this does not look like an address (https://…)")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", badRequest("only http:// and https:// addresses work")
	}
	if u.User != nil {
		return "", badRequest("put the user name and password in their own fields, not in the address")
	}
	u.Fragment, u.RawQuery = "", ""
	return u.String(), nil
}

// newAccount builds an account from a request body (not saved).
func (s *Server) newAccount(uid int64, in *accountIn) (*store.Account, error) {
	kind := strings.ToLower(str(in.Kind))
	if !accountKinds[kind] {
		return nil, badRequest("kind is nextcloud, icloud or carddav")
	}
	a := &store.Account{UserID: uid, Kind: kind, Via: "direct", Mode: "import", Enabled: true, SyncMinutes: 15}
	if kind == "nextcloud" && str(in.Via) != "" {
		a.Via = strings.ToLower(str(in.Via))
		if a.Via != "direct" && a.Via != "core" {
			return nil, badRequest("via is core or direct")
		}
	}
	if m := strings.ToLower(str(in.Mode)); m != "" {
		if !accountModes[m] || m == "off" {
			return nil, badRequest("mode is import, two_way or read_only")
		}
		a.Mode = m
	}
	a.Name = str(in.Name)
	if in.SyncMinutes != nil {
		a.SyncMinutes = min(max(*in.SyncMinutes, 5), 1440)
	}
	switch {
	case kind == "icloud":
		a.URL = icloudURL
	case kind == "nextcloud" && a.Via == "core":
		if !s.core.Configured() {
			return nil, badRequest("Pocket Core is not configured on this box — use the Nextcloud address and an app password instead")
		}
		return a, nil
	default:
		u, err := normalizeAccountURL(str(in.URL))
		if err != nil {
			return nil, err
		}
		if u == "" {
			if kind == "nextcloud" {
				return nil, badRequest("the Nextcloud address is needed (e.g. https://cloud.example.com)")
			}
			return nil, badRequest("the server address is needed (e.g. https://carddav.fastmail.com)")
		}
		a.URL = u
	}
	a.Username = str(in.Username)
	if a.Username == "" {
		if kind == "icloud" {
			return nil, badRequest("the Apple ID is needed")
		}
		return nil, badRequest("the user name is needed")
	}
	if in.Password == nil || *in.Password == "" {
		if kind == "icloud" {
			return nil, badRequest("an app-specific password is needed (appleid.apple.com → Sign-In and Security → App-Specific Passwords)")
		}
		return nil, badRequest("the password (an app password for Nextcloud) is needed")
	}
	sealed, err := s.seal(*in.Password)
	if err != nil {
		return nil, err
	}
	a.Secret, a.HasSecret = sealed, true
	return a, nil
}

// connectError is the 400 a failed connection answers.
func connectError(err error) error {
	var ue userError
	if errors.As(err, &ue) {
		return err
	}
	msg := friendlyAccountError(err)
	var dnsErr *net.DNSError
	var opErr *net.OpError
	if errors.As(err, &dnsErr) || errors.As(err, &opErr) {
		msg = "the server cannot be reached: " + err.Error()
	}
	return badRequest("could not connect: " + msg)
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return strings.TrimPrefix(u.Hostname(), "www.")
	}
	return raw
}

// defaultAccountName names an account after its provider.
func defaultAccountName(a *store.Account) string {
	host := hostOf(a.URL)
	switch {
	case a.Kind == "nextcloud":
		return "Nextcloud"
	case a.Kind == "icloud" || strings.Contains(host, "icloud.com"):
		return "iCloud"
	case strings.Contains(host, "fastmail"):
		return "Fastmail"
	case strings.Contains(host, "mailbox.org"):
		return "mailbox.org"
	case a.Username != "":
		return a.Username + " @ " + host
	}
	return host
}

// probe connects and lists every remote address book of an account.
func (s *Server) probe(ctx context.Context, a *store.Account) (*carddav.Client, []carddav.AddressBook, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cl, err := s.davClient(ctx, a)
	if err != nil {
		return nil, nil, err
	}
	home, err := cl.Home(ctx)
	if err != nil {
		return nil, nil, err
	}
	books, err := cl.AddressBooks(ctx, home)
	if err != nil {
		return nil, nil, err
	}
	return cl, books, nil
}

// handleDiscoverAccount connects without saving and lists the address books
// that would be mirrored (Nextcloud's system books left out).
func (s *Server) handleDiscoverAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in accountIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Mode == nil {
		m := "import"
		in.Mode = &m
	}
	a, err := s.newAccount(me(r), &in)
	if err != nil {
		s.fail(w, err, "account")
		return
	}
	cl, books, err := s.probe(ctx, a)
	if err != nil {
		s.fail(w, connectError(err), "account")
		return
	}
	out := []map[string]any{}
	for _, b := range books {
		if isSkippedBook(b.Href) {
			continue
		}
		n := 0
		if list, err := cl.ListETags(ctx, b.Href); err == nil {
			n = len(list)
		}
		out = append(out, map[string]any{"href": b.Href, "name": firstNonEmpty(b.Name, carddav.LastSegment(b.Href)), "count": n, "read_only": b.ReadOnly})
	}
	writeJSON(w, http.StatusOK, map[string]any{"addressbooks": out, "name": defaultAccountName(a)})
}

func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in accountIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	a, err := s.newAccount(me(r), &in)
	if err != nil {
		s.fail(w, err, "account")
		return
	}
	_, remote, err := s.probe(ctx, a)
	if err != nil {
		s.fail(w, connectError(err), "account")
		return
	}
	if accs, err := s.store.ListAccounts(ctx, a.UserID); err == nil {
		for _, o := range accs {
			if o.Kind == a.Kind && o.Via == a.Via && strings.TrimRight(o.URL, "/") == strings.TrimRight(a.URL, "/") && strings.EqualFold(o.Username, a.Username) {
				writeErr(w, http.StatusConflict, "this account is connected already ("+o.Name+")")
				return
			}
		}
	}
	var chosen []carddav.AddressBook
	if len(in.Hrefs) > 0 {
		for _, h := range in.Hrefs {
			found := false
			for _, b := range remote {
				if carddav.SameHref(b.Href, h) {
					chosen, found = append(chosen, b), true
					break
				}
			}
			if !found {
				writeErr(w, http.StatusBadRequest, h+" is not an address book of this account")
				return
			}
		}
	} else {
		for _, b := range remote {
			if !isSkippedBook(b.Href) {
				chosen = append(chosen, b)
			}
		}
	}
	if len(chosen) == 0 {
		writeErr(w, http.StatusBadRequest, "this account has no address book to bring over")
		return
	}
	if a.Name == "" {
		a.Name = defaultAccountName(a)
	}
	if err := s.store.SaveAccount(ctx, a); err != nil {
		s.fail(w, err, "save account")
		return
	}
	own, _ := s.store.OwnAddressBooks(ctx, a.UserID)
	for i, b := range chosen {
		accID := a.ID
		nb := &store.AddressBook{UserID: a.UserID, Name: firstNonEmpty(b.Name, carddav.LastSegment(b.Href), "Contacts"), Description: b.Description,
			Color: syncPalette[(len(own)+i)%len(syncPalette)], Kind: a.Kind, AccountID: &accID, RemoteHref: b.Href,
			ReadOnly: b.ReadOnly || a.Mode == "read_only"}
		if err := s.store.CreateAddressBook(ctx, nb); err != nil {
			s.fail(w, err, "create address book")
			return
		}
	}
	// the books appear at once; their contacts follow in the background
	s.kickSync(a.ID)
	if a.Kind == "nextcloud" {
		go s.reportModes(context.WithoutCancel(ctx))
	}
	s.store.AddActivity(ctx, a.UserID, nil, nil, "account_added", a.Name, a.Kind+", "+a.Mode)
	s.touch()
	if a, err = s.store.GetAccount(ctx, a.UserID, a.ID); err != nil {
		s.fail(w, err, "account")
		return
	}
	writeJSON(w, http.StatusCreated, s.accountView(ctx, a))
}

func (s *Server) handleUpdateAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a, err := s.store.GetAccount(ctx, me(r), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "account")
		return
	}
	var in accountIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	oldMode := a.Mode
	if in.Name != nil {
		a.Name = firstNonEmpty(str(in.Name), defaultAccountName(a))
	}
	if in.Password != nil && *in.Password != "" {
		if a.Via == "core" {
			writeErr(w, http.StatusBadRequest, "this account signs in through Pocket Core — change the Nextcloud app password there")
			return
		}
		sealed, err := s.seal(*in.Password)
		if err != nil {
			s.fail(w, err, "account")
			return
		}
		a.Secret, a.HasSecret = sealed, true
	}
	if m := strings.ToLower(str(in.Mode)); m != "" {
		if !accountModes[m] {
			writeErr(w, http.StatusBadRequest, "mode is import, two_way, read_only or off")
			return
		}
		a.Mode = m
	}
	if in.Enabled != nil {
		a.Enabled = *in.Enabled
	}
	if in.SyncMinutes != nil {
		a.SyncMinutes = min(max(*in.SyncMinutes, 5), 1440)
	}
	if err := s.store.SaveAccount(ctx, a); err != nil {
		s.fail(w, err, "save account")
		return
	}
	if a.Mode != oldMode {
		if err := s.modeChanged(ctx, a, oldMode); err != nil {
			s.fail(w, err, "account")
			return
		}
		s.store.AddActivity(ctx, a.UserID, nil, nil, "account_mode", a.Name, oldMode+" → "+a.Mode)
	}
	if a.Enabled && a.Mode != "off" && (a.Mode != oldMode || in.Enabled != nil || in.Password != nil) {
		s.kickSync(a.ID)
	}
	if a.Kind == "nextcloud" {
		go s.reportModes(context.WithoutCancel(ctx))
	}
	s.touch()
	if a, err = s.store.GetAccount(ctx, a.UserID, a.ID); err != nil {
		s.fail(w, err, "account")
		return
	}
	writeJSON(w, http.StatusOK, s.accountView(ctx, a))
}

// modeChanged brings the account's books in line with a new mode: what
// two-way still had to send is dropped when it stops; when it starts, local
// edits since the last sync and contacts made here are queued; read-only
// mirrors refuse edits here.
func (s *Server) modeChanged(ctx context.Context, a *store.Account, oldMode string) error {
	switch {
	case oldMode == "two_way" && a.Mode != "two_way":
		if err := s.store.ForgetAccountChanges(ctx, a.ID); err != nil {
			return err
		}
	case oldMode != "two_way" && a.Mode == "two_way":
		if err := s.store.TrackAccountChanges(ctx, a.ID, firstNonEmpty(a.LastSyncAt, store.Now())); err != nil {
			return err
		}
	}
	ro := a.Mode == "read_only"
	_, err := s.store.DB.ExecContext(ctx, `UPDATE addressbooks SET read_only = ? WHERE account_id = ?`, ro, a.ID)
	return err
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := me(r)
	a, err := s.store.GetAccount(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "account")
		return
	}
	keep := queryBool(r, "keep")
	if keep {
		// kept books become ordinary local ones, editable again
		_, _ = s.store.DB.ExecContext(ctx, `UPDATE addressbooks SET read_only = 0 WHERE account_id = ?`, a.ID)
	}
	if err := s.store.DeleteAccount(ctx, uid, a.ID, keep); err != nil {
		s.fail(w, err, "delete account")
		return
	}
	if a.Kind == "nextcloud" {
		go s.reportModes(context.WithoutCancel(ctx))
	}
	s.store.AddActivity(ctx, uid, nil, nil, "account_removed", a.Name, a.Kind)
	s.touch()
	w.WriteHeader(http.StatusNoContent)
}

// handleSyncAccount syncs now and answers the account once the sync is done
// (or after 60 s, with "syncing": true while it goes on).
func (s *Server) handleSyncAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a, err := s.store.GetAccount(ctx, me(r), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "account")
		return
	}
	if a.Mode == "off" || !a.Enabled {
		writeErr(w, http.StatusBadRequest, "this account is switched off — choose import, two-way or read-only to sync it")
		return
	}
	done := make(chan error, 1)
	go func() { done <- s.syncAccount(context.WithoutCancel(ctx), a.ID, false) }()
	deadline := time.After(60 * time.Second)
	wait := true
	for wait {
		select {
		case err := <-done:
			if errors.Is(err, errSyncBusy) {
				// one was running already: wait for that one
				go func() {
					for s.isSyncing(a.ID) {
						time.Sleep(200 * time.Millisecond)
					}
					done <- nil
				}()
				continue
			}
			wait = false
		case <-deadline:
			wait = false
		case <-ctx.Done():
			return
		}
	}
	if a, err = s.store.GetAccount(ctx, a.UserID, a.ID); err != nil {
		s.fail(w, err, "account")
		return
	}
	writeJSON(w, http.StatusOK, s.accountView(ctx, a))
}

// handleNextcloudStatus says whether Pocket Core holds a Nextcloud login
// for the person (address, user name and app password).
func (s *Server) handleNextcloudStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := map[string]any{"core": false, "detail": "", "url": "", "username": ""}
	if !s.core.Configured() {
		out["detail"] = "Pocket Core is not configured on this box"
		writeJSON(w, http.StatusOK, out)
		return
	}
	cred, err := s.core.ServiceForFresh(ctx, userFrom(ctx).Email, "nextcloud")
	if err != nil {
		var le *core.LinkError
		if errors.As(err, &le) {
			out["detail"] = le.Message
		} else {
			out["detail"] = "Pocket Core cannot be reached: " + err.Error()
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	user, pass := ncCredential(cred)
	out["url"], out["username"] = cred.URL, user
	if cred.URL == "" || user == "" || pass == "" {
		out["detail"] = "Pocket Core has your Nextcloud, but not its address, your user name and an app password"
	} else {
		out["core"] = true
	}
	writeJSON(w, http.StatusOK, out)
}

// ncCredential reads the user name and app password of a Nextcloud
// service credential from Core.
func ncCredential(cred *core.ServiceCred) (user, pass string) {
	return cred.Fields["USERNAME"], firstNonEmpty(cred.Fields["APP_PASSWORD"], cred.Fields["PASSWORD"], cred.Key)
}

// davClient builds the CardDAV client for an account.
func (s *Server) davClient(ctx context.Context, a *store.Account) (*carddav.Client, error) {
	var cl *carddav.Client
	switch {
	case a.Kind == "nextcloud" && a.Via == "core":
		if !s.core.Configured() {
			return nil, errors.New("Pocket Core is not configured on this box")
		}
		cred, err := s.core.ServiceFor(ctx, s.userEmail(ctx, a.UserID), "nextcloud")
		if err != nil {
			var le *core.LinkError
			if errors.As(err, &le) {
				return nil, fmt.Errorf("Nextcloud through Pocket Core: %s", le.Message)
			}
			return nil, fmt.Errorf("Nextcloud through Pocket Core: %w", err)
		}
		user, pass := ncCredential(cred)
		if cred.URL == "" || user == "" || pass == "" {
			return nil, errors.New("Pocket Core has no Nextcloud login for you (address, user name and app password)")
		}
		// shown on the account; the password stays in Core
		a.URL, a.Username = cred.URL, user
		cl = carddav.New(nextcloudDAV(cred.URL), user, pass)
	case a.Kind == "nextcloud":
		pass, err := s.open(a.Secret)
		if err != nil {
			return nil, err
		}
		cl = carddav.New(nextcloudDAV(a.URL), a.Username, pass)
	default:
		pass, err := s.open(a.Secret)
		if err != nil {
			return nil, err
		}
		cl = carddav.New(a.URL, a.Username, pass)
	}
	cl.HTTP, cl.UA = s.http, "PocketContacts/"+Version
	return cl, nil
}

// nextcloudDAV turns a Nextcloud address (as pasted from the browser) into
// its WebDAV root.
func nextcloudDAV(base string) string {
	base = strings.TrimRight(base, "/")
	for _, cut := range []string{"/remote.php/", "/index.php", "/apps/"} {
		if i := strings.Index(base, cut); i >= 0 {
			base = base[:i]
		}
	}
	return strings.TrimRight(base, "/") + "/remote.php/dav"
}
