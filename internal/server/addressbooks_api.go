package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/store"
)

// bookColors are the colours new address books get in turn.
var bookColors = []string{"#e11d48", "#0284c7", "#059669", "#d97706", "#9333ea", "#db2777", "#0d9488", "#ea580c", "#4f46e5", "#65a30d"}

func isHexColor(c string) bool {
	if len(c) != 7 || c[0] != '#' {
		return false
	}
	for _, r := range strings.ToLower(c[1:]) {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// booksFor lists a person's books as they see them: shares for those they
// manage, and is_default meaning "where my new contacts go".
func (s *Server) booksFor(ctx context.Context, u *store.User) ([]*store.AddressBook, error) {
	books, err := s.store.ListAddressBooksFor(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	if len(books) == 0 {
		s.seedUser(ctx, u)
		if books, err = s.store.ListAddressBooksFor(ctx, u.ID); err != nil {
			return nil, err
		}
	}
	var def int64
	if ab, err := s.defaultBook(ctx, u); err == nil {
		def = ab.ID
	}
	for _, b := range books {
		b.IsDefault = b.ID == def
		if store.RoleAtLeast(b.Role, store.RoleManage) {
			if sh, err := s.store.ListShares(ctx, b.ID); err == nil {
				b.Shares = sh
			}
		}
		if b.Shares == nil {
			b.Shares = []store.Share{}
		}
	}
	if books == nil {
		books = []*store.AddressBook{}
	}
	return books, nil
}

func (s *Server) respondBook(w http.ResponseWriter, r *http.Request, status int, id int64) {
	books, err := s.booksFor(r.Context(), userFrom(r.Context()))
	if err != nil {
		s.fail(w, err, "address books")
		return
	}
	for _, b := range books {
		if b.ID == id {
			writeJSON(w, status, b)
			return
		}
	}
	writeErr(w, http.StatusNotFound, "not found")
}

func (s *Server) handleListBooks(w http.ResponseWriter, r *http.Request) {
	books, err := s.booksFor(r.Context(), userFrom(r.Context()))
	if err != nil {
		s.fail(w, err, "address books")
		return
	}
	writeJSON(w, http.StatusOK, books)
}

type bookIn struct {
	Name        *string  `json:"name"`
	Color       *string  `json:"color"`
	Description *string  `json:"description"`
	Visible     *bool    `json:"visible"`
	SortOrder   *float64 `json:"sort_order"`
	IsDefault   *bool    `json:"is_default"`
}

func (s *Server) handleCreateBook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var in bookIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Name == nil || strings.TrimSpace(*in.Name) == "" {
		writeErr(w, http.StatusBadRequest, "an address book needs a name")
		return
	}
	own, _ := s.store.OwnAddressBooks(ctx, u.ID)
	ab := &store.AddressBook{UserID: u.ID, Name: strings.TrimSpace(*in.Name), Color: bookColors[len(own)%len(bookColors)]}
	if in.Color != nil && *in.Color != "" {
		if !isHexColor(*in.Color) {
			writeErr(w, http.StatusBadRequest, "colors look like #e11d48")
			return
		}
		ab.Color = *in.Color
	}
	if in.Description != nil {
		ab.Description = *in.Description
	}
	if err := s.store.CreateAddressBook(ctx, ab); err != nil {
		s.fail(w, err, "create address book")
		return
	}
	if in.IsDefault != nil && *in.IsDefault {
		_ = s.store.SetDefaultAddressBook(ctx, u.ID, ab.ID)
		_ = s.store.SetUserSetting(ctx, u.ID, "default_addressbook_id", strconv.FormatInt(ab.ID, 10))
	}
	abID := ab.ID
	s.store.AddActivity(ctx, u.ID, &abID, nil, "addressbook_created", ab.Name, "")
	s.touch()
	s.respondBook(w, r, http.StatusCreated, ab.ID)
}

func (s *Server) handleUpdateBook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	role, ab, err := s.store.BookRole(ctx, u.ID, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "address book")
		return
	}
	var in bookIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	// the person's own view: any role
	if in.Visible != nil || in.SortOrder != nil {
		if err := s.store.SavePrefs(ctx, u.ID, ab.ID, in.Visible, in.SortOrder); err != nil {
			s.fail(w, err, "save")
			return
		}
	}
	if in.Name != nil || in.Color != nil || in.Description != nil {
		if !store.RoleAtLeast(role, store.RoleManage) {
			writeErr(w, http.StatusForbidden, "only the owner can change this address book")
			return
		}
		if in.Name != nil {
			if strings.TrimSpace(*in.Name) == "" {
				writeErr(w, http.StatusBadRequest, "an address book needs a name")
				return
			}
			ab.Name = strings.TrimSpace(*in.Name)
		}
		if in.Color != nil {
			if *in.Color != "" && !isHexColor(*in.Color) {
				writeErr(w, http.StatusBadRequest, "colors look like #e11d48")
				return
			}
			ab.Color = *in.Color
		}
		if in.Description != nil {
			ab.Description = *in.Description
		}
		if err := s.store.UpdateAddressBook(ctx, ab); err != nil {
			s.fail(w, err, "update address book")
			return
		}
	}
	if in.IsDefault != nil && *in.IsDefault {
		if err := canWrite(role, ab); err != nil {
			s.fail(w, err, "address book")
			return
		}
		if role == store.RoleOwner {
			_ = s.store.SetDefaultAddressBook(ctx, u.ID, ab.ID)
		}
		_ = s.store.SetUserSetting(ctx, u.ID, "default_addressbook_id", strconv.FormatInt(ab.ID, 10))
	}
	s.touch()
	s.respondBook(w, r, http.StatusOK, ab.ID)
}

