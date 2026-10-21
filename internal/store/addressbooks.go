package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Roles, weakest first. The owner can do everything.
const (
	RoleRead   = "read"
	RoleWrite  = "write"
	RoleManage = "manage"
	RoleOwner  = "owner"
)

var roleRank = map[string]int{RoleRead: 2, RoleWrite: 3, RoleManage: 4, RoleOwner: 5}

// RoleAtLeast reports whether have grants want.
func RoleAtLeast(have, want string) bool { return roleRank[have] >= roleRank[want] }

// ValidRole reports whether r can be granted through a share.
func ValidRole(r string) bool {
	return r == RoleRead || r == RoleWrite || r == RoleManage
}

const abCols = `c.id, c.user_id, c.uid, c.name, c.color, c.description, c.kind, c.account_id, c.remote_href,
	c.remote_ctag, c.remote_token, c.read_only, c.is_default, c.sort_order, c.seq, c.created_at, c.updated_at`

func scanAddressBook(row interface{ Scan(...any) error }, extra ...any) (*AddressBook, error) {
	var c AddressBook
	var acc sql.NullInt64
	dest := []any{&c.ID, &c.UserID, &c.UID, &c.Name, &c.Color, &c.Description, &c.Kind, &acc, &c.RemoteHref,
		&c.RemoteCTag, &c.RemoteToken, &c.ReadOnly, &c.IsDefault, &c.SortOrder, &c.Seq, &c.CreatedAt, &c.UpdatedAt}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return nil, notFound(err)
	}
	if acc.Valid {
		c.AccountID = &acc.Int64
	}
	return &c, nil
}

// GetAddressBook returns an address book by id (no access check).
func (s *Store) GetAddressBook(ctx context.Context, id int64) (*AddressBook, error) {
	return scanAddressBook(s.DB.QueryRowContext(ctx, `SELECT `+abCols+` FROM addressbooks c WHERE c.id = ?`, id))
}

// GetAddressBookByUID returns one of a person's own address books by its path segment.
func (s *Store) GetAddressBookByUID(ctx context.Context, userID int64, uid string) (*AddressBook, error) {
	return scanAddressBook(s.DB.QueryRowContext(ctx, `SELECT `+abCols+` FROM addressbooks c WHERE c.user_id = ? AND c.uid = ?`, userID, uid))
}

// BookRole returns what userID may do with an address book; ErrNotFound when
// they may not see it at all (so other people's ids answer 404).
func (s *Store) BookRole(ctx context.Context, userID, calID int64) (string, *AddressBook, error) {
	c, err := s.GetAddressBook(ctx, calID)
	if err != nil {
		return "", nil, err
	}
	if c.UserID == userID {
		return RoleOwner, c, nil
	}
	role := ""
	rows, err := s.DB.QueryContext(ctx, `SELECT role FROM addressbook_shares WHERE addressbook_id = ? AND (user_id = ? OR everyone = 1)`, calID, userID)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r string
		if rows.Scan(&r) == nil && roleRank[r] > roleRank[role] {
			role = r
		}
	}
	if role == "" {
		return "", nil, ErrNotFound
	}
	return role, c, nil
}

