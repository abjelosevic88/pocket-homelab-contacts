package store

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

// DefaultTitle is the app title until someone changes it.
const DefaultTitle = "Pocket Contacts"

// Settings are the box settings plus one person's preferences.
type Settings struct {
	Title         string `json:"title"`
	SetupComplete bool   `json:"setup_complete"`

	SortBy               string `json:"sort_by"`       // given | family
	DisplayOrder         string `json:"display_order"` // given_first | family_first
	DefaultAddressBookID int64  `json:"default_addressbook_id"`
	Density              string `json:"density"`      // comfortable | compact
	PhoneRegion          string `json:"phone_region"` // ISO country code numbers without + belong to (BA)

	NotifyKind      string `json:"notify_kind"` // apprise | ntfy | webhook
	NotifyURL       string `json:"notify_url"`
	NotifyToken     string `json:"-"`
	BirthdayNotify  bool   `json:"birthday_notify"`  // a message on the day
	BirthdayAhead   int    `json:"birthday_ahead"`   // and this many days before (0 = off)
	BirthdayTime    string `json:"birthday_time"`    // "08:00"
	AnniversaryNote bool   `json:"anniversary_note"` // anniversaries too

	// Other Pocket apps used from a contact: app → "core" | "direct" | "off".
	Integrations     map[string]string `json:"integrations"`
	IntegrationURLs  map[string]string `json:"integration_urls"`
	IntegrationToken map[string]bool   `json:"integration_tokens"` // which have a direct token saved
}

func defaults() Settings {
	return Settings{
		Title: DefaultTitle, SortBy: "given", DisplayOrder: "given_first", Density: "comfortable",
		NotifyKind: "apprise", BirthdayNotify: true, BirthdayAhead: 0, BirthdayTime: "08:00", AnniversaryNote: true,
		Integrations: map[string]string{}, IntegrationURLs: map[string]string{}, IntegrationToken: map[string]bool{},
	}
}

// GetSettings loads the box settings plus the preferences of userID, with
// defaults applied. userID 0 gives the box settings with default preferences.
func (s *Store) GetSettings(ctx context.Context, userID int64) (Settings, error) {
	st := defaults()
	rows, err := s.DB.QueryContext(ctx, `SELECT key, value FROM settings WHERE key IN ('title', 'setup_complete')
		UNION ALL SELECT key, value FROM user_settings WHERE user_id = ?`, userID)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	atoi := func(v string, def int) int {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		return def
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return st, err
		}
		switch {
		case k == "title":
			if v != "" {
				st.Title = v
			}
		case k == "setup_complete":
			st.SetupComplete = v == "1"
		case k == "sort_by":
			st.SortBy = v
		case k == "display_order":
			st.DisplayOrder = v
		case k == "default_addressbook_id":
			st.DefaultAddressBookID, _ = strconv.ParseInt(v, 10, 64)
		case k == "density":
			st.Density = v
		case k == "phone_region":
			st.PhoneRegion = v
		case k == "notify_kind":
			st.NotifyKind = v
		case k == "notify_url":
			st.NotifyURL = v
		case k == "notify_token":
			st.NotifyToken = v
		case k == "birthday_notify":
			st.BirthdayNotify = v != "0"
		case k == "birthday_ahead":
			st.BirthdayAhead = atoi(v, 0)
		case k == "birthday_time":
			st.BirthdayTime = v
		case k == "anniversary_note":
			st.AnniversaryNote = v != "0"
		case strings.HasPrefix(k, "app.mode."):
			st.Integrations[strings.TrimPrefix(k, "app.mode.")] = v
		case strings.HasPrefix(k, "app.url."):
			st.IntegrationURLs[strings.TrimPrefix(k, "app.url.")] = v
		case strings.HasPrefix(k, "app.token."):
			st.IntegrationToken[strings.TrimPrefix(k, "app.token.")] = v != ""
		}
	}
	return st, rows.Err()
}

func b2s(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func itoa(n int) string { return strconv.Itoa(n) }

// SaveSettings stores a person's preferences (not the notify token, not integrations).
func (s *Store) SaveSettings(ctx context.Context, userID int64, st Settings) error {
	return s.putUserSettings(ctx, userID, map[string]string{
		"sort_by": st.SortBy, "display_order": st.DisplayOrder, "density": st.Density, "phone_region": st.PhoneRegion,
		"default_addressbook_id": strconv.FormatInt(st.DefaultAddressBookID, 10),
		"notify_kind":            st.NotifyKind, "notify_url": st.NotifyURL, "birthday_notify": b2s(st.BirthdayNotify),
		"birthday_ahead": itoa(st.BirthdayAhead), "birthday_time": st.BirthdayTime, "anniversary_note": b2s(st.AnniversaryNote),
	})
}

// UserSetting reads one raw key of a person's settings ("" when missing).
func (s *Store) UserSetting(ctx context.Context, userID int64, key string) string {
	var v string
	_ = s.DB.QueryRowContext(ctx, `SELECT value FROM user_settings WHERE user_id = ? AND key = ?`, userID, key).Scan(&v)
	return v
}

// SetUserSetting stores one key for a person.
func (s *Store) SetUserSetting(ctx context.Context, userID int64, key, value string) error {
	return s.putUserSettings(ctx, userID, map[string]string{key: value})
}

// SaveBoxSettings stores the settings of the box itself: title and setup state.
func (s *Store) SaveBoxSettings(ctx context.Context, st Settings) error {
	kv := map[string]string{"title": st.Title, "setup_complete": b2s(st.SetupComplete)}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	for k, v := range kv {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) putUserSettings(ctx context.Context, userID int64, kv map[string]string) error {
	if userID <= 0 {
		return ErrNotFound
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	for k, v := range kv {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_settings (user_id, key, value) VALUES (?, ?, ?)
			ON CONFLICT(user_id, key) DO UPDATE SET value = excluded.value`, userID, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// OwnerID is the account the box was set up with (the first user): keys
// given to the whole box through the environment are theirs.
func (s *Store) OwnerID(ctx context.Context) int64 {
	var id sql.NullInt64
	_ = s.DB.QueryRowContext(ctx, `SELECT MIN(id) FROM users`).Scan(&id)
	return id.Int64
}

// UserIDs lists every user id.
func (s *Store) UserIDs(ctx context.Context) []int64 {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM users ORDER BY id`)
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
