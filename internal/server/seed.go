package server

import (
	"context"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/store"
)

// seedUser gives a new person their own address book.
func (s *Server) seedUser(ctx context.Context, u *store.User) {
	s.store.BindPendingShares(ctx, u.ID, u.Email)
	if books, err := s.store.OwnAddressBooks(ctx, u.ID); err == nil && len(books) > 0 {
		return
	}
	ab := &store.AddressBook{UserID: u.ID, UID: "contacts", Name: "Contacts", Color: "#e11d48", IsDefault: true}
	if err := s.store.CreateAddressBook(ctx, ab); err != nil {
		s.log.Warn("seed address book", "user", u.Email, "err", err)
	}
}