// ListAddressBooksFor returns every address book a person owns or that is
// shared with them, with their role and own preferences.
func (s *Store) ListAddressBooksFor(ctx context.Context, userID int64) ([]*AddressBook, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+abCols+`, COALESCE(p.visible, 1), COALESCE(p.sort_order, c.sort_order),
		CASE WHEN c.user_id = ? THEN 'owner' ELSE (SELECT role FROM addressbook_shares sh WHERE sh.addressbook_id = c.id AND (sh.user_id = ? OR sh.everyone = 1)
			ORDER BY CASE role WHEN 'manage' THEN 4 WHEN 'write' THEN 3 ELSE 2 END DESC LIMIT 1) END,
		(SELECT COUNT(1) FROM contacts e WHERE e.addressbook_id = c.id AND e.kind <> 'group'),
		u.id, u.name, u.email
		FROM addressbooks c
		JOIN users u ON u.id = c.user_id
		LEFT JOIN addressbook_prefs p ON p.addressbook_id = c.id AND p.user_id = ?
		WHERE c.user_id = ? OR EXISTS (SELECT 1 FROM addressbook_shares sh WHERE sh.addressbook_id = c.id AND (sh.user_id = ? OR sh.everyone = 1))
		ORDER BY CASE WHEN c.user_id = ? THEN 0 ELSE 1 END, COALESCE(p.sort_order, c.sort_order), c.id`,
		userID, userID, userID, userID, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AddressBook
	for rows.Next() {
		var p Person
		var sort float64
		var c *AddressBook
		var visible bool
		var role string
		var n int
		c, err = scanAddressBook(rows, &visible, &sort, &role, &n, &p.ID, &p.Name, &p.Email)
		if err != nil {
			return nil, err
		}
		c.Visible, c.Role, c.Contacts, c.SortOrder = visible, role, n, sort
		c.Owner = &p
		out = append(out, c)
	}
	return out, rows.Err()
}

// VisibleBookIDs are the address books that make up a person's "All
// contacts" (and search): own and shared ones they have not hidden.
func (s *Store) VisibleBookIDs(ctx context.Context, userID int64) ([]int64, error) {
	books, err := s.ListAddressBooksFor(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := []int64{}
	for _, b := range books {
		if b.Visible {
			out = append(out, b.ID)
		}
	}
	return out, nil
}

// ReadableBookIDs are every address book a person may read (hidden ones included).
func (s *Store) ReadableBookIDs(ctx context.Context, userID int64) ([]int64, error) {
	books, err := s.ListAddressBooksFor(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := []int64{}
	for _, b := range books {
		out = append(out, b.ID)
	}
	return out, nil
}

// CreateAddressBook inserts an address book (uid and feed token are made up when empty).
func (s *Store) CreateAddressBook(ctx context.Context, c *AddressBook) error {
	if c.UID == "" {
		c.UID = s.freeAddressBookUID(ctx, c.UserID, c.Name)
	}
	if c.Kind == "" {
		c.Kind = "local"
	}
	if c.SortOrder == 0 {
		_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_order), 0) + 1 FROM addressbooks WHERE user_id = ?`, c.UserID).Scan(&c.SortOrder)
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO addressbooks (user_id, uid, name, color, description, kind, account_id, remote_href,
		read_only, is_default, sort_order) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.UserID, c.UID, c.Name, c.Color, c.Description, c.Kind, nullInt(c.AccountID), c.RemoteHref,
		c.ReadOnly, c.IsDefault, c.SortOrder)
	if err != nil {
		return conflict(err)
	}
	c.ID, _ = res.LastInsertId()
	c.CreatedAt, c.UpdatedAt = Now(), Now()
	return nil
}

// UpdateAddressBook stores the editable columns.
func (s *Store) UpdateAddressBook(ctx context.Context, c *AddressBook) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE addressbooks SET name = ?, color = ?, description = ?, read_only = ?,
		is_default = ?, sort_order = ?, remote_href = ?, remote_ctag = ?, remote_token = ?, seq = ?, updated_at = ? WHERE id = ?`,
		c.Name, c.Color, c.Description, c.ReadOnly, c.IsDefault, c.SortOrder, c.RemoteHref, c.RemoteCTag, c.RemoteToken, c.Seq, Now(), c.ID)
	return conflict(err)
}