func (s *Server) handleDeleteBook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	role, ab, err := s.store.BookRole(ctx, u.ID, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "address book")
		return
	}
	if role != store.RoleOwner {
		writeErr(w, http.StatusForbidden, "only the owner can delete an address book — leave it instead")
		return
	}
	if own, _ := s.store.OwnAddressBooks(ctx, u.ID); len(own) <= 1 {
		writeErr(w, http.StatusBadRequest, "you need at least one address book")
		return
	}
	if err := s.store.DeleteAddressBook(ctx, ab.ID); err != nil {
		s.fail(w, err, "delete address book")
		return
	}
	if s.store.UserSetting(ctx, u.ID, "default_addressbook_id") == strconv.FormatInt(ab.ID, 10) {
		_ = s.store.SetUserSetting(ctx, u.ID, "default_addressbook_id", "0")
	}
	s.store.AddActivity(ctx, u.ID, nil, nil, "addressbook_deleted", ab.Name, "")
	s.touch()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLeaveBook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	role, ab, err := s.store.BookRole(ctx, u.ID, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "address book")
		return
	}
	if role == store.RoleOwner {
		writeErr(w, http.StatusBadRequest, "you own this address book")
		return
	}
	if err := s.store.LeaveAddressBook(ctx, u.ID, ab.ID); err != nil {
		s.fail(w, err, "leave")
		return
	}
	s.touch()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleExportBook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, ab, err := s.store.BookRole(ctx, me(r), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "address book")
		return
	}
	rows, err := s.store.ListContacts(ctx, ab.ID)
	if err != nil {
		s.fail(w, err, "export")
		return
	}
	writeVCF(w, fileName(ab.Name, "contacts"), rows)
}

// ---------- sharing ----------

func roleText(r string) string {
	return map[string]string{store.RoleRead: "can see the contacts", store.RoleWrite: "can add and change contacts",
		store.RoleManage: "can change contacts and sharing"}[r]
}

// manageBook loads a book the person may share.
func (s *Server) manageBook(ctx context.Context, uid, id int64) (string, *store.AddressBook, error) {
	role, ab, err := s.store.BookRole(ctx, uid, id)
	if err != nil {
		return "", nil, err
	}
	if !store.RoleAtLeast(role, store.RoleManage) {
		return "", nil, errForbidden("only the owner can see and change who " + ab.Name + " is shared with")
	}
	return role, ab, nil
}

func (s *Server) handleListShares(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, ab, err := s.manageBook(ctx, me(r), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "address book")
		return
	}
	sh, err := s.store.ListShares(ctx, ab.ID)
	if err != nil {
		s.fail(w, err, "shares")
		return
	}
	writeJSON(w, http.StatusOK, sh)
}

