package store

import (
	"context"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/vcard"
)

// GroupRef names an Apple group card.
type GroupRef struct {
	UID  string `json:"uid"`
	Name string `json:"name"`
}

// GroupsWithMember lists the group cards of an address book that hold uid.
func (s *Store) GroupsWithMember(ctx context.Context, abID int64, uid string) ([]GroupRef, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT e.uid, e.fn FROM contacts e, json_each(e.members) m
		WHERE e.addressbook_id = ? AND e.kind = 'group' AND e.members <> '' AND m.value = ? ORDER BY e.fn`, abID, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GroupRef{}
	for rows.Next() {
		var g GroupRef
		if err := rows.Scan(&g.UID, &g.Name); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ContactsInBooks returns every contact of the given books (group cards
// only when groups is true), ordered by id.
func (s *Store) ContactsInBooks(ctx context.Context, abIDs []int64, groups bool) ([]*ContactRow, error) {
	if len(abIDs) == 0 {
		return []*ContactRow{}, nil
	}
	ph, args := inList(abIDs)
	q := `SELECT ` + contactCols + ` FROM contacts e WHERE e.addressbook_id IN (` + ph + `)`
	if !groups {
		q += ` AND e.kind <> 'group'`
	}
	return scanContacts(s.DB.QueryContext(ctx, q+` ORDER BY e.id`, args...))
}

// LookupEmail returns the contacts (in the given books) that have exactly this email.
func (s *Store) LookupEmail(ctx context.Context, abIDs []int64, email string) ([]*ContactRow, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if len(abIDs) == 0 || email == "" || strings.ContainsAny(email, " ") {
		return []*ContactRow{}, nil
	}
	ph, args := inList(abIDs)
	args = append(args, "% "+likeEscape(email)+" %")
	return scanContacts(s.DB.QueryContext(ctx, `SELECT `+contactCols+` FROM contacts e WHERE e.addressbook_id IN (`+ph+`)
		AND e.kind <> 'group' AND (' ' || e.emails || ' ') LIKE ? ESCAPE '\' ORDER BY e.sort_given, e.id`, args...))
}

// LookupPhone returns the contacts (in the given books) with a number whose
// last 8 digits are those of phone.
func (s *Store) LookupPhone(ctx context.Context, abIDs []int64, phone string) ([]*ContactRow, error) {
	key := vcard.PhoneKey(phone)
	if len(abIDs) == 0 || len(key) < 6 {
		return []*ContactRow{}, nil
	}
	ph, args := inList(abIDs)
	args = append(args, "%"+likeEscape(key)+"%")
	rows, err := scanContacts(s.DB.QueryContext(ctx, `SELECT `+contactCols+` FROM contacts e WHERE e.addressbook_id IN (`+ph+`)
		AND e.kind <> 'group' AND e.phones LIKE ? ESCAPE '\' ORDER BY e.sort_given, e.id`, args...))
	if err != nil {
		return nil, err
	}
	out := []*ContactRow{}
	for _, e := range rows {
		for _, p := range strings.Fields(e.Phones) {
			if vcard.PhoneKey(p) == key {
				out = append(out, e)
				break
			}
		}
	}
	return out, nil
}

// IsStarred reports whether a person starred a contact.
func (s *Store) IsStarred(ctx context.Context, userID, contactID int64) bool {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT starred FROM contact_prefs WHERE user_id = ? AND contact_id = ?`, userID, contactID).Scan(&n)
	return n == 1
}

// StarredBy lists the people who starred a contact (to carry the stars over a move or a merge).
func (s *Store) StarredBy(ctx context.Context, contactID int64) []int64 {
	rows, err := s.DB.QueryContext(ctx, `SELECT user_id FROM contact_prefs WHERE contact_id = ? AND starred = 1`, contactID)
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

// ContactCount is how many contacts the whole box holds (demo data goes only into an empty box).
func (s *Store) ContactCount(ctx context.Context) int {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM contacts`).Scan(&n)
	return n
}