// SetDefaultAddressBook marks one of a person's address books as where new contacts go.
func (s *Store) SetDefaultAddressBook(ctx context.Context, userID, calID int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE addressbooks SET is_default = CASE WHEN id = ? THEN 1 ELSE 0 END WHERE user_id = ?`, calID, userID)
	return err
}

// DeleteAddressBook removes an address book and its contacts.
func (s *Store) DeleteAddressBook(ctx context.Context, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `DELETE FROM contacts_fts WHERE rowid IN (SELECT id FROM contacts WHERE addressbook_id = ?)`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM addressbooks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, _ = tx.ExecContext(ctx, `DELETE FROM tombstones WHERE addressbook_id = ?`, id)
	return tx.Commit()
}

// DefaultAddressBook returns where a person's new contacts go: their default
// address book, else their first writable local one.
func (s *Store) DefaultAddressBook(ctx context.Context, userID int64) (*AddressBook, error) {
	c, err := scanAddressBook(s.DB.QueryRowContext(ctx, `SELECT `+abCols+` FROM addressbooks c WHERE c.user_id = ? AND c.read_only = 0
		ORDER BY c.is_default DESC, CASE c.kind WHEN 'local' THEN 0 ELSE 1 END, c.sort_order, c.id LIMIT 1`, userID))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNotFound
	}
	return c, err
}

// AddressBooksOfAccount lists the address books mirrored from an account.
func (s *Store) AddressBooksOfAccount(ctx context.Context, accountID int64) ([]*AddressBook, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+abCols+` FROM addressbooks c WHERE c.account_id = ? ORDER BY c.sort_order, c.id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AddressBook
	for rows.Next() {
		c, err := scanAddressBook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// OwnAddressBooks lists a person's own address books (CalDAV home).
func (s *Store) OwnAddressBooks(ctx context.Context, userID int64) ([]*AddressBook, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+abCols+` FROM addressbooks c WHERE c.user_id = ? ORDER BY c.sort_order, c.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AddressBook
	for rows.Next() {
		c, err := scanAddressBook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SavePrefs stores a person's own view of an address book.
func (s *Store) SavePrefs(ctx context.Context, userID, abID int64, visible *bool, sort *float64) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO addressbook_prefs (user_id, addressbook_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, userID, abID)
	if err != nil {
		return err
	}
	if visible != nil {
		if _, err := s.DB.ExecContext(ctx, `UPDATE addressbook_prefs SET visible = ? WHERE user_id = ? AND addressbook_id = ?`, *visible, userID, abID); err != nil {
			return err
		}
	}
	if sort != nil {
		if _, err := s.DB.ExecContext(ctx, `UPDATE addressbook_prefs SET sort_order = ? WHERE user_id = ? AND addressbook_id = ?`, *sort, userID, abID); err != nil {
			return err
		}
	}
	return nil
}

// BumpAddressBook hands an address book a new change number (CalDAV ctag/sync token).
func (s *Store) BumpAddressBook(ctx context.Context, tx *sql.Tx, calID int64) (int64, error) {
	var n int64
	if err := tx.QueryRowContext(ctx, `SELECT CAST(value AS INTEGER) FROM settings WHERE key = 'seq'`).Scan(&n); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	n++
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('seq', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, n); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE addressbooks SET seq = ?, updated_at = ? WHERE id = ?`, n, Now(), calID); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) freeAddressBookUID(ctx context.Context, userID int64, name string) string {
	base := Slug(name)
	if base == "" {
		base = "contacts"
	}
	uid := base
	for i := 2; i < 1000; i++ {
		var n int
		_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM addressbooks WHERE user_id = ? AND uid = ?`, userID, uid).Scan(&n)
		if n == 0 {
			return uid
		}
		uid = base + "-" + itoa(i)
	}
	return base + "-" + RandomToken(4)
}

// ---------- sharing ----------

// ListShares returns who an address book is shared with.
func (s *Store) ListShares(ctx context.Context, calID int64) ([]Share, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT sh.id, sh.addressbook_id, sh.user_id, sh.email, sh.everyone, sh.role, sh.created_at,
		COALESCE(u.name, ''), COALESCE(u.email, '')
		FROM addressbook_shares sh LEFT JOIN users u ON u.id = sh.user_id WHERE sh.addressbook_id = ? ORDER BY sh.everyone DESC, sh.id`, calID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Share{}
	for rows.Next() {
		var sh Share
		var uid sql.NullInt64
		var name, email string
		if err := rows.Scan(&sh.ID, &sh.AddressBookID, &uid, &sh.Email, &sh.Everyone, &sh.Role, &sh.CreatedAt, &name, &email); err != nil {
			return nil, err
		}
		if uid.Valid {
			sh.UserID = &uid.Int64
			sh.Person = &Person{ID: uid.Int64, Name: name, Email: email}
		} else if !sh.Everyone {
			sh.Pending = true
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// UpsertShare grants (or changes) access for a person, an email or everyone.
func (s *Store) UpsertShare(ctx context.Context, sh *Share, by int64) error {
	var err error
	switch {
	case sh.Everyone:
		_, err = s.DB.ExecContext(ctx, `INSERT INTO addressbook_shares (addressbook_id, everyone, role, created_by) VALUES (?, 1, ?, ?)
			ON CONFLICT (addressbook_id) WHERE everyone = 1 DO UPDATE SET role = excluded.role`, sh.AddressBookID, sh.Role, by)
	case sh.UserID != nil:
		_, err = s.DB.ExecContext(ctx, `INSERT INTO addressbook_shares (addressbook_id, user_id, role, created_by) VALUES (?, ?, ?, ?)
			ON CONFLICT (addressbook_id, user_id) WHERE user_id IS NOT NULL DO UPDATE SET role = excluded.role`, sh.AddressBookID, *sh.UserID, sh.Role, by)
	default:
		_, err = s.DB.ExecContext(ctx, `INSERT INTO addressbook_shares (addressbook_id, email, role, created_by) VALUES (?, ?, ?, ?)
			ON CONFLICT (addressbook_id, email) WHERE user_id IS NULL AND email <> '' DO UPDATE SET role = excluded.role`, sh.AddressBookID, strings.ToLower(sh.Email), sh.Role, by)
	}
	return err
}

// GetShare returns one share.
func (s *Store) GetShare(ctx context.Context, id int64) (*Share, error) {
	var sh Share
	var uid sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT id, addressbook_id, user_id, email, everyone, role, created_at FROM addressbook_shares WHERE id = ?`, id).
		Scan(&sh.ID, &sh.AddressBookID, &uid, &sh.Email, &sh.Everyone, &sh.Role, &sh.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	if uid.Valid {
		sh.UserID = &uid.Int64
	}
	return &sh, nil
}

// DeleteShare removes a share.
func (s *Store) DeleteShare(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM addressbook_shares WHERE id = ?`, id)
	return err
}

// LeaveAddressBook removes a person's own share of someone else's address book
// (an address book shared with everyone is hidden instead).
func (s *Store) LeaveAddressBook(ctx context.Context, userID, calID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM addressbook_shares WHERE addressbook_id = ? AND user_id = ?`, calID, userID)
	if err != nil {
		return err
	}
	f := false
	return s.SavePrefs(ctx, userID, calID, &f, nil)
}

// BindPendingShares turns shares and attendee rows addressed to an email into
// ones for the account that now has it.
func (s *Store) BindPendingShares(ctx context.Context, userID int64, email string) {
	email = strings.ToLower(email)
	_, _ = s.DB.ExecContext(ctx, `UPDATE OR IGNORE addressbook_shares SET user_id = ?, email = '' WHERE user_id IS NULL AND everyone = 0 AND email = ?`, userID, email)
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM addressbook_shares WHERE user_id IS NULL AND everyone = 0 AND email = ?`, email)
}

// People lists everyone on the box (name and email only) for share and
// invite pickers.
func (s *Store) People(ctx context.Context) ([]Person, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, email FROM users ORDER BY name COLLATE NOCASE, email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Person{}
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.ID, &p.Name, &p.Email); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// UserIDByEmail returns the account with that email (0 when none).
func (s *Store) UserIDByEmail(ctx context.Context, email string) int64 {
	var id int64
	_ = s.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE email = ?`, strings.ToLower(strings.TrimSpace(email))).Scan(&id)
	return id
}
