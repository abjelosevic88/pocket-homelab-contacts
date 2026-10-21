package server

import (
	"context"
	"database/sql"
	"errors"
	"hash/fnv"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/store"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/vcard"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// contactRoutes is the REST API of docs/api.md (except devices and external
// accounts): address books and sharing, contacts, photos, search, labels,
// birthdays, duplicates, import/export, recently deleted, activity and the
// contact's page in the other Pocket apps.
func (s *Server) contactRoutes(r chi.Router) {
	r.Get("/addressbooks", s.handleListBooks)
	r.Post("/addressbooks", s.handleCreateBook)
	r.Put("/addressbooks/{id}", s.handleUpdateBook)
	r.Delete("/addressbooks/{id}", s.handleDeleteBook)
	r.Post("/addressbooks/{id}/leave", s.handleLeaveBook)
	r.Get("/addressbooks/{id}/shares", s.handleListShares)
	r.Post("/addressbooks/{id}/shares", s.handleCreateShare)
	r.Get("/addressbooks/{id}/export.vcf", s.handleExportBook)
	r.Put("/shares/{sid}", s.handleUpdateShare)
	r.Delete("/shares/{sid}", s.handleDeleteShare)
	r.Get("/people", s.handlePeople)

	r.Get("/contacts", s.handleListContacts)
	r.Post("/contacts", s.handleCreateContact)
	r.Post("/contacts/bulk", s.handleBulk)
	r.Get("/contacts/{id}", s.handleGetContact)
	r.Put("/contacts/{id}", s.handlePutContact)
	r.Patch("/contacts/{id}", s.handlePatchContact)
	r.Delete("/contacts/{id}", s.handleDeleteContact)
	r.Get("/contacts/{id}/vcf", s.handleContactVCF)
	r.Get("/contacts/{id}/photo", s.handleGetPhoto)
	r.Put("/contacts/{id}/photo", s.handlePutPhoto)
	r.Delete("/contacts/{id}/photo", s.handleDeletePhoto)
	r.Get("/contacts/{id}/related", s.handleRelated)
	r.Post("/contacts/{id}/task", s.handleRelatedTask)
	r.Post("/contacts/{id}/event", s.handleRelatedEvent)
	r.Get("/contacts/{id}/activity", s.handleContactActivity)

	r.Get("/search", s.handleSearch)
	r.Get("/suggest", s.handleSuggest)
	r.Get("/lookup", s.handleLookup)
	r.Get("/categories", s.handleCategories)
	r.Post("/categories/rename", s.handleRenameCategory)
	r.Post("/categories/delete", s.handleDeleteCategory)

	r.Get("/birthdays", s.handleBirthdays)
	r.Get("/feeds", s.handleFeeds)
	r.Post("/feeds/rotate", s.handleRotateFeed)

	r.Get("/duplicates", s.handleDuplicates)
	r.Post("/duplicates/merge", s.handleMerge)
	r.Post("/duplicates/ignore", s.handleIgnoreDuplicates)

	r.Post("/import", s.handleImport)
	r.Get("/export.vcf", s.handleExport)

	r.Get("/deleted", s.handleListDeleted)
	r.Post("/deleted/{id}/restore", s.handleRestoreDeleted)
	r.Delete("/deleted/{id}", s.handleForgetDeleted)
	r.Get("/activity", s.handleActivity)
}

// ---------- who is acting ----------

// actor is who makes a change: the person, the Pocket app acting for them
// through a Core link ("" otherwise) and the source recorded on the contact.
type actor struct {
	u      *store.User
	app    string
	source string
}

func (s *Server) actorOf(r *http.Request) actor {
	return actor{u: userFrom(r.Context()), app: s.linkApp(r), source: s.source(r)}
}

// linkMayChange is the rule for Pocket apps acting through a link token:
// they change and delete only the contacts they created themselves.
func linkMayChange(app, source string) error {
	if app == "" || app == source {
		return nil
	}
	return errForbidden("an app may only change the contacts it created itself")
}

// ---------- access ----------

// loadContact returns a contact the person may see with their role on its
// address book; anything else is ErrNotFound (404).
func (s *Server) loadContact(ctx context.Context, uid, id int64) (*store.ContactRow, string, *store.AddressBook, error) {
	e, err := s.store.GetContact(ctx, id)
	if err != nil {
		return nil, "", nil, err
	}
	role, ab, err := s.store.BookRole(ctx, uid, e.AddressBookID)
	if err != nil {
		return nil, "", nil, err
	}
	return e, role, ab, nil
}

// canWrite says why a person may not add or change contacts in a book (nil = they may).
func canWrite(role string, ab *store.AddressBook) error {
	if !store.RoleAtLeast(role, store.RoleWrite) {
		return errForbidden("you can only read " + ab.Name)
	}
	if ab.ReadOnly {
		return errForbidden("this address book is read-only (it mirrors an account)")
	}
	return nil
}

// writableContact loads a contact the actor may change.
func (s *Server) writableContact(ctx context.Context, a actor, id int64) (*store.ContactRow, *store.AddressBook, error) {
	e, role, ab, err := s.loadContact(ctx, a.u.ID, id)
	if err != nil {
		return nil, nil, err
	}
	if err := canWrite(role, ab); err != nil {
		return nil, nil, err
	}
	if err := linkMayChange(a.app, e.Source); err != nil {
		return nil, nil, err
	}
	return e, ab, nil
}

// writableBook returns an address book the person may add contacts to.
func (s *Server) writableBook(ctx context.Context, u *store.User, id int64) (*store.AddressBook, error) {
	role, ab, err := s.store.BookRole(ctx, u.ID, id)
	if err != nil {
		return nil, err
	}
	if err := canWrite(role, ab); err != nil {
		return nil, err
	}
	return ab, nil
}

// defaultBook is where a person's new contacts go: their chosen book while
// they can still write it, else their own default.
func (s *Server) defaultBook(ctx context.Context, u *store.User) (*store.AddressBook, error) {
	st, _ := s.store.GetSettings(ctx, u.ID)
	if st.DefaultAddressBookID > 0 {
		if ab, err := s.writableBook(ctx, u, st.DefaultAddressBookID); err == nil {
			return ab, nil
		}
	}
	ab, err := s.store.DefaultAddressBook(ctx, u.ID)
	if errors.Is(err, store.ErrNotFound) {
		s.seedUser(ctx, u)
		ab, err = s.store.DefaultAddressBook(ctx, u.ID)
	}
	return ab, err
}

// targetBook is the book a new contact goes to: the one asked for, else the default.
func (s *Server) targetBook(ctx context.Context, u *store.User, id int64) (*store.AddressBook, error) {
	if id > 0 {
		return s.writableBook(ctx, u, id)
	}
	return s.defaultBook(ctx, u)
}

// writableBookIDs are the books a person may change contacts in.
func (s *Server) writableBookIDs(ctx context.Context, uid int64) []int64 {
	books, _ := s.store.ListAddressBooksFor(ctx, uid)
	var out []int64
	for _, b := range books {
		if canWrite(b.Role, b) == nil {
			out = append(out, b.ID)
		}
	}
	return out
}

// ---------- views ----------

// Summary is a contact as a list row (docs/api.md).
type Summary struct {
	ID            int64    `json:"id"`
	AddressBookID int64    `json:"addressbook_id"`
	UID           string   `json:"uid"`
	Kind          string   `json:"kind"`
	FN            string   `json:"fn"`
	Given         string   `json:"given"`
	Family        string   `json:"family"`
	Nickname      string   `json:"nickname"`
	Org           string   `json:"org"`
	Title         string   `json:"title"`
	Emails        []string `json:"emails"`
	Phones        []string `json:"phones"`
	Birthday      string   `json:"birthday"`
	Categories    []string `json:"categories"`
	HasPhoto      bool     `json:"has_photo"`
	PhotoURL      string   `json:"photo_url"`
	Starred       bool     `json:"starred"`
	Initials      string   `json:"initials"`
	Color         string   `json:"color"`
	UpdatedAt     string   `json:"updated_at"`
}

// ContactView is one contact with its structured card.
type ContactView struct {
	Summary
	Card        vcard.Contact    `json:"card"`
	ETag        string           `json:"etag"`
	Role        string           `json:"role"`
	AddressBook bookRef          `json:"addressbook"`
	Source      string           `json:"source"`
	Groups      []store.GroupRef `json:"groups"`
	URL         string           `json:"url"`
	CreatedAt   string           `json:"created_at"`
}

type bookRef struct {
	ID    int64         `json:"id"`
	Name  string        `json:"name"`
	Color string        `json:"color"`
	Owner *store.Person `json:"owner"`
}

// avatarColors are the backgrounds of the initials avatars.
var avatarColors = []string{"#e11d48", "#db2777", "#9333ea", "#4f46e5", "#0284c7", "#0891b2", "#0d9488", "#059669", "#65a30d", "#d97706"}

func avatarColor(uid string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(uid))
	return avatarColors[h.Sum32()%uint32(len(avatarColors))]
}