func (s *Server) handleCreateShare(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	role, ab, err := s.manageBook(ctx, u.ID, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "address book")
		return
	}
	var in struct {
		UserID   *int64 `json:"user_id"`
		Email    string `json:"email"`
		Everyone bool   `json:"everyone"`
		Role     string `json:"role"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Role == "" {
		in.Role = store.RoleRead
	}
	if !store.ValidRole(in.Role) {
		writeErr(w, http.StatusBadRequest, "role must be read, write or manage")
		return
	}
	if role == store.RoleManage && in.Role == store.RoleManage {
		writeErr(w, http.StatusForbidden, "only the owner can hand out manage rights")
		return
	}
	sh := &store.Share{AddressBookID: ab.ID, Role: in.Role, Everyone: in.Everyone}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	switch {
	case in.Everyone:
	case in.UserID != nil:
		p, err := s.store.GetUser(ctx, *in.UserID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "no such person on this box")
			return
		}
		sh.UserID = &p.ID
		email = p.Email
	case email != "" && strings.Contains(email, "@"):
		if id := s.store.UserIDByEmail(ctx, email); id > 0 {
			sh.UserID = &id
		} else {
			sh.Email = email
		}
	default:
		writeErr(w, http.StatusBadRequest, "share with a person, an email or everyone")
		return
	}
	if sh.UserID != nil && *sh.UserID == ab.UserID {
		writeErr(w, http.StatusBadRequest, "that's the owner")
		return
	}
	if err := s.store.UpsertShare(ctx, sh, u.ID); err != nil {
		s.fail(w, err, "share")
		return
	}
	who := firstNonEmpty(u.Name, u.Email)
	if sh.UserID != nil {
		t := true
		_ = s.store.SavePrefs(ctx, *sh.UserID, ab.ID, &t, nil)
		s.notifyUser(ctx, *sh.UserID, "shares", who+" shared an address book with you", ab.Name+" — you "+roleText(in.Role), s.cfg.BaseURL)
	}
	abID := ab.ID
	target := email
	if in.Everyone {
		target = "everyone"
	}
	s.store.AddActivity(ctx, ab.UserID, &abID, nil, "shared", ab.Name, target+" · "+in.Role)
	s.touch()
	list, _ := s.store.ListShares(ctx, ab.ID)
	writeJSON(w, http.StatusOK, list)
}

// shareForManager loads a share whose book the person manages.
func (s *Server) shareForManager(r *http.Request) (*store.Share, *store.AddressBook, string, error) {
	ctx := r.Context()
	sh, err := s.store.GetShare(ctx, idParam(r, "sid"))
	if err != nil {
		return nil, nil, "", err
	}
	role, ab, err := s.manageBook(ctx, me(r), sh.AddressBookID)
	if err != nil {
		return nil, nil, "", err
	}
	return sh, ab, role, nil
}

func (s *Server) handleUpdateShare(w http.ResponseWriter, r *http.Request) {
	sh, ab, role, err := s.shareForManager(r)
	if err != nil {
		s.fail(w, err, "share")
		return
	}
	var in struct {
		Role string `json:"role"`
	}
	if err := decode(r, &in); err != nil || !store.ValidRole(in.Role) {
		writeErr(w, http.StatusBadRequest, "role must be read, write or manage")
		return
	}
	if role == store.RoleManage && in.Role == store.RoleManage {
		writeErr(w, http.StatusForbidden, "only the owner can hand out manage rights")
		return
	}
	sh.Role = in.Role
	if err := s.store.UpsertShare(r.Context(), sh, me(r)); err != nil {
		s.fail(w, err, "share")
		return
	}
	s.touch()
	list, _ := s.store.ListShares(r.Context(), ab.ID)
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleDeleteShare(w http.ResponseWriter, r *http.Request) {
	sh, ab, _, err := s.shareForManager(r)
	if err != nil {
		s.fail(w, err, "share")
		return
	}
	if err := s.store.DeleteShare(r.Context(), sh.ID); err != nil {
		s.fail(w, err, "share")
		return
	}
	s.touch()
	list, _ := s.store.ListShares(r.Context(), ab.ID)
	writeJSON(w, http.StatusOK, list)
}

// handlePeople lists who is on the box (name and email) for the share picker.
func (s *Server) handlePeople(w http.ResponseWriter, r *http.Request) {
	people, err := s.store.People(r.Context())
	if err != nil {
		s.fail(w, err, "people")
		return
	}
	writeJSON(w, http.StatusOK, people)
}
