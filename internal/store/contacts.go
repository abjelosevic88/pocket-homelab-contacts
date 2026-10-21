package store

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/vcard"
)

// ErrPrecondition is returned when If-Match / If-None-Match does not hold.
var ErrPrecondition = errors.New("precondition failed")

// ErrUIDTaken is returned when another resource of the address book has the UID.
var ErrUIDTaken = errors.New("another contact of this address book has that UID")

// Hook runs inside a write's transaction before it commits (Core events).
type Hook = func(*sql.Tx) error

const contactCols = `e.id, e.addressbook_id, e.uid, e.href, e.etag, e.vcf, e.kind, e.fn, e.sort_given, e.sort_family, e.given,
	e.family, e.nickname, e.org, e.title, e.emails, e.phones, e.bday, e.anniversary, e.categories, e.members, e.has_photo,
	e.photo_hash, e.source, e.created_by, e.updated_by, e.seq, e.remote_href, e.remote_etag, e.dirty, e.created_at, e.updated_at`

func scanContact(row interface{ Scan(...any) error }, extra ...any) (*ContactRow, error) {
	var e ContactRow
	var cats, members string
	dest := []any{&e.ID, &e.AddressBookID, &e.UID, &e.Href, &e.ETag, &e.VCF, &e.Kind, &e.FN, &e.SortGiven, &e.SortFamily,
		&e.Given, &e.Family, &e.Nickname, &e.Org, &e.Title, &e.Emails, &e.Phones, &e.Bday, &e.Anniversary, &cats, &members,
		&e.HasPhoto, &e.PhotoHash, &e.Source, &e.CreatedBy, &e.UpdatedBy, &e.Seq, &e.RemoteHref, &e.RemoteETag, &e.Dirty,
		&e.CreatedAt, &e.UpdatedAt}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return nil, notFound(err)
	}
	e.Categories = []string{}
	_ = jsonUnmarshal(cats, &e.Categories)
	_ = jsonUnmarshal(members, &e.Members)
	return &e, nil
}