func firstLetter(s string) string {
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return string(unicode.ToUpper(r))
		}
	}
	return ""
}

// initials are the letters of the avatar: given + family, else the first
// and last word of the display name.
func initials(kind, given, family, fn string) string {
	if kind != "org" {
		if v := firstLetter(given) + firstLetter(family); v != "" {
			return v
		}
	}
	words := strings.Fields(fn)
	switch {
	case len(words) == 0:
		return "?"
	case len(words) == 1 || strings.Contains(fn, "@"):
		return firstLetter(words[0])
	}
	return firstLetter(words[0]) + firstLetter(words[len(words)-1])
}

// cardParts is what a list row needs from the vCard itself (cached by etag).
type cardParts struct {
	emails, phones []string
	photo          bool // an embedded picture (not just an address)
}

var (
	partsCache sync.Map
	partsCount atomic.Int64
)

func contactParts(e *store.ContactRow) cardParts {
	key := e.ETag
	if v, ok := partsCache.Load(key); ok && key != "" {
		return v.(cardParts)
	}
	p := cardParts{emails: []string{}, phones: []string{}}
	if c, err := vcard.ParseOne([]byte(e.VCF)); err == nil {
		ct := c.Contact()
		for _, f := range ct.Emails {
			p.emails = append(p.emails, f.Value)
		}
		for _, f := range ct.Phones {
			p.phones = append(p.phones, f.Value)
		}
		b, _, _ := c.Photo()
		p.photo = len(b) > 0
	}
	if partsCount.Add(1) > 50000 {
		partsCache.Clear()
		partsCount.Store(0)
	}
	partsCache.Store(key, p)
	return p
}

