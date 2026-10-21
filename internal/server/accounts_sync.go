package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/carddav"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/store"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/vcard"
)

// The sync engine of external accounts. A sync pulls each mirrored book
// (sync-collection with the stored token, else a listing of etags; then
// addressbook-multiget of what changed, 50 at a time) and, in two-way mode,
// pushes (local edits with If-Match, new contacts with If-None-Match: *,
// local deletes with If-Match). Pulled vCards are stored byte for byte.
//
// Conflicts: the remote version wins when the local copy has no unsent
// change. When both changed, the newer REV wins (else the newer change
// time: the local update vs the remote's last-modified); the losing
// version is kept in Recently deleted. A 412 on a push fetches the remote
// version and applies the same rule.

var (
	accountSyncTick = time.Minute      // how often due accounts are looked for
	accountPushTick = 20 * time.Second // how often local changes are pushed
	accountPushWait = 5 * time.Minute  // no push retry for an account before this after a failure
)

var errSyncBusy = errors.New("a sync of this account is running")

type accountRuntime struct {
	dueBusy  atomic.Bool
	pushBusy atomic.Bool
	pushFail sync.Map // account id → time.Time of the last failed push
}

var accountRuntimes sync.Map // *Server → *accountRuntime

func (s *Server) acctRuntime() *accountRuntime {
	v, _ := accountRuntimes.LoadOrStore(s, &accountRuntime{})
	return v.(*accountRuntime)
}

// syncLoop syncs every enabled account on its interval (checked every
// minute), right away when asked through s.syncNow, and pushes local
// changes of two-way accounts every ~20 s.
func (s *Server) syncLoop(ctx context.Context) {
	tick := time.NewTicker(accountSyncTick)
	defer tick.Stop()
	push := time.NewTicker(accountPushTick)
	defer push.Stop()
	first := time.NewTimer(15 * time.Second)
	defer first.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.syncNow:
			go func() { _ = s.syncAccount(ctx, id, false) }()
		case <-first.C:
			go s.syncDue(ctx)
		case <-tick.C:
			go s.syncDue(ctx)
		case <-push.C:
			go s.pushPending(ctx)
		}
	}
}

// kickSync asks the loop to sync an account soon.
func (s *Server) kickSync(id int64) {
	select {
	case s.syncNow <- id:
	default:
	}
}

func (s *Server) isSyncing(id int64) bool {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	return s.syncing[id]
}

func (s *Server) beginSync(id int64) bool {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	if s.syncing[id] {
		return false
	}
	s.syncing[id] = true
	return true
}

func (s *Server) endSync(id int64) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	delete(s.syncing, id)
}

func accountDue(a *store.Account, now time.Time) bool {
	if !a.Enabled || a.Mode == "off" {
		return false
	}
	last := store.ParseTime(a.LastSyncAt)
	every := time.Duration(a.SyncMinutes) * time.Minute
	if every <= 0 {
		every = 15 * time.Minute
	}
	return last.IsZero() || now.Sub(last) >= every-30*time.Second
}

// syncDue syncs the accounts whose interval is up, one after another.
func (s *Server) syncDue(ctx context.Context) {
	rt := s.acctRuntime()
	if !rt.dueBusy.CompareAndSwap(false, true) {
		return
	}
	defer rt.dueBusy.Store(false)
	accs, err := s.store.ListAccounts(ctx, 0)
	if err != nil {
		return
	}
	for _, a := range accs {
		if ctx.Err() != nil {
			return
		}
		if accountDue(a, time.Now()) {
			_ = s.syncAccount(ctx, a.ID, false)
		}
	}
}

// pushPending sends local changes of two-way books without waiting for the
// next full sync.
func (s *Server) pushPending(ctx context.Context) {
	rt := s.acctRuntime()
	if !rt.pushBusy.CompareAndSwap(false, true) {
		return
	}
	defer rt.pushBusy.Store(false)
	for _, id := range s.accountsWithLocalChanges(ctx) {
		if v, ok := rt.pushFail.Load(id); ok && time.Since(v.(time.Time)) < accountPushWait {
			continue
		}
		_ = s.syncAccount(ctx, id, true)
	}
}