func scanContacts(rows *sql.Rows, err error) ([]*ContactRow, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ContactRow{}
	for rows.Next() {
		e, err := scanContact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetContact returns a contact by id (no access check).
func (s *Store) GetContact(ctx context.Context, id int64) (*ContactRow, error) {
	return scanContact(s.DB.QueryRowContext(ctx, `SELECT `+contactCols+` FROM contacts e WHERE e.id = ?`, id))
}

// GetContactByHref returns a contact by its resource name.
func (s *Store) GetContactByHref(ctx context.Context, abID int64, href string) (*ContactRow, error) {
	return scanContact(s.DB.QueryRowContext(ctx, `SELECT `+contactCols+` FROM contacts e WHERE e.addressbook_id = ? AND e.href = ?`, abID, href))
}

// GetContactByUID returns a contact by UID within one address book.
func (s *Store) GetContactByUID(ctx context.Context, abID int64, uid string) (*ContactRow, error) {
	return scanContact(s.DB.QueryRowContext(ctx, `SELECT `+contactCols+` FROM contacts e WHERE e.addressbook_id = ? AND e.uid = ?`, abID, uid))
}

// GetContactByRemote returns the contact mirrored from a remote resource.
func (s *Store) GetContactByRemote(ctx context.Context, abID int64, remoteHref string) (*ContactRow, error) {
	return scanContact(s.DB.QueryRowContext(ctx, `SELECT `+contactCols+` FROM contacts e WHERE e.addressbook_id = ? AND e.remote_href = ?`, abID, remoteHref))
}

// ListContacts returns every resource of one address book (group cards included).
func (s *Store) ListContacts(ctx context.Context, abID int64) ([]*ContactRow, error) {
	return scanContacts(s.DB.QueryContext(ctx, `SELECT `+contactCols+` FROM contacts e WHERE e.addressbook_id = ? ORDER BY e.id`, abID))
}

// ContactsByIDs loads several contacts, keeping only those in the given books.
func (s *Store) ContactsByIDs(ctx context.Context, abIDs []int64, ids []int64) ([]*ContactRow, error) {
	if len(abIDs) == 0 || len(ids) == 0 {
		return []*ContactRow{}, nil
	}
	ph1, a1 := inList(abIDs)
	ph2, a2 := inList(ids)
	return scanContacts(s.DB.QueryContext(ctx, `SELECT `+contactCols+` FROM contacts e WHERE e.addressbook_id IN (`+ph1+`) AND e.id IN (`+ph2+`)`, append(a1, a2...)...))
}

// Query filters the contact list.
type Query struct {
	BookIDs      []int64 // required: what the person may see
	UserID       int64   // for starred / recent
	Text         string  // search
	Category     string  // a label (CATEGORIES) or "group:<uid>" for an Apple group card
	Starred      bool
	Kind         string // individual | org | group | "" (everything but groups)
	Has          string // email | phone | birthday | address | photo | none (no phone and no email)
	SortBy       string // given | family
	Recent       bool   // recently viewed first
	UpdatedSince string
	Limit        int
	Offset       int
}

// FindContacts returns the contacts a query matches and how many there are.
func (s *Store) FindContacts(ctx context.Context, q Query) ([]*ContactRow, int, error) {
	if len(q.BookIDs) == 0 {
		return []*ContactRow{}, 0, nil
	}
	ph, args := inList(q.BookIDs)
	where := []string{`e.addressbook_id IN (` + ph + `)`}
	switch q.Kind {
	case "individual", "org", "group":
		where = append(where, `e.kind = ?`)
		args = append(args, q.Kind)
	case "all":
	default:
		where = append(where, `e.kind <> 'group'`)
	}
	if strings.HasPrefix(q.Category, "group:") {
		where = append(where, `e.uid IN (SELECT value FROM contacts g, json_each(g.members) WHERE g.kind = 'group' AND g.uid = ? AND g.addressbook_id IN (`+ph+`))`)
		args = append(args, strings.TrimPrefix(q.Category, "group:"))
		args = append(args, idsAny(q.BookIDs)...)
	} else if q.Category == "-" {
		where = append(where, `e.categories IN ('', '[]', 'null')`)
	} else if q.Category != "" {
		where = append(where, `EXISTS (SELECT 1 FROM json_each(e.categories) WHERE lower(value) = lower(?))`)
		args = append(args, q.Category)
	}
	if q.Starred {
		where = append(where, `EXISTS (SELECT 1 FROM contact_prefs p WHERE p.contact_id = e.id AND p.user_id = ? AND p.starred = 1)`)
		args = append(args, q.UserID)
	}
	switch q.Has {
	case "email":
		where = append(where, `e.emails <> ''`)
	case "phone":
		where = append(where, `e.phones <> ''`)
	case "birthday":
		where = append(where, `e.bday <> ''`)
	case "photo":
		where = append(where, `e.has_photo = 1`)
	case "none":
		where = append(where, `e.emails = '' AND e.phones = ''`)
	}
	if q.UpdatedSince != "" {
		where = append(where, `e.updated_at > ?`)
		args = append(args, q.UpdatedSince)
	}
	if t := strings.TrimSpace(q.Text); t != "" {
		cond, a := searchCond(t)
		where = append(where, cond)
		args = append(args, a...)
	}
	w := strings.Join(where, " AND ")
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM contacts e WHERE `+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := `CASE WHEN e.sort_given = '' THEN 1 ELSE 0 END, e.sort_given, e.id`
	if q.SortBy == "family" {
		order = `CASE WHEN e.sort_family = '' THEN 1 ELSE 0 END, e.sort_family, e.id`
	}
	if q.Recent {
		order = `(SELECT p.viewed_at FROM contact_prefs p WHERE p.contact_id = e.id AND p.user_id = ` + fmt.Sprint(q.UserID) + `) DESC, e.updated_at DESC`
	}
	limit := q.Limit
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+contactCols+` FROM contacts e WHERE `+w+` ORDER BY `+order+` LIMIT ? OFFSET ?`,
		append(args, limit, q.Offset)...)
	list, err := scanContacts(rows, err)
	return list, total, err
}

// searchCond matches names/emails/org/notes through FTS (every word as a
// prefix, diacritics and Cyrillic folded) or, for 3+ digits, any phone that
// contains them.
func searchCond(t string) (string, []any) {
	digits := vcard.Digits(t)
	onlyPhone := len(strings.TrimPrefix(digits, "+")) >= 3 && strings.Trim(t, "0123456789+-/() .") == ""
	var parts []string
	var args []any
	if m := ftsAll(t); m != "" && !onlyPhone {
		parts = append(parts, `e.id IN (SELECT rowid FROM contacts_fts WHERE contacts_fts MATCH ?)`)
		args = append(args, m)
	}
	if d := strings.TrimPrefix(digits, "+"); len(d) >= 3 {
		parts = append(parts, `e.phones LIKE ? ESCAPE '\'`)
		args = append(args, "%"+likeEscape(strings.TrimLeft(d, "0"))+"%")
	}
	if len(parts) == 0 {
		return `0`, nil
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

// ftsAll is an FTS5 query where every word (in any of its spellings) must
// match as a prefix: "đorđe pet" → ("đorđe"* OR "djordje"*) AND "pet"*;
// a word with punctuation needs all its parts ("marko@example" → "marko"* AND "example"*).
func ftsAll(q string) string {
	var groups []string
	for _, w := range strings.Fields(q) {
		var alts []string
		seen := map[string]bool{}
		for _, f := range strings.Fields(vcard.SearchForms(w)) {
			f = strings.Map(func(r rune) rune {
				if strings.ContainsRune(`"*:()^{}+-.,;@'/`, r) {
					return ' '
				}
				return r
			}, f)
			var toks []string
			for _, x := range strings.Fields(f) {
				toks = append(toks, `"`+x+`"*`)
			}
			if len(toks) == 0 {
				continue
			}
			alt := strings.Join(toks, " AND ")
			if len(toks) > 1 {
				alt = "(" + alt + ")"
			}
			if !seen[strings.ToLower(alt)] {
				seen[strings.ToLower(alt)] = true
				alts = append(alts, alt)
			}
		}
		switch len(alts) {
		case 0:
		case 1:
			groups = append(groups, alts[0])
		default:
			groups = append(groups, "("+strings.Join(alts, " OR ")+")")
		}
	}
	return strings.Join(groups, " AND ")
}

func inList(ids []int64) (string, []any) {
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i], args[i] = "?", id
	}
	return strings.Join(ph, ","), args
}

func idsAny(ids []int64) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// Categories counts the labels (CATEGORIES) used in the given books.
func (s *Store) Categories(ctx context.Context, abIDs []int64) (map[string]int, error) {
	out := map[string]int{}
	if len(abIDs) == 0 {
		return out, nil
	}
	ph, args := inList(abIDs)
	rows, err := s.DB.QueryContext(ctx, `SELECT value, COUNT(1) FROM contacts e, json_each(e.categories)
		WHERE e.addressbook_id IN (`+ph+`) AND e.kind <> 'group' GROUP BY lower(value) ORDER BY lower(value)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

// ---------- writes ----------

// ContactWrite is one create-or-replace of a vCard resource.
type ContactWrite struct {
	AddressBookID int64
	Href          string // resource name; defaults to the existing one or <uid>.vcf
	VCF           string
	Source        string
	Actor         int64
	IfMatch       string // etag the client last saw ("" = no check)
	IfNoneMatch   bool   // create only
	FromRemote    bool   // the write comes from the remote address book (not dirty)
	RemoteHref    string
	RemoteETag    string
}

// index is what the columns keep of a card.
type index struct {
	uid, kind, fn, sortGiven, sortFamily, given, family, nickname, org, title string
	emails, phones, bday, anniversary, categories, members, photoHash         string
	hasPhoto                                                                  bool
	ftsNames, ftsEmails, ftsPhones, ftsOrg, ftsOther                          string
}

// IndexCard computes the index columns of a card.
func indexCard(c *vcard.Card) (*index, error) {
	ct := c.Contact()
	ix := &index{uid: c.UID(), kind: ct.Kind, given: ct.Given, family: ct.Family, nickname: ct.Nickname, org: ct.Org, title: ct.Title,
		bday: ct.Birthday, anniversary: ct.Anniversary}
	if ix.uid == "" {
		return nil, errors.New("the vCard has no UID")
	}
	ix.fn = ct.DisplayName()
	given := vcard.Fold(strings.Join(nonEmpty(ct.Given, ct.Middle, ct.Family), " "))
	family := vcard.Fold(strings.Join(nonEmpty(ct.Family, ct.Given, ct.Middle), " "))
	if given == "" || ct.Kind == "org" {
		given = vcard.Fold(ix.fn)
	}
	if family == "" || ct.Kind == "org" {
		family = vcard.Fold(ix.fn)
	}
	ix.sortGiven, ix.sortFamily = given, family
	var emails, phones, phoneForms []string
	for _, f := range ct.Emails {
		emails = append(emails, strings.ToLower(f.Value))
	}
	for _, f := range ct.Phones {
		d := vcard.Digits(f.Value)
		if d == "" {
			continue
		}
		phones = append(phones, d)
		bare := strings.TrimLeft(strings.TrimPrefix(d, "+"), "0")
		phoneForms = append(phoneForms, bare, vcard.PhoneKey(d))
	}
	ix.emails, ix.phones = strings.Join(emails, " "), strings.Join(phones, " ")
	ix.categories = jsonMarshal(nonNil(ct.Categories))
	if len(ct.Members) > 0 {
		ix.members = jsonMarshal(ct.Members)
	}
	if b, _, uri := c.Photo(); len(b) > 0 || uri != "" {
		ix.hasPhoto = true
		h := sha1.Sum(append(b, uri...))
		ix.photoHash = hex.EncodeToString(h[:6])
	}
	names := nonEmpty(ix.fn, ct.ComposedName(), ct.Nickname, ct.PhoneticGiven, ct.PhoneticFamily)
	ix.ftsNames = vcard.SearchForms(strings.Join(names, " "))
	ix.ftsEmails = strings.Join(emails, " ")
	// an address is found by its parts: "marko", "example", "com"
	ix.ftsEmails += " " + strings.NewReplacer("@", " ", ".", " ", "_", " ", "-", " ").Replace(ix.ftsEmails)
	ix.ftsPhones = strings.Join(phoneForms, " ")
	ix.ftsOrg = vcard.SearchForms(strings.Join(nonEmpty(ct.Org, ct.Department, ct.Title), " "))
	var other []string
	other = append(other, ct.Note)
	other = append(other, ct.Categories...)
	for _, a := range ct.Addresses {
		other = append(other, a.Street, a.City, a.Region, a.Country, a.Postcode)
	}
	for _, f := range append(append(append([]vcard.Field{}, ct.URLs...), ct.IMs...), append(ct.Socials, ct.Related...)...) {
		other = append(other, f.Value)
	}
	ix.ftsOther = vcard.SearchForms(strings.Join(nonEmpty(other...), " "))
	return ix, nil
}

func nonEmpty(xs ...string) []string {
	var out []string
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			out = append(out, strings.TrimSpace(x))
		}
	}
	return out
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

// ETag is the strong validator of a stored vCard.
func ETag(vcf string) string {
	h := sha1.Sum([]byte(vcf))
	return `"` + hex.EncodeToString(h[:10]) + `"`
}

// SaveContact creates or replaces a vCard and keeps the index, search,
// change numbers and the remote mirror state in step. It is the only write path.
func (s *Store) SaveContact(ctx context.Context, w ContactWrite, hooks ...Hook) (*ContactRow, bool, error) {
	card, err := vcard.ParseOne([]byte(w.VCF))
	if err != nil {
		return nil, false, err
	}
	ix, err := indexCard(card)
	if err != nil {
		return nil, false, err
	}
	ab, err := s.GetAddressBook(ctx, w.AddressBookID)
	if err != nil {
		return nil, false, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback() //nolint:errcheck

	var existing *ContactRow
	if w.Href != "" {
		existing, err = scanContact(tx.QueryRowContext(ctx, `SELECT `+contactCols+` FROM contacts e WHERE e.addressbook_id = ? AND e.href = ?`, w.AddressBookID, w.Href))
	} else {
		existing, err = scanContact(tx.QueryRowContext(ctx, `SELECT `+contactCols+` FROM contacts e WHERE e.addressbook_id = ? AND e.uid = ?`, w.AddressBookID, ix.uid))
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}
	if w.IfNoneMatch && existing != nil {
		return nil, false, ErrPrecondition
	}
	if w.IfMatch != "" && w.IfMatch != "*" && (existing == nil || existing.ETag != w.IfMatch) {
		return nil, false, ErrPrecondition
	}
	if w.IfMatch == "*" && existing == nil {
		return nil, false, ErrPrecondition
	}
	if existing != nil && existing.UID != ix.uid {
		return nil, false, fmt.Errorf("%w (the resource holds %q)", ErrUIDTaken, existing.UID)
	}
	if existing == nil {
		var other int
		_ = tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM contacts WHERE addressbook_id = ? AND uid = ?`, w.AddressBookID, ix.uid).Scan(&other)
		if other > 0 {
			return nil, false, ErrUIDTaken
		}
	}
	href := w.Href
	if href == "" {
		href = SafeHref(ix.uid) + ".vcf"
	}
	seq, err := s.BumpAddressBook(ctx, tx, w.AddressBookID)
	if err != nil {
		return nil, false, err
	}
	etag := ETag(w.VCF)
	dirty := ab.AccountID != nil && !w.FromRemote
	now := Now()
	var id int64
	created := existing == nil
	if existing == nil {
		res, err := tx.ExecContext(ctx, `INSERT INTO contacts (addressbook_id, uid, href, etag, vcf, kind, fn, sort_given, sort_family,
			given, family, nickname, org, title, emails, phones, bday, anniversary, categories, members, has_photo, photo_hash, source,
			created_by, updated_by, seq, remote_href, remote_etag, dirty, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			w.AddressBookID, ix.uid, href, etag, w.VCF, ix.kind, ix.fn, ix.sortGiven, ix.sortFamily, ix.given, ix.family,
			ix.nickname, ix.org, ix.title, ix.emails, ix.phones, ix.bday, ix.anniversary, ix.categories, ix.members, ix.hasPhoto,
			ix.photoHash, w.Source, w.Actor, w.Actor, seq, w.RemoteHref, w.RemoteETag, dirty, now, now)
		if err != nil {
			return nil, false, conflict(err)
		}
		id, _ = res.LastInsertId()
		// a resource name that comes back after a delete is no longer a tombstone
		_, _ = tx.ExecContext(ctx, `DELETE FROM tombstones WHERE addressbook_id = ? AND href = ?`, w.AddressBookID, href)
	} else {
		id = existing.ID
		remoteHref, remoteETag := existing.RemoteHref, existing.RemoteETag
		if w.FromRemote {
			remoteHref, remoteETag = w.RemoteHref, w.RemoteETag
		}
		dirtyArg := dirty || (existing.Dirty && !w.FromRemote)
		if _, err := tx.ExecContext(ctx, `UPDATE contacts SET etag = ?, vcf = ?, kind = ?, fn = ?, sort_given = ?, sort_family = ?,
			given = ?, family = ?, nickname = ?, org = ?, title = ?, emails = ?, phones = ?, bday = ?, anniversary = ?, categories = ?,
			members = ?, has_photo = ?, photo_hash = ?, source = CASE WHEN source = '' THEN ? ELSE source END, updated_by = ?, seq = ?,
			remote_href = ?, remote_etag = ?, dirty = ?, updated_at = ? WHERE id = ?`,
			etag, w.VCF, ix.kind, ix.fn, ix.sortGiven, ix.sortFamily, ix.given, ix.family, ix.nickname, ix.org, ix.title, ix.emails,
			ix.phones, ix.bday, ix.anniversary, ix.categories, ix.members, ix.hasPhoto, ix.photoHash, w.Source, w.Actor, seq,
			remoteHref, remoteETag, dirtyArg, now, id); err != nil {
			return nil, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM contacts_fts WHERE rowid = ?`, id); err != nil {
		return nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO contacts_fts (rowid, names, emails, phones, org, other) VALUES (?, ?, ?, ?, ?, ?)`,
		id, ix.ftsNames, ix.ftsEmails, ix.ftsPhones, ix.ftsOrg, ix.ftsOther); err != nil {
		return nil, false, err
	}
	for _, h := range hooks {
		if h != nil {
			if err := h(tx); err != nil {
				return nil, false, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	e, err := s.GetContact(ctx, id)
	return e, created, err
}

// SafeHref turns a UID into a resource name.
func SafeHref(uid string) string {
	var b strings.Builder
	for _, r := range uid {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.', r == '@':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 || b.Len() > 200 {
		return RandomToken(16)
	}
	return b.String()
}

// DeleteContactOpts says how a delete happens.
type DeleteContactOpts struct {
	Actor      int64
	KeepCopy   bool // restorable from "Recently deleted"
	FromRemote bool // the remote address book deleted it already
	IfMatch    string
}

// DeleteContact removes a vCard resource.
func (s *Store) DeleteContact(ctx context.Context, e *ContactRow, o DeleteContactOpts, hooks ...Hook) error {
	ab, err := s.GetAddressBook(ctx, e.AddressBookID)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if o.IfMatch != "" && o.IfMatch != "*" {
		var etag string
		if err := tx.QueryRowContext(ctx, `SELECT etag FROM contacts WHERE id = ?`, e.ID).Scan(&etag); err != nil || etag != o.IfMatch {
			return ErrPrecondition
		}
	}
	seq, err := s.BumpAddressBook(ctx, tx, e.AddressBookID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO tombstones (addressbook_id, href, seq) VALUES (?, ?, ?)`, e.AddressBookID, e.Href, seq); err != nil {
		return err
	}
	if ab.AccountID != nil && !o.FromRemote && e.RemoteHref != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO remote_deletes (addressbook_id, remote_href, remote_etag) VALUES (?, ?, ?)`, e.AddressBookID, e.RemoteHref, e.RemoteETag); err != nil {
			return err
		}
	}
	if o.KeepCopy {
		uid := o.Actor
		if uid == 0 {
			uid = ab.UserID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO deleted_contacts (user_id, addressbook_id, uid, fn, vcf, deleted_by) VALUES (?, ?, ?, ?, ?, ?)`,
			uid, e.AddressBookID, e.UID, e.FN, e.VCF, o.Actor); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM contacts_fts WHERE rowid = ?`, e.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM contacts WHERE id = ?`, e.ID); err != nil {
		return err
	}
	for _, h := range hooks {
		if h != nil {
			if err := h(tx); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Reindex rebuilds the index columns and search rows of every contact
// (run after the indexing rules change; cheap for a few thousand cards).
func (s *Store) Reindex(ctx context.Context) (int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, vcf FROM contacts`)
	if err != nil {
		return 0, err
	}
	type rv struct {
		id  int64
		vcf string
	}
	var all []rv
	for rows.Next() {
		var r rv
		if err := rows.Scan(&r.id, &r.vcf); err != nil {
			rows.Close()
			return 0, err
		}
		all = append(all, r)
	}
	rows.Close()
	n := 0
	for _, r := range all {
		card, err := vcard.ParseOne([]byte(r.vcf))
		if err != nil {
			continue
		}
		ix, err := indexCard(card)
		if err != nil {
			continue
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE contacts SET kind = ?, fn = ?, sort_given = ?, sort_family = ?, given = ?, family = ?,
			nickname = ?, org = ?, title = ?, emails = ?, phones = ?, bday = ?, anniversary = ?, categories = ?, members = ?, has_photo = ?,
			photo_hash = ? WHERE id = ?`, ix.kind, ix.fn, ix.sortGiven, ix.sortFamily, ix.given, ix.family, ix.nickname, ix.org, ix.title,
			ix.emails, ix.phones, ix.bday, ix.anniversary, ix.categories, ix.members, ix.hasPhoto, ix.photoHash, r.id); err != nil {
			return n, err
		}
		_, _ = s.DB.ExecContext(ctx, `DELETE FROM contacts_fts WHERE rowid = ?`, r.id)
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO contacts_fts (rowid, names, emails, phones, org, other) VALUES (?, ?, ?, ?, ?, ?)`,
			r.id, ix.ftsNames, ix.ftsEmails, ix.ftsPhones, ix.ftsOrg, ix.ftsOther); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// ---------- CardDAV sync ----------

// Change is one resource changed (or gone) since a sync token.
type Change struct {
	Href    string
	ETag    string
	Deleted bool
}

// ChangesSince lists resources changed after seq (and those deleted).
func (s *Store) ChangesSince(ctx context.Context, abID, seq int64) ([]Change, error) {
	var out []Change
	rows, err := s.DB.QueryContext(ctx, `SELECT href, etag FROM contacts WHERE addressbook_id = ? AND seq > ? ORDER BY seq`, abID, seq)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c Change
		if err := rows.Scan(&c.Href, &c.ETag); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	rows.Close()
	rows, err = s.DB.QueryContext(ctx, `SELECT href FROM tombstones WHERE addressbook_id = ? AND seq > ? AND href NOT IN
		(SELECT href FROM contacts WHERE addressbook_id = ?) GROUP BY href`, abID, seq, abID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		c := Change{Deleted: true}
		if err := rows.Scan(&c.Href); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// OldestTombstone is the smallest seq still remembered for deletes (sync
// tokens older than that can't be answered and get a full resync).
func (s *Store) OldestTombstone(ctx context.Context, abID int64) int64 {
	var n sql.NullInt64
	_ = s.DB.QueryRowContext(ctx, `SELECT MIN(seq) FROM tombstones WHERE addressbook_id = ?`, abID).Scan(&n)
	return n.Int64
}

// PruneTombstones forgets deletes older than keep change numbers per book.
func (s *Store) PruneTombstones(ctx context.Context, keep int) {
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM tombstones WHERE id IN (SELECT id FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY addressbook_id ORDER BY seq DESC) AS rn FROM tombstones) WHERE rn > ?)`, keep)
}