func (s *Server) summary(e *store.ContactRow, starred bool) Summary {
	p := contactParts(e)
	sm := Summary{ID: e.ID, AddressBookID: e.AddressBookID, UID: e.UID, Kind: e.Kind, FN: e.FN, Given: e.Given,
		Family: e.Family, Nickname: e.Nickname, Org: e.Org, Title: e.Title, Emails: p.emails, Phones: p.phones,
		Birthday: e.Bday, Categories: e.Categories, HasPhoto: e.HasPhoto, Starred: starred,
		Initials: initials(e.Kind, e.Given, e.Family, e.FN), Color: avatarColor(e.UID), UpdatedAt: e.UpdatedAt}
	if sm.Categories == nil {
		sm.Categories = []string{}
	}
	if e.HasPhoto && p.photo {
		sm.PhotoURL = "/api/v1/contacts/" + strconv.FormatInt(e.ID, 10) + "/photo?v=" + e.PhotoHash
	}
	return sm
}

func (s *Server) summaries(ctx context.Context, uid int64, rows []*store.ContactRow) []Summary {
	starred := s.store.Starred(ctx, uid)
	out := make([]Summary, 0, len(rows))
	for _, e := range rows {
		out = append(out, s.summary(e, starred[e.ID]))
	}
	return out
}