func (s *Server) accountsWithLocalChanges(ctx context.Context) []int64 {
	rows, err := s.store.DB.QueryContext(ctx, `SELECT DISTINCT b.account_id FROM addressbooks b JOIN accounts a ON a.id = b.account_id
		WHERE a.enabled = 1 AND a.mode = 'two_way' AND b.read_only = 0 AND b.remote_href <> ''
		AND (EXISTS (SELECT 1 FROM contacts e WHERE e.addressbook_id = b.id AND e.dirty = 1)
		  OR EXISTS (SELECT 1 FROM remote_deletes d WHERE d.addressbook_id = b.id))`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// syncAccount runs one account's sync (pushOnly: just send local changes)
// and records the outcome on the account. An import ends by switching the
// account off, its books left as ordinary books.
func (s *Server) syncAccount(ctx context.Context, id int64, pushOnly bool) error {
	if !s.beginSync(id) {
		return errSyncBusy
	}
	defer s.endSync(id)
	a, err := s.store.GetAccount(ctx, 0, id)
	if err != nil {
		return err
	}
	if !a.Enabled || a.Mode == "off" || (pushOnly && a.Mode != "two_way") {
		return nil
	}
	sctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	start := time.Now()
	n, err := s.syncCardDAV(sctx, a, pushOnly)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	rt := s.acctRuntime()
	if pushOnly {
		if err != nil {
			rt.pushFail.Store(id, time.Now())
			_, _ = s.store.DB.ExecContext(ctx, `UPDATE accounts SET last_error = ? WHERE id = ?`, accountErrText(err), id)
		} else {
			rt.pushFail.Delete(id)
		}
	} else {
		rt.pushFail.Delete(id)
		s.store.SetAccountSync(ctx, id, accountErrText(err))
	}
	if err == nil && !pushOnly && a.Mode == "import" {
		_, _ = s.store.DB.ExecContext(ctx, `UPDATE accounts SET mode = 'off', updated_at = ? WHERE id = ?`, store.Now(), id)
		_, _ = s.store.DB.ExecContext(ctx, `UPDATE addressbooks SET read_only = 0 WHERE account_id = ?`, id)
		s.store.AddActivity(ctx, a.UserID, nil, nil, "imported", a.Name, fmt.Sprintf("%d contacts", n))
		if a.Kind == "nextcloud" {
			go s.reportModes(context.WithoutCancel(ctx))
		}
	} else if n > 0 && !pushOnly {
		s.store.AddActivity(ctx, a.UserID, nil, nil, "synced", a.Name, fmt.Sprintf("%d changes", n))
	}
	if n > 0 {
		s.touch()
	}
	if err != nil {
		s.log.Warn("account sync failed", "account", a.ID, "kind", a.Kind, "name", a.Name, "push_only", pushOnly, "err", err)
	} else if n > 0 || !pushOnly {
		s.log.Debug("account synced", "account", a.ID, "kind", a.Kind, "mode", a.Mode, "changes", n, "push_only", pushOnly, "took", time.Since(start).Round(time.Millisecond).String())
	}
	return err
}

func accountErrText(err error) string {
	if err == nil {
		return ""
	}
	msg := friendlyAccountError(err)
	if len(msg) > 600 {
		msg = msg[:600] + "…"
	}
	return msg
}

// friendlyAccountError turns client errors into something a person can act on.
func friendlyAccountError(err error) string {
	switch {
	case errors.Is(err, carddav.ErrAuth):
		return "the server refused the user name or password (Nextcloud and iCloud need an app password)"
	case errors.Is(err, carddav.ErrNoAddressBooks):
		return "no CardDAV address books were found at this address — check the server address"
	case errors.Is(err, context.DeadlineExceeded):
		return "the server took too long to answer"
	}
	return err.Error()
}

func (s *Server) setBookRemote(ctx context.Context, abID int64, ctag, token string) {
	_, _ = s.store.DB.ExecContext(ctx, `UPDATE addressbooks SET remote_ctag = ?, remote_token = ? WHERE id = ?`, ctag, token, abID)
}

// syncCardDAV syncs every book of an account; it returns how many contacts changed here or there.
func (s *Server) syncCardDAV(ctx context.Context, a *store.Account, pushOnly bool) (int, error) {
	cl, err := s.davClient(ctx, a)
	if err != nil {
		return 0, err
	}
	locals, err := s.store.AddressBooksOfAccount(ctx, a.ID)
	if err != nil {
		return 0, err
	}
	changed := 0
	var errs []string
	remote := map[string]carddav.AddressBook{}
	if !pushOnly {
		home, err := cl.Home(ctx)
		if err != nil {
			return 0, err
		}
		list, err := cl.AddressBooks(ctx, home)
		if err != nil {
			return 0, err
		}
		if len(list) == 0 && len(locals) > 0 {
			return 0, errors.New("the server listed no address books; keeping the ones here")
		}
		for _, rb := range list {
			remote[strings.TrimSuffix(rb.Href, "/")] = rb
		}
		for _, lb := range locals {
			rb, ok := remote[strings.TrimSuffix(lb.RemoteHref, "/")]
			if !ok {
				// gone over there: keep the contacts as an ordinary book
				_, _ = s.store.DB.ExecContext(ctx, `UPDATE addressbooks SET kind = 'local', account_id = NULL, read_only = 0, remote_href = '', remote_ctag = '', remote_token = '' WHERE id = ?`, lb.ID)
				_, _ = s.store.DB.ExecContext(ctx, `UPDATE contacts SET dirty = 0, remote_href = '', remote_etag = '' WHERE addressbook_id = ?`, lb.ID)
				s.store.AddActivity(ctx, a.UserID, nil, nil, "addressbook_detached", lb.Name, "deleted at "+a.Name+"; kept here as an ordinary address book")
				errs = append(errs, fmt.Sprintf("%q is gone from %s; it stays here as an ordinary address book", lb.Name, a.Name))
				lb.RemoteHref = ""
				continue
			}
			ro := rb.ReadOnly || a.Mode == "read_only"
			if ro != lb.ReadOnly {
				_, _ = s.store.DB.ExecContext(ctx, `UPDATE addressbooks SET read_only = ? WHERE id = ?`, ro, lb.ID)
				lb.ReadOnly = ro
			}
		}
	}
	for _, lb := range locals {
		if lb.RemoteHref == "" || ctx.Err() != nil {
			continue
		}
		if !pushOnly {
			n, err := s.pullBook(ctx, cl, lb, remote[strings.TrimSuffix(lb.RemoteHref, "/")])
			changed += n
			if err != nil {
				errs = append(errs, lb.Name+": "+friendlyAccountError(err))
				continue
			}
		}
		if a.Mode != "two_way" || lb.ReadOnly {
			continue
		}
		n, err := s.pushBook(ctx, cl, lb)
		changed += n
		if err != nil {
			errs = append(errs, lb.Name+": "+friendlyAccountError(err))
		}
	}
	if ctx.Err() != nil {
		return changed, ctx.Err()
	}
	if len(errs) > 0 {
		return changed, errors.New(strings.Join(errs, "; "))
	}
	return changed, nil
}

func (s *Server) pendingDeletes(ctx context.Context, abID int64) map[string]bool {
	out := map[string]bool{}
	if dels, err := s.store.RemoteDeletes(ctx, abID); err == nil {
		for _, d := range dels {
			out[d.RemoteHref] = true
		}
	}
	return out
}

// pullBook brings the remote changes of one address book here.
func (s *Server) pullBook(ctx context.Context, cl *carddav.Client, lb *store.AddressBook, rb carddav.AddressBook) (int, error) {
	href := lb.RemoteHref
	if rb.CTag == "" && rb.SyncToken == "" {
		if info, err := cl.Collection(ctx, href); err == nil {
			rb.CTag, rb.SyncToken = info.CTag, info.SyncToken
		}
	}
	if rb.CTag != "" && rb.CTag == lb.RemoteCTag {
		return 0, nil // nothing changed over there
	}
	pending := s.pendingDeletes(ctx, lb.ID)
	var changes []carddav.Change
	token := lb.RemoteToken
	full := token == ""
	noSync := false
	if !full {
		ch, tok, err := cl.Sync(ctx, href, token)
		switch {
		case errors.Is(err, carddav.ErrInvalidToken):
			full = true
		case errors.Is(err, carddav.ErrAuth), ctx.Err() != nil:
			return 0, err
		case err != nil:
			// the server does not do sync-collection after all: list from now on
			full, noSync = true, true
		default:
			changes, token = ch, tok
		}
	}
	var locals []*store.ContactRow
	if full {
		ch, err := cl.ListETags(ctx, href)
		if err != nil {
			return 0, err
		}
		if locals, err = s.store.ListContacts(ctx, lb.ID); err != nil {
			return 0, err
		}
		mirrored := 0
		for _, e := range locals {
			if e.RemoteHref != "" {
				mirrored++
			}
		}
		if len(ch) == 0 && mirrored > 0 {
			// an empty listing would delete everything here: a glitch more likely than intent
			return 0, fmt.Errorf("the server listed no contacts in %q; keeping the %d here", lb.Name, mirrored)
		}
		changes, token = ch, rb.SyncToken
		if noSync {
			token = ""
		}
	}
	n := 0
	listed := map[string]bool{}
	var fetch []string
	for _, ch := range changes {
		if pending[ch.Href] {
			continue
		}
		listed[ch.Href] = true
		local, lerr := s.store.GetContactByRemote(ctx, lb.ID, ch.Href)
		if ch.Deleted {
			if lerr == nil && s.remoteGone(ctx, local) {
				n++
			}
			continue
		}
		if lerr == nil && ch.ETag != "" && local.RemoteETag == ch.ETag {
			continue
		}
		fetch = append(fetch, ch.Href)
	}
	if full {
		// what the listing no longer has was deleted over there
		for _, e := range locals {
			if e.RemoteHref != "" && !listed[e.RemoteHref] && !pending[e.RemoteHref] && s.remoteGone(ctx, e) {
				n++
			}
		}
	}
	if len(fetch) > 0 {
		res, err := cl.Multiget(ctx, href, fetch)
		if err != nil {
			return n, err
		}
		if len(res) == 0 {
			// a server that does not multiget: one by one
			for _, h := range fetch {
				if r, err := cl.Get(ctx, h); err == nil {
					res = append(res, *r)
				}
			}
		}
		for _, r := range res {
			if s.applyRemote(ctx, lb, r, false) {
				n++
			}
		}
	}
	s.setBookRemote(ctx, lb.ID, rb.CTag, token)
	return n, nil
}

// remoteGone handles a contact deleted over there: it goes to Recently
// deleted here. A local edit not pushed yet keeps it (it goes back as new).
func (s *Server) remoteGone(ctx context.Context, e *store.ContactRow) bool {
	if e.Dirty {
		_ = s.store.MarkPushed(ctx, e.ID, -1, "", "")
		return false
	}
	if err := s.store.DeleteContact(ctx, e, store.DeleteContactOpts{FromRemote: true, KeepCopy: true}); err != nil {
		s.log.Warn("account sync: cannot delete contact", "contact", e.ID, "err", err)
		return false
	}
	return true
}

// cardRev is the REV of a vCard (zero when missing or unreadable).
func cardRev(vcf string) time.Time {
	c, err := vcard.ParseOne([]byte(vcf))
	if err != nil {
		return time.Time{}
	}
	v := strings.TrimSpace(c.Value("REV"))
	for _, layout := range []string{"20060102T150405Z", "2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05.999Z07:00", "20060102T150405", "2006-01-02T15:04:05", "20060102T150405Z0700", "20060102", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

// remoteWins applies the conflict rule to a contact changed on both sides:
// the newer REV when both have one, else the newer change time.
func remoteWins(local *store.ContactRow, r carddav.Resource) bool {
	lrev, rrev := cardRev(local.VCF), cardRev(r.Data)
	if !lrev.IsZero() && !rrev.IsZero() {
		return rrev.After(lrev)
	}
	lt := store.ParseTime(local.UpdatedAt)
	if lrev.After(lt) {
		lt = lrev
	}
	rt := r.LastModified
	if rrev.After(rt) {
		rt = rrev
	}
	return !rt.IsZero() && rt.After(lt)
}

// keepLoser puts the losing side of a conflict into Recently deleted.
func (s *Server) keepLoser(ctx context.Context, lb *store.AddressBook, uid, vcf string) {
	fn := ""
	if c, err := vcard.ParseOne([]byte(vcf)); err == nil {
		fn = c.Contact().DisplayName()
		uid = firstNonEmpty(c.UID(), uid)
	}
	if err := s.store.KeepDeletedCopy(ctx, lb.UserID, lb.ID, uid, fn, vcf); err != nil {
		s.log.Warn("account sync: cannot keep the other version", "uid", uid, "err", err)
	}
}

// applyRemote stores one fetched vCard as it came; it reports whether
// anything changed here. force skips the conflict rule (it was applied).
func (s *Server) applyRemote(ctx context.Context, lb *store.AddressBook, r carddav.Resource, force bool) bool {
	card, err := vcard.ParseOne([]byte(r.Data))
	if err != nil {
		s.log.Warn("account sync: unreadable vCard", "book", lb.Name, "href", r.Href, "err", err)
		return false
	}
	data := r.Data
	uid := strings.TrimSpace(card.UID())
	if uid == "" {
		// a card without a UID gets its resource name as one
		uid = strings.TrimSuffix(carddav.LastSegment(r.Href), ".vcf")
		data = withUID(data, uid)
	}
	local, err := s.store.GetContactByRemote(ctx, lb.ID, r.Href)
	if err != nil {
		local = nil
		if byUID, err := s.store.GetContactByUID(ctx, lb.ID, uid); err == nil {
			if byUID.RemoteHref != "" && byUID.RemoteHref != r.Href {
				s.log.Warn("account sync: two remote vCards share a UID", "book", lb.Name, "uid", uid, "href", r.Href)
				return false
			}
			local = byUID
		}
	}
	if local != nil && local.VCF == data {
		// the same card: only the remote etag moved
		_ = s.store.MarkPushed(ctx, local.ID, local.Seq, r.Href, r.ETag)
		return false
	}
	if local != nil && local.Dirty && !force {
		if !remoteWins(local, r) {
			return false // ours is newer; the push carries it (and keeps theirs)
		}
		s.log.Info("account sync: the remote edit is newer and wins", "contact", local.ID, "fn", local.FN)
		s.keepLoser(ctx, lb, local.UID, local.VCF)
	}
	if local != nil && local.UID != uid {
		// the resource over there now holds another contact
		_ = s.store.DeleteContact(ctx, local, store.DeleteContactOpts{FromRemote: true, KeepCopy: true})
		local = nil
	}
	href := ""
	if local != nil {
		href = local.Href
	}
	w := store.ContactWrite{AddressBookID: lb.ID, Href: href, VCF: data, Source: "remote", FromRemote: true, RemoteHref: r.Href, RemoteETag: r.ETag}
	_, _, err = s.store.SaveContact(ctx, w)
	if errors.Is(err, store.ErrConflict) && href == "" {
		w.Href = store.RandomToken(12) + ".vcf"
		_, _, err = s.store.SaveContact(ctx, w)
	}
	if err != nil {
		s.log.Warn("account sync: cannot store contact", "book", lb.Name, "href", r.Href, "err", err)
		return false
	}
	return true
}

// pushBook sends the local deletes and edits of one address book.
func (s *Server) pushBook(ctx context.Context, cl *carddav.Client, lb *store.AddressBook) (int, error) {
	n := 0
	var firstErr error
	dels, err := s.store.RemoteDeletes(ctx, lb.ID)
	if err != nil {
		return 0, err
	}
	for _, d := range dels {
		err := cl.Delete(ctx, d.RemoteHref, d.RemoteETag)
		switch {
		case err == nil, errors.Is(err, carddav.ErrNotFound):
			_ = s.store.DoneRemoteDelete(ctx, d.ID)
			n++
		case errors.Is(err, carddav.ErrPrecondition):
			// changed over there after we last saw it: that edit is newer
			// than the delete here, so the contact comes back
			_ = s.store.DoneRemoteDelete(ctx, d.ID)
			if res, gerr := cl.Get(ctx, d.RemoteHref); gerr == nil && s.applyRemote(ctx, lb, *res, true) {
				n++
			}
		default:
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	dirty, err := s.store.DirtyContacts(ctx, lb.ID)
	if err != nil {
		return n, err
	}
	for _, e := range dirty {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		if err := s.pushContact(ctx, cl, lb, e); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", firstNonEmpty(e.FN, e.UID), err)
			}
			continue
		}
		n++
	}
	return n, firstErr
}

// pushContact writes one local change over there.
func (s *Server) pushContact(ctx context.Context, cl *carddav.Client, lb *store.AddressBook, e *store.ContactRow) error {
	href := e.RemoteHref
	create := href == ""
	if create {
		href = strings.TrimSuffix(lb.RemoteHref, "/") + "/" + store.SafeHref(e.UID) + ".vcf"
	}
	etag, err := cl.Put(ctx, href, e.VCF, e.RemoteETag, create)
	if errors.Is(err, carddav.ErrNotFound) && !create {
		// gone over there meanwhile: put it back as new
		etag, err = cl.Put(ctx, href, e.VCF, "", true)
	}
	if errors.Is(err, carddav.ErrPrecondition) {
		res, gerr := cl.Get(ctx, href)
		if gerr != nil {
			return gerr
		}
		if c, perr := vcard.ParseOne([]byte(res.Data)); perr == nil && c.UID() != e.UID {
			// the name is taken by another contact over there
			href = strings.TrimSuffix(lb.RemoteHref, "/") + "/" + store.RandomToken(16) + ".vcf"
			if etag, err = cl.Put(ctx, href, e.VCF, "", true); err != nil {
				return err
			}
			return s.store.MarkPushed(ctx, e.ID, e.Seq, href, etag)
		}
		if res.Data == e.VCF {
			return s.store.MarkPushed(ctx, e.ID, e.Seq, href, res.ETag)
		}
		if remoteWins(e, *res) {
			// changed on both sides and theirs is newer: it wins, ours is kept aside
			s.keepLoser(ctx, lb, e.UID, e.VCF)
			s.applyRemote(ctx, lb, *res, true)
			return nil
		}
		s.keepLoser(ctx, lb, e.UID, res.Data)
		etag, err = cl.Put(ctx, href, e.VCF, res.ETag, false)
	}
	if err != nil {
		return err
	}
	return s.store.MarkPushed(ctx, e.ID, e.Seq, href, etag)
}