// DirtyContacts are local changes not yet pushed to the remote address book.
func (s *Store) DirtyContacts(ctx context.Context, abID int64) ([]*ContactRow, error) {
	return scanContacts(s.DB.QueryContext(ctx, `SELECT `+contactCols+` FROM contacts e WHERE e.addressbook_id = ? AND e.dirty = 1`, abID))
}

// MarkPushed records that a local change reached the remote (unless it changed again meanwhile).
func (s *Store) MarkPushed(ctx context.Context, id, seq int64, remoteHref, remoteETag string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE contacts SET remote_href = ?, remote_etag = ?, dirty = CASE WHEN seq = ? THEN 0 ELSE dirty END WHERE id = ?`,
		remoteHref, remoteETag, seq, id)
	return err
}

// RemoteDelete is a delete the remote has not heard of yet.
type RemoteDelete struct {
	ID         int64
	RemoteHref string
	RemoteETag string
}

// RemoteDeletes lists pending remote deletes of an address book.
func (s *Store) RemoteDeletes(ctx context.Context, abID int64) ([]RemoteDelete, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, remote_href, remote_etag FROM remote_deletes WHERE addressbook_id = ?`, abID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RemoteDelete
	for rows.Next() {
		var d RemoteDelete
		if err := rows.Scan(&d.ID, &d.RemoteHref, &d.RemoteETag); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DoneRemoteDelete forgets a pending remote delete.
func (s *Store) DoneRemoteDelete(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM remote_deletes WHERE id = ?`, id)
	return err
}

// ---------- recently deleted ----------

// ListDeleted lists a person's recently deleted contacts, newest first.
func (s *Store) ListDeleted(ctx context.Context, userID int64) ([]DeletedContact, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, addressbook_id, uid, fn, vcf, deleted_at FROM deleted_contacts WHERE user_id = ? ORDER BY deleted_at DESC LIMIT 500`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeletedContact{}
	for rows.Next() {
		var d DeletedContact
		if err := rows.Scan(&d.ID, &d.AddressBookID, &d.UID, &d.FN, &d.VCF, &d.DeletedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDeleted returns one of a person's recently deleted contacts.
func (s *Store) GetDeleted(ctx context.Context, userID, id int64) (*DeletedContact, error) {
	var d DeletedContact
	err := s.DB.QueryRowContext(ctx, `SELECT id, addressbook_id, uid, fn, vcf, deleted_at FROM deleted_contacts WHERE id = ? AND user_id = ?`, id, userID).
		Scan(&d.ID, &d.AddressBookID, &d.UID, &d.FN, &d.VCF, &d.DeletedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &d, nil
}

// ForgetDeleted removes an entry from Recently deleted.
func (s *Store) ForgetDeleted(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM deleted_contacts WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PurgeDeleted drops recently deleted contacts older than days.
func (s *Store) PurgeDeleted(ctx context.Context, days int) {
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM deleted_contacts WHERE deleted_at < strftime('%Y-%m-%dT%H:%M:%fZ', 'now', ?)`, fmt.Sprintf("-%d days", days))
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM reminder_log WHERE fired_at < strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '-400 days')`)
}

// ---------- per-person marks ----------

// ContactPrefs returns which of the given contacts a person starred.
func (s *Store) Starred(ctx context.Context, userID int64) map[int64]bool {
	out := map[int64]bool{}
	rows, err := s.DB.QueryContext(ctx, `SELECT contact_id FROM contact_prefs WHERE user_id = ? AND starred = 1`, userID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out[id] = true
		}
	}
	return out
}

// SetStarred marks or unmarks a contact as a favourite of a person.
func (s *Store) SetStarred(ctx context.Context, userID, contactID int64, on bool) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO contact_prefs (user_id, contact_id, starred) VALUES (?, ?, ?)
		ON CONFLICT (user_id, contact_id) DO UPDATE SET starred = excluded.starred`, userID, contactID, on)
	return err
}

// MarkViewed records that a person opened a contact (for "Recent").
func (s *Store) MarkViewed(ctx context.Context, userID, contactID int64) {
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO contact_prefs (user_id, contact_id, viewed_at) VALUES (?, ?, ?)
		ON CONFLICT (user_id, contact_id) DO UPDATE SET viewed_at = excluded.viewed_at`, userID, contactID, Now())
}

// ---------- birthdays ----------

// DatedContacts returns the contacts (in the given books) with a birthday or
// an anniversary; the caller works out the next occurrence.
func (s *Store) DatedContacts(ctx context.Context, abIDs []int64) ([]*ContactRow, error) {
	if len(abIDs) == 0 {
		return []*ContactRow{}, nil
	}
	ph, args := inList(abIDs)
	return scanContacts(s.DB.QueryContext(ctx, `SELECT `+contactCols+` FROM contacts e WHERE e.addressbook_id IN (`+ph+`)
		AND e.kind <> 'group' AND (e.bday <> '' OR e.anniversary <> '')`, args...))
}

// ReminderFired records a birthday reminder and reports whether it had
// gone out already (true: don't send it again).
func (s *Store) ReminderFired(ctx context.Context, userID, contactID int64, day, kind string) bool {
	res, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO reminder_log (user_id, contact_id, day, kind) VALUES (?, ?, ?, ?)`, userID, contactID, day, kind)
	if err != nil {
		return true
	}
	n, _ := res.RowsAffected()
	return n == 0
}

// ---------- duplicates ----------

// NotDuplicate remembers that two contacts are different people.
func (s *Store) NotDuplicate(ctx context.Context, userID int64, a, b string) error {
	if a > b {
		a, b = b, a
	}
	_, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO not_duplicates (user_id, a_uid, b_uid) VALUES (?, ?, ?)`, userID, a, b)
	return err
}

// NotDuplicates returns the pairs a person said are different ("a|b", a < b).
func (s *Store) NotDuplicates(ctx context.Context, userID int64) map[string]bool {
	out := map[string]bool{}
	rows, err := s.DB.QueryContext(ctx, `SELECT a_uid, b_uid FROM not_duplicates WHERE user_id = ?`, userID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var a, b string
		if rows.Scan(&a, &b) == nil {
			out[a+"|"+b] = true
		}
	}
	return out
}

// ---------- activity ----------

// AddActivity appends to a person's change log.
func (s *Store) AddActivity(ctx context.Context, userID int64, abID, contactID *int64, action, summary, detail string) {
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO activity (user_id, addressbook_id, contact_id, action, summary, detail) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, nullInt(abID), nullInt(contactID), action, summary, detail)
}

// ListActivity returns a person's latest changes.
func (s *Store) ListActivity(ctx context.Context, userID int64, contactID int64, limit int) ([]Activity, error) {
	q := `SELECT id, addressbook_id, contact_id, action, summary, detail, created_at FROM activity WHERE user_id = ?`
	args := []any{userID}
	if contactID > 0 {
		q += ` AND contact_id = ?`
		args = append(args, contactID)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		var ab, ct sql.NullInt64
		if err := rows.Scan(&a.ID, &ab, &ct, &a.Action, &a.Summary, &a.Detail, &a.CreatedAt); err != nil {
			return nil, err
		}
		if ab.Valid {
			a.AddressBookID = &ab.Int64
		}
		if ct.Valid {
			a.ContactID = &ct.Int64
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// PruneActivity keeps the newest entries of a person's log.
func (s *Store) PruneActivity(ctx context.Context, userID int64, keep int) {
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM activity WHERE user_id = ? AND id NOT IN (SELECT id FROM activity WHERE user_id = ? ORDER BY id DESC LIMIT ?)`, userID, userID, keep)
}

// ---------- accounts ----------

const accountCols = `id, user_id, kind, name, url, username, secret, via, mode, sync_minutes, enabled, last_sync_at, last_error, created_at, updated_at`

func scanAccount(row interface{ Scan(...any) error }) (*Account, error) {
	var a Account
	if err := row.Scan(&a.ID, &a.UserID, &a.Kind, &a.Name, &a.URL, &a.Username, &a.Secret, &a.Via, &a.Mode, &a.SyncMinutes,
		&a.Enabled, &a.LastSyncAt, &a.LastError, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	a.HasSecret = a.Secret != ""
	return &a, nil
}

// ListAccounts returns a person's external accounts (all accounts for userID 0).
func (s *Store) ListAccounts(ctx context.Context, userID int64) ([]*Account, error) {
	q := `SELECT ` + accountCols + `, (SELECT COUNT(1) FROM addressbooks c WHERE c.account_id = a.id) FROM accounts a`
	var args []any
	if userID > 0 {
		q += ` WHERE user_id = ?`
		args = append(args, userID)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Account{}
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.UserID, &a.Kind, &a.Name, &a.URL, &a.Username, &a.Secret, &a.Via, &a.Mode, &a.SyncMinutes,
			&a.Enabled, &a.LastSyncAt, &a.LastError, &a.CreatedAt, &a.UpdatedAt, &a.AddressBooks); err != nil {
			return nil, err
		}
		a.HasSecret = a.Secret != ""
		out = append(out, &a)
	}
	return out, rows.Err()
}

// GetAccount returns one of a person's accounts (any account for userID 0).
func (s *Store) GetAccount(ctx context.Context, userID, id int64) (*Account, error) {
	if userID == 0 {
		return scanAccount(s.DB.QueryRowContext(ctx, `SELECT `+accountCols+` FROM accounts WHERE id = ?`, id))
	}
	return scanAccount(s.DB.QueryRowContext(ctx, `SELECT `+accountCols+` FROM accounts WHERE id = ? AND user_id = ?`, id, userID))
}

// SaveAccount inserts or updates an account.
func (s *Store) SaveAccount(ctx context.Context, a *Account) error {
	if a.Mode == "" {
		a.Mode = "two_way"
	}
	if a.Via == "" {
		a.Via = "direct"
	}
	if a.ID == 0 {
		res, err := s.DB.ExecContext(ctx, `INSERT INTO accounts (user_id, kind, name, url, username, secret, via, mode, sync_minutes, enabled)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, a.UserID, a.Kind, a.Name, a.URL, a.Username, a.Secret, a.Via, a.Mode, a.SyncMinutes, a.Enabled)
		if err != nil {
			return err
		}
		a.ID, _ = res.LastInsertId()
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE accounts SET name = ?, url = ?, username = ?, secret = ?, via = ?, mode = ?, sync_minutes = ?,
		enabled = ?, updated_at = ? WHERE id = ?`, a.Name, a.URL, a.Username, a.Secret, a.Via, a.Mode, a.SyncMinutes, a.Enabled, Now(), a.ID)
	return err
}

// SetAccountSync records the outcome of a sync.
func (s *Store) SetAccountSync(ctx context.Context, id int64, errText string) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE accounts SET last_sync_at = ?, last_error = ? WHERE id = ?`, Now(), errText, id)
}

// DeleteAccount removes an account. keepBooks keeps its address books as
// local ones (with their contacts); otherwise they are deleted too.
func (s *Store) DeleteAccount(ctx context.Context, userID, id int64, keepBooks bool) error {
	books, err := s.AddressBooksOfAccount(ctx, id)
	if err != nil {
		return err
	}
	for _, b := range books {
		if keepBooks {
			_, _ = s.DB.ExecContext(ctx, `UPDATE addressbooks SET kind = 'local', account_id = NULL, remote_href = '', remote_ctag = '', remote_token = '' WHERE id = ?`, b.ID)
			_, _ = s.DB.ExecContext(ctx, `UPDATE contacts SET remote_href = '', remote_etag = '', dirty = 0 WHERE addressbook_id = ?`, b.ID)
			_, _ = s.DB.ExecContext(ctx, `DELETE FROM remote_deletes WHERE addressbook_id = ?`, b.ID)
		} else if err := s.DeleteAddressBook(ctx, b.ID); err != nil {
			return err
		}
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM accounts WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SortStrings sorts case-insensitively (helper for category lists).
func SortStrings(xs []string) {
	sort.Slice(xs, func(i, j int) bool { return strings.ToLower(xs[i]) < strings.ToLower(xs[j]) })
}