// appURL is an absolute address of this app (BASE_URL, else the request's host).
func (s *Server) appURL(r *http.Request, path string) string {
	if s.cfg.BaseURL != "" || r == nil {
		return s.cfg.BaseURL + path
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host + path
}

// contactURL is the contact's page: what notes and tasks link to.
func (s *Server) contactURL(r *http.Request, id int64) string {
	return s.appURL(r, "/contacts/"+strconv.FormatInt(id, 10))
}

// cardJSON fills nil lists so the web app gets [] rather than null.
func cardJSON(ct vcard.Contact) vcard.Contact {
	for _, fs := range []*[]vcard.Field{&ct.Emails, &ct.Phones, &ct.URLs, &ct.IMs, &ct.Socials, &ct.Dates, &ct.Related} {
		if *fs == nil {
			*fs = []vcard.Field{}
		}
	}
	if ct.Addresses == nil {
		ct.Addresses = []vcard.Address{}
	}
	if ct.Categories == nil {
		ct.Categories = []string{}
	}
	return ct
}

func (s *Server) contactView(ctx context.Context, r *http.Request, uid int64, e *store.ContactRow, role string, ab *store.AddressBook) (*ContactView, error) {
	card, err := vcard.ParseOne([]byte(e.VCF))
	if err != nil {
		return nil, err
	}
	v := &ContactView{Summary: s.summary(e, s.store.IsStarred(ctx, uid, e.ID)), Card: cardJSON(card.Contact()), ETag: e.ETag,
		Role: role, Source: e.Source, URL: s.contactURL(r, e.ID), CreatedAt: e.CreatedAt}
	v.AddressBook = bookRef{ID: ab.ID, Name: ab.Name, Color: ab.Color}
	if o, err := s.store.GetUser(ctx, ab.UserID); err == nil {
		v.AddressBook.Owner = &store.Person{ID: o.ID, Name: o.Name, Email: o.Email}
	}
	v.Groups, _ = s.store.GroupsWithMember(ctx, e.AddressBookID, e.UID)
	if v.Groups == nil {
		v.Groups = []store.GroupRef{}
	}
	return v, nil
}

// respondContact answers with the contact as the person sees it now.
func (s *Server) respondContact(w http.ResponseWriter, r *http.Request, status int, id int64) {
	ctx := r.Context()
	e, role, ab, err := s.loadContact(ctx, me(r), id)
	if err != nil {
		s.fail(w, err, "contact")
		return
	}
	v, err := s.contactView(ctx, r, me(r), e, role, ab)
	if err != nil {
		s.fail(w, err, "contact")
		return
	}
	writeJSON(w, status, v)
}

// ---------- Core events and the log ----------

// contactHook queues the Core event in the same transaction as the change
// (payload {id, uid, fn, addressbook_id, source}, user_email of the book
// owner). Imports publish nothing per contact (see import.go).
func (s *Server) contactHook(ctx context.Context, kind string, ab *store.AddressBook, id int64, uid, fn, source string) store.Hook {
	if !s.core.Configured() || ab == nil || source == "import" {
		return nil
	}
	owner, err := s.store.GetUser(ctx, ab.UserID)
	return func(tx *sql.Tx) error {
		if err != nil {
			return err
		}
		if kind != "deleted" {
			_ = tx.QueryRowContext(ctx, `SELECT id, fn FROM contacts WHERE addressbook_id = ? AND uid = ?`, ab.ID, uid).Scan(&id, &fn)
		}
		return core.QueueEvent(ctx, tx, "contacts."+kind, "contact:"+strconv.FormatInt(ab.ID, 10)+":"+uid, map[string]any{
			"id": id, "uid": uid, "fn": fn, "addressbook_id": ab.ID, "source": source, "user_email": owner.Email,
		})
	}
}

// logContact writes the change into the actor's log, and the owner's when
// someone else changed a contact in their book.
func (s *Server) logContact(ctx context.Context, a actor, ab *store.AddressBook, id int64, action, summary, detail string) {
	abID, cid := ab.ID, id
	s.store.AddActivity(ctx, a.u.ID, &abID, &cid, action, summary, detail)
	if ab.UserID != a.u.ID {
		by := "by " + firstNonEmpty(a.u.Name, a.u.Email)
		if detail != "" {
			by = detail + " · " + by
		}
		s.store.AddActivity(ctx, ab.UserID, &abID, &cid, action, summary, by)
	}
}

// changedSections names what an edit touched, for the log.
func changedSections(prev, next vcard.Contact) string {
	next.Clean()
	var out []string
	add := func(changed bool, name string) {
		if changed {
			out = append(out, name)
		}
	}
	same := func(a, b any) bool { return reflect.DeepEqual(a, b) }
	add(prev.FN != next.FN || prev.Given != next.Given || prev.Family != next.Family || prev.Middle != next.Middle ||
		prev.Prefix != next.Prefix || prev.Suffix != next.Suffix || prev.Nickname != next.Nickname, "name")
	add(prev.Org != next.Org || prev.Department != next.Department || prev.Title != next.Title, "company")
	add(!same(prev.Emails, next.Emails), "emails")
	add(!same(prev.Phones, next.Phones), "phones")
	add(!same(prev.Addresses, next.Addresses), "addresses")
	add(!same(prev.URLs, next.URLs) || !same(prev.IMs, next.IMs) || !same(prev.Socials, next.Socials), "links")
	add(prev.Birthday != next.Birthday || prev.Anniversary != next.Anniversary || !same(prev.Dates, next.Dates), "dates")
	add(!same(prev.Related, next.Related), "related people")
	add(prev.Note != next.Note, "note")
	add(!same(prev.Categories, next.Categories), "labels")
	return strings.Join(out, ", ")
}

// ---------- writes ----------

// newUID is a random UUID in upper case, like iOS makes them.
func newUID() string { return strings.ToUpper(uuid.NewString()) }

// createCard stores a new card in a book.
func (s *Server) createCard(ctx context.Context, a actor, ab *store.AddressBook, card *vcard.Card, wantID ...int64) (*store.ContactRow, error) {
	fn := card.Contact().DisplayName()
	w := store.ContactWrite{AddressBookID: ab.ID, VCF: card.String(), Source: a.source, Actor: a.u.ID, IfNoneMatch: true}
	if len(wantID) > 0 {
		w.WantID = wantID[0]
	}
	e, _, err := s.store.SaveContact(ctx, w,
		s.contactHook(ctx, "created", ab, 0, card.UID(), fn, a.source))
	if err != nil {
		return nil, err
	}
	s.touch()
	return e, nil
}

// saveCard writes an edited card over a stored contact (ifMatch "" = no check).
func (s *Server) saveCard(ctx context.Context, a actor, e *store.ContactRow, ab *store.AddressBook, card *vcard.Card, ifMatch string) (*store.ContactRow, error) {
	saved, _, err := s.store.SaveContact(ctx, store.ContactWrite{AddressBookID: e.AddressBookID, Href: e.Href, VCF: card.String(), Source: a.source, Actor: a.u.ID, IfMatch: ifMatch},
		s.contactHook(ctx, "updated", ab, e.ID, e.UID, e.FN, a.source))
	if err != nil {
		return nil, err
	}
	s.touch()
	return saved, nil
}

// editCard applies a change to the structured view of a stored contact and
// saves it when anything changed (false when nothing did).
func (s *Server) editCard(ctx context.Context, a actor, e *store.ContactRow, ab *store.AddressBook, ifMatch string, edit func(*vcard.Contact)) (*store.ContactRow, bool, error) {
	card, err := vcard.ParseOne([]byte(e.VCF))
	if err != nil {
		return nil, false, err
	}
	prev := card.Contact()
	prev.Clean()
	next := prev
	next.Categories = append([]string(nil), prev.Categories...)
	edit(&next)
	next.Clean()
	if reflect.DeepEqual(prev, next) {
		return e, false, nil
	}
	card.Apply(prev, next)
	saved, err := s.saveCard(ctx, a, e, ab, card, ifMatch)
	return saved, err == nil, err
}

// deleteContact moves a contact to Recently deleted.
func (s *Server) deleteContact(ctx context.Context, a actor, id int64) error {
	e, ab, err := s.writableContact(ctx, a, id)
	if err != nil {
		return err
	}
	if err := s.store.DeleteContact(ctx, e, store.DeleteContactOpts{Actor: a.u.ID, KeepCopy: true},
		s.contactHook(ctx, "deleted", ab, e.ID, e.UID, e.FN, a.source)); err != nil {
		return err
	}
	s.touch()
	s.logContact(ctx, a, ab, e.ID, "deleted", e.FN, ab.Name)
	return nil
}

// moveContact puts a contact into another book (write on both).
func (s *Server) moveContact(ctx context.Context, a actor, e *store.ContactRow, from *store.AddressBook, to int64) (*store.ContactRow, error) {
	if to == e.AddressBookID {
		return e, nil
	}
	target, err := s.writableBook(ctx, a.u, to)
	if err != nil {
		return nil, err
	}
	if _, err := s.store.GetContactByUID(ctx, target.ID, e.UID); err == nil {
		return nil, badRequest(target.Name + " already has this contact")
	}
	stars := s.store.StarredBy(ctx, e.ID)
	moved, _, err := s.store.SaveContact(ctx, store.ContactWrite{AddressBookID: target.ID, VCF: e.VCF, Source: e.Source, Actor: a.u.ID, IfNoneMatch: true},
		s.contactHook(ctx, "created", target, 0, e.UID, e.FN, a.source))
	if err != nil {
		return nil, err
	}
	if err := s.store.DeleteContact(ctx, e, store.DeleteContactOpts{Actor: a.u.ID}, s.contactHook(ctx, "deleted", from, e.ID, e.UID, e.FN, a.source)); err != nil {
		return nil, err
	}
	for _, uid := range stars {
		_ = s.store.SetStarred(ctx, uid, moved.ID, true)
	}
	s.touch()
	s.logContact(ctx, a, target, moved.ID, "moved", e.FN, from.Name+" → "+target.Name)
	return moved, nil
}

// ---------- handlers ----------

// bookScope is the books a list or search covers: one (any the person may
// read) or every visible one.
func (s *Server) bookScope(ctx context.Context, uid, book int64) ([]int64, error) {
	if book > 0 {
		if _, _, err := s.store.BookRole(ctx, uid, book); err != nil {
			return nil, err
		}
		return []int64{book}, nil
	}
	return s.store.VisibleBookIDs(ctx, uid)
}

func (s *Server) handleListContacts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	qv := r.URL.Query()
	books, err := s.bookScope(ctx, u.ID, queryInt64(r, "book"))
	if err != nil {
		s.fail(w, err, "address book")
		return
	}
	q := store.Query{BookIDs: books, UserID: u.ID, Text: qv.Get("q"), Category: qv.Get("category"), Starred: queryBool(r, "starred"),
		Kind: qv.Get("kind"), Has: qv.Get("has"), SortBy: qv.Get("sort"), Recent: queryBool(r, "recent"),
		Limit: queryInt(r, "limit"), Offset: queryInt(r, "offset")}
	if q.SortBy != "given" && q.SortBy != "family" {
		st, _ := s.store.GetSettings(ctx, u.ID)
		q.SortBy = st.SortBy
	}
	rows, total, err := s.store.FindContacts(ctx, q)
	if err != nil {
		s.fail(w, err, "list contacts")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"contacts": s.summaries(ctx, u.ID, rows), "total": total})
}

