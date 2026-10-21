package store

// User is an account. Data is strictly per person; other people see only
// what is shared with them.
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	Username     string `json:"username"` // CardDAV principal name
	PasswordHash string `json:"-"`
	Role         string `json:"role"` // admin | member
	FeedToken    string `json:"-"`
	CreatedAt    string `json:"created_at"`
}

// APIToken is a personal access token (hash stored, plaintext shown once).
// It doubles as the app password CardDAV clients sign in with.
type APIToken struct {
	ID         int64   `json:"id"`
	UserID     int64   `json:"user_id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// AddressBook is one collection of contacts.
type AddressBook struct {
	ID          int64   `json:"id"`
	UserID      int64   `json:"owner_id"`
	UID         string  `json:"uid"`
	Name        string  `json:"name"`
	Color       string  `json:"color"`
	Description string  `json:"description"`
	Kind        string  `json:"kind"` // local | carddav | nextcloud
	AccountID   *int64  `json:"account_id"`
	RemoteHref  string  `json:"remote_href,omitempty"`
	RemoteCTag  string  `json:"-"`
	RemoteToken string  `json:"-"`
	ReadOnly    bool    `json:"read_only"`
	IsDefault   bool    `json:"is_default"`
	SortOrder   float64 `json:"sort_order"`
	Seq         int64   `json:"-"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`

	// Filled per viewer.
	Role     string  `json:"role,omitempty"` // owner | manage | write | read
	Visible  bool    `json:"visible"`
	Owner    *Person `json:"owner,omitempty"`
	Shares   []Share `json:"shares,omitempty"`
	Contacts int     `json:"contacts"`
}

// Person is the public face of an account (for sharing).
type Person struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// Share grants someone (or everyone on the box) access to an address book.
type Share struct {
	ID            int64   `json:"id"`
	AddressBookID int64   `json:"addressbook_id"`
	UserID        *int64  `json:"user_id"`
	Email         string  `json:"email,omitempty"`
	Everyone      bool    `json:"everyone"`
	Role          string  `json:"role"`
	Person        *Person `json:"person,omitempty"`
	Pending       bool    `json:"pending"`
	CreatedAt     string  `json:"created_at"`
}

// Account is an external address book account (CardDAV or Nextcloud).
type Account struct {
	ID           int64  `json:"id"`
	UserID       int64  `json:"-"`
	Kind         string `json:"kind"` // carddav | nextcloud
	Name         string `json:"name"`
	URL          string `json:"url"`
	Username     string `json:"username"`
	Secret       string `json:"-"`    // sealed
	Via          string `json:"via"`  // direct | core
	Mode         string `json:"mode"` // import | two_way | read_only | off
	SyncMinutes  int    `json:"sync_minutes"`
	Enabled      bool   `json:"enabled"`
	LastSyncAt   string `json:"last_sync_at"`
	LastError    string `json:"last_error"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	HasSecret    bool   `json:"has_secret"`
	AddressBooks int    `json:"addressbooks"`
}

// ContactRow is the stored vCard with its index columns.
type ContactRow struct {
	ID            int64    `json:"id"`
	AddressBookID int64    `json:"addressbook_id"`
	UID           string   `json:"uid"`
	Href          string   `json:"href"`
	ETag          string   `json:"etag"`
	VCF           string   `json:"-"`
	Kind          string   `json:"kind"`
	FN            string   `json:"fn"`
	SortGiven     string   `json:"-"`
	SortFamily    string   `json:"-"`
	Given         string   `json:"given"`
	Family        string   `json:"family"`
	Nickname      string   `json:"nickname"`
	Org           string   `json:"org"`
	Title         string   `json:"title"`
	Emails        string   `json:"-"`
	Phones        string   `json:"-"`
	Bday          string   `json:"birthday"`
	Anniversary   string   `json:"anniversary"`
	Categories    []string `json:"categories"`
	Members       []string `json:"members,omitempty"`
	HasPhoto      bool     `json:"has_photo"`
	PhotoHash     string   `json:"photo_hash,omitempty"`
	Source        string   `json:"source"`
	CreatedBy     int64    `json:"created_by"`
	UpdatedBy     int64    `json:"updated_by"`
	Seq           int64    `json:"-"`
	RemoteHref    string   `json:"-"`
	RemoteETag    string   `json:"-"`
	Dirty         bool     `json:"-"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
}

// DeletedContact is a restorable deleted contact.
type DeletedContact struct {
	ID            int64  `json:"id"`
	AddressBookID int64  `json:"addressbook_id"`
	UID           string `json:"uid"`
	FN            string `json:"fn"`
	VCF           string `json:"-"`
	DeletedAt     string `json:"deleted_at"`
}

// Activity is one entry of a person's change log.
type Activity struct {
	ID            int64  `json:"id"`
	AddressBookID *int64 `json:"addressbook_id"`
	ContactID     *int64 `json:"contact_id"`
	Action        string `json:"action"`
	Summary       string `json:"summary"`
	Detail        string `json:"detail"`
	CreatedAt     string `json:"created_at"`
}