func (s *Server) handleGetContact(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	e, role, ab, err := s.loadContact(ctx, u.ID, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "contact")
		return
	}
	s.store.MarkViewed(ctx, u.ID, e.ID)
	v, err := s.contactView(ctx, r, u.ID, e, role, ab)
	if err != nil {
		s.fail(w, err, "contact")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleCreateContact(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a := s.actorOf(r)
	var in struct {
		AddressBookID int64         `json:"addressbook_id"`
		Card          vcard.Contact `json:"card"`
		Starred       bool          `json:"starred"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	ab, err := s.targetBook(ctx, a.u, in.AddressBookID)
	if err != nil {
		s.fail(w, err, "address book")
		return
	}
	ct := in.Card
	ct.Clean()
	if ct.DisplayName() == "" {
		writeErr(w, http.StatusBadRequest, "a contact needs a name, a company, an email or a phone number")
		return
	}
	e, err := s.createCard(ctx, a, ab, vcard.New(newUID(), ct))
	if err != nil {
		s.fail(w, err, "create contact")
		return
	}
	if in.Starred {
		_ = s.store.SetStarred(ctx, a.u.ID, e.ID, true)
	}
	s.logContact(ctx, a, ab, e.ID, "created", e.FN, ab.Name)
	s.respondContact(w, r, http.StatusCreated, e.ID)
}

func (s *Server) handlePutContact(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a := s.actorOf(r)
	var in struct {
		Card vcard.Contact `json:"card"`
		ETag string        `json:"etag"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	e, ab, err := s.writableContact(ctx, a, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "contact")
		return
	}
	ifMatch := firstNonEmpty(in.ETag, r.Header.Get("If-Match"))
	if ifMatch != "" && ifMatch != e.ETag {
		s.fail(w, store.ErrPrecondition, "contact")
		return
	}
	card, err := vcard.ParseOne([]byte(e.VCF))
	if err != nil {
		s.fail(w, err, "contact")
		return
	}
	prev := card.Contact()
	prev.Clean()
	next := in.Card
	if next.Kind == "" {
		next.Kind = prev.Kind
	}
	if next.Members == nil && prev.Kind == "group" {
		next.Members = prev.Members
	}
	probe := next
	probe.Clean()
	if probe.DisplayName() == "" {
		writeErr(w, http.StatusBadRequest, "a contact needs a name, a company, an email or a phone number")
		return
	}
	changed := changedSections(prev, next)
	card.Apply(prev, next)
	saved, err := s.saveCard(ctx, a, e, ab, card, firstNonEmpty(ifMatch, e.ETag))
	if err != nil {
		s.fail(w, err, "save contact")
		return
	}
	if changed != "" {
		s.logContact(ctx, a, ab, saved.ID, "updated", saved.FN, changed)
	}
	s.respondContact(w, r, http.StatusOK, saved.ID)
}

type patchIn struct {
	Starred          *bool    `json:"starred"`
	AddCategories    []string `json:"add_categories"`
	RemoveCategories []string `json:"remove_categories"`
	AddressBookID    *int64   `json:"addressbook_id"`
}

// patchContact makes the small changes of PATCH and the bulk actions.
func (s *Server) patchContact(ctx context.Context, a actor, id int64, in patchIn) (*store.ContactRow, error) {
	e, _, _, err := s.loadContact(ctx, a.u.ID, id)
	if err != nil {
		return nil, err
	}
	if in.Starred != nil {
		// a star is the person's own mark: reading is enough
		if err := s.store.SetStarred(ctx, a.u.ID, e.ID, *in.Starred); err != nil {
			return nil, err
		}
		s.touch()
	}
	if len(in.AddCategories) == 0 && len(in.RemoveCategories) == 0 && in.AddressBookID == nil {
		return e, nil
	}
	e, ab, err := s.writableContact(ctx, a, id)
	if err != nil {
		return nil, err
	}
	if len(in.AddCategories) > 0 || len(in.RemoveCategories) > 0 {
		var changed bool
		e, changed, err = s.editCard(ctx, a, e, ab, e.ETag, func(ct *vcard.Contact) {
			ct.Categories = editLabels(ct.Categories, in.AddCategories, in.RemoveCategories)
		})
		if err != nil {
			return nil, err
		}
		if changed {
			s.logContact(ctx, a, ab, e.ID, "updated", e.FN, "labels")
		}
	}
	if in.AddressBookID != nil {
		return s.moveContact(ctx, a, e, ab, *in.AddressBookID)
	}
	return e, nil
}

// editLabels adds and removes labels (case-insensitive), keeping the order.
func editLabels(have, add, remove []string) []string {
	drop := map[string]bool{}
	for _, l := range remove {
		drop[strings.ToLower(strings.TrimSpace(l))] = true
	}
	out := []string{}
	seen := map[string]bool{}
	for _, l := range append(append([]string(nil), have...), add...) {
		l = strings.TrimSpace(l)
		k := strings.ToLower(l)
		if l == "" || drop[k] || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, l)
	}
	return out
}

func (s *Server) handlePatchContact(w http.ResponseWriter, r *http.Request) {
	var in patchIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	e, err := s.patchContact(r.Context(), s.actorOf(r), idParam(r, "id"), in)
	if err != nil {
		s.fail(w, err, "contact")
		return
	}
	s.respondContact(w, r, http.StatusOK, e.ID)
}

func (s *Server) handleDeleteContact(w http.ResponseWriter, r *http.Request) {
	if err := s.deleteContact(r.Context(), s.actorOf(r), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete contact")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// errText is an error in the plain words a bulk answer lists.
func errText(err error) string {
	var fb forbidden
	var ue userError
	switch {
	case errors.Is(err, store.ErrNotFound):
		return "not found"
	case errors.As(err, &fb):
		return fb.msg
	case errors.As(err, &ue):
		return ue.msg
	case errors.Is(err, store.ErrPrecondition):
		return "the contact changed meanwhile"
	}
	return err.Error()
}

func (s *Server) handleBulk(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a := s.actorOf(r)
	var in struct {
		IDs           []int64 `json:"ids"`
		Action        string  `json:"action"`
		Category      string  `json:"category"`
		AddressBookID int64   `json:"addressbook_id"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	var op func(id int64) error
	t, f := true, false
	patch := func(p patchIn) func(int64) error {
		return func(id int64) error { _, err := s.patchContact(ctx, a, id, p); return err }
	}
	switch in.Action {
	case "delete":
		op = func(id int64) error { return s.deleteContact(ctx, a, id) }
	case "star":
		op = patch(patchIn{Starred: &t})
	case "unstar":
		op = patch(patchIn{Starred: &f})
	case "add_category", "remove_category":
		if strings.TrimSpace(in.Category) == "" {
			writeErr(w, http.StatusBadRequest, "which label?")
			return
		}
		if in.Action == "add_category" {
			op = patch(patchIn{AddCategories: []string{in.Category}})
		} else {
			op = patch(patchIn{RemoveCategories: []string{in.Category}})
		}
	case "move":
		if in.AddressBookID == 0 {
			writeErr(w, http.StatusBadRequest, "move to which address book?")
			return
		}
		op = patch(patchIn{AddressBookID: &in.AddressBookID})
	default:
		writeErr(w, http.StatusBadRequest, "action must be delete, star, unstar, add_category, remove_category or move")
		return
	}
	done := 0
	failed := []map[string]any{}
	for _, id := range in.IDs {
		if err := op(id); err != nil {
			failed = append(failed, map[string]any{"id": id, "error": errText(err)})
			continue
		}
		done++
	}
	writeJSON(w, http.StatusOK, map[string]any{"done": done, "failed": failed})
}

func (s *Server) handleContactVCF(w http.ResponseWriter, r *http.Request) {
	e, _, _, err := s.loadContact(r.Context(), me(r), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "contact")
		return
	}
	w.Header().Set("Content-Type", "text/vcard; charset=utf-8")
	w.Header().Set("ETag", e.ETag)
	if queryBool(r, "download") {
		w.Header().Set("Content-Disposition", `attachment; filename="`+fileName(e.FN, "contact")+`.vcf"`)
	}
	_, _ = w.Write([]byte(e.VCF))
}

// fileName is a safe download name for a person or a book.
func fileName(name, def string) string {
	n := store.Slug(vcard.Fold(name))
	if n == "" {
		return def
	}
	return n
}

// writeVCF sends contacts as one .vcf file.
func writeVCF(w http.ResponseWriter, name string, rows []*store.ContactRow) {
	w.Header().Set("Content-Type", "text/vcard; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.vcf"`)
	var b strings.Builder
	for _, e := range rows {
		v := e.VCF
		if !strings.HasSuffix(v, "\n") {
			v += "\r\n"
		}
		b.WriteString(v)
	}
	_, _ = w.Write([]byte(b.String()))
}

// handleExport is the person's visible contacts (or the given ones, or one book) as one .vcf.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	books, err := s.bookScope(ctx, u.ID, queryInt64(r, "book"))
	if err != nil {
		s.fail(w, err, "address book")
		return
	}
	var rows []*store.ContactRow
	if ids := r.URL.Query().Get("ids"); ids != "" {
		var list []int64
		for _, p := range strings.Split(ids, ",") {
			if n, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64); err == nil {
				list = append(list, n)
			}
		}
		readable, _ := s.store.ReadableBookIDs(ctx, u.ID)
		rows, err = s.store.ContactsByIDs(ctx, readable, list)
	} else {
		rows, err = s.store.ContactsInBooks(ctx, books, true)
	}
	if err != nil {
		s.fail(w, err, "export")
		return
	}
	writeVCF(w, "contacts", rows)
}

// ---------- recently deleted and the log ----------

func (s *Server) handleListDeleted(w http.ResponseWriter, r *http.Request) {
	d, err := s.store.ListDeleted(r.Context(), me(r))
	if err != nil {
		s.fail(w, err, "recently deleted")
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// handleRestoreDeleted puts a deleted contact back into its book (or the
// person's default book when they can no longer write there) under the id it
// had, so links to it work again; a UID taken meanwhile gets a new one.
func (s *Server) handleRestoreDeleted(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a := s.actorOf(r)
	d, err := s.store.GetDeleted(ctx, a.u.ID, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "restore")
		return
	}
	ab, err := s.writableBook(ctx, a.u, d.AddressBookID)
	if err != nil {
		if ab, err = s.defaultBook(ctx, a.u); err != nil {
			s.fail(w, err, "restore")
			return
		}
	}
	card, err := vcard.ParseOne([]byte(d.VCF))
	if err != nil {
		s.fail(w, err, "restore")
		return
	}
	if _, err := s.store.GetContactByUID(ctx, ab.ID, card.UID()); err == nil || card.UID() == "" {
		card.Set("UID", newUID())
	}
	e, err := s.createCard(ctx, a, ab, card, d.ContactID)
	if err != nil {
		s.fail(w, err, "restore")
		return
	}
	_ = s.store.ForgetDeleted(ctx, a.u.ID, d.ID)
	s.logContact(ctx, a, ab, e.ID, "restored", e.FN, ab.Name)
	s.respondContact(w, r, http.StatusOK, e.ID)
}

func (s *Server) handleForgetDeleted(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ForgetDeleted(r.Context(), me(r), idParam(r, "id")); err != nil {
		s.fail(w, err, "forget")
		return
	}
	s.touch()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.ListActivity(r.Context(), me(r), 0, 200)
	if err != nil {
		s.fail(w, err, "activity")
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleContactActivity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	e, _, _, err := s.loadContact(ctx, me(r), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "contact")
		return
	}
	a, err := s.store.ListActivity(ctx, me(r), e.ID, 100)
	if err != nil {
		s.fail(w, err, "activity")
		return
	}
	writeJSON(w, http.StatusOK, a)
}
