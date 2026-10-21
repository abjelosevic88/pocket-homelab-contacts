CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  username      TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT 'admin',
  feed_token    TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT
);
CREATE UNIQUE INDEX users_username ON users(username) WHERE username <> '';
CREATE UNIQUE INDEX users_feed_token ON users(feed_token) WHERE feed_token <> '';

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL DEFAULT '',
  token_hash   TEXT NOT NULL UNIQUE,
  prefix       TEXT NOT NULL DEFAULT '',
  last_used_at TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Box-wide settings (title, setup_complete, seq). Everything a person
-- chooses is in user_settings under their id.
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);
CREATE TABLE user_settings (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key     TEXT NOT NULL,
  value   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, key)
);

-- An external address book account: CardDAV (iCloud, Fastmail, Radicale,
-- any CardDAV server) or Nextcloud (directly or through Pocket Core).
-- Secrets are AES-GCM sealed with DATA_DIR/secret.key.
CREATE TABLE accounts (
  id            INTEGER PRIMARY KEY,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind          TEXT NOT NULL,              -- carddav | nextcloud
  name          TEXT NOT NULL DEFAULT '',
  url           TEXT NOT NULL DEFAULT '',
  username      TEXT NOT NULL DEFAULT '',
  secret        TEXT NOT NULL DEFAULT '',   -- sealed password / app password
  via           TEXT NOT NULL DEFAULT 'direct', -- direct | core (Nextcloud through Pocket Core)
  mode          TEXT NOT NULL DEFAULT 'two_way', -- import (once, then off) | two_way | read_only | off
  sync_minutes  INTEGER NOT NULL DEFAULT 15,
  enabled       INTEGER NOT NULL DEFAULT 1,
  last_sync_at  TEXT NOT NULL DEFAULT '',
  last_error    TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX accounts_user ON accounts(user_id);

-- An address book is a CardDAV collection of vCards (uid = its path segment).
-- kind = local, or the account kind when it mirrors a remote address book.
CREATE TABLE addressbooks (
  id             INTEGER PRIMARY KEY,
  user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  uid            TEXT NOT NULL,
  name           TEXT NOT NULL,
  color          TEXT NOT NULL DEFAULT '',
  description    TEXT NOT NULL DEFAULT '',
  kind           TEXT NOT NULL DEFAULT 'local',
  account_id     INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
  remote_href    TEXT NOT NULL DEFAULT '',
  remote_ctag    TEXT NOT NULL DEFAULT '',
  remote_token   TEXT NOT NULL DEFAULT '',
  read_only      INTEGER NOT NULL DEFAULT 0,
  is_default     INTEGER NOT NULL DEFAULT 0,
  sort_order     REAL NOT NULL DEFAULT 0,
  seq            INTEGER NOT NULL DEFAULT 0,
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  UNIQUE (user_id, uid)
);
CREATE INDEX addressbooks_account ON addressbooks(account_id);

-- Who else sees an address book: one person, someone invited by email who
-- has not signed in yet (bound on first sign-in), or everyone on the box.
-- role: read | write | manage.
CREATE TABLE addressbook_shares (
  id             INTEGER PRIMARY KEY,
  addressbook_id INTEGER NOT NULL REFERENCES addressbooks(id) ON DELETE CASCADE,
  user_id        INTEGER REFERENCES users(id) ON DELETE CASCADE,
  email          TEXT NOT NULL DEFAULT '',
  everyone       INTEGER NOT NULL DEFAULT 0,
  role           TEXT NOT NULL DEFAULT 'read',
  created_by     INTEGER NOT NULL DEFAULT 0,
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE UNIQUE INDEX shares_user ON addressbook_shares(addressbook_id, user_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX shares_email ON addressbook_shares(addressbook_id, email) WHERE user_id IS NULL AND email <> '';
CREATE UNIQUE INDEX shares_everyone ON addressbook_shares(addressbook_id) WHERE everyone = 1;
CREATE INDEX shares_grantee ON addressbook_shares(user_id);

-- A person's own view of an address book they own or that is shared with them.
CREATE TABLE addressbook_prefs (
  user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  addressbook_id INTEGER NOT NULL REFERENCES addressbooks(id) ON DELETE CASCADE,
  visible        INTEGER NOT NULL DEFAULT 1, -- part of "All contacts" and search
  sort_order     REAL NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, addressbook_id)
);

-- A contact is one vCard resource. vcf is the source of truth (kept as the
-- client sent it, unknown properties included); the columns are the index.
-- kind: individual | org | group (Apple group cards, X-ADDRESSBOOKSERVER-KIND).
CREATE TABLE contacts (
  id             INTEGER PRIMARY KEY,
  addressbook_id INTEGER NOT NULL REFERENCES addressbooks(id) ON DELETE CASCADE,
  uid            TEXT NOT NULL,
  href           TEXT NOT NULL,
  etag           TEXT NOT NULL DEFAULT '',
  vcf            TEXT NOT NULL DEFAULT '',
  kind           TEXT NOT NULL DEFAULT 'individual',
  fn             TEXT NOT NULL DEFAULT '',
  sort_given     TEXT NOT NULL DEFAULT '',   -- lower-case, diacritics folded: given name first
  sort_family    TEXT NOT NULL DEFAULT '',   -- family name first
  given          TEXT NOT NULL DEFAULT '',
  family         TEXT NOT NULL DEFAULT '',
  nickname       TEXT NOT NULL DEFAULT '',
  org            TEXT NOT NULL DEFAULT '',
  title          TEXT NOT NULL DEFAULT '',
  emails         TEXT NOT NULL DEFAULT '',   -- lower-case, space separated
  phones         TEXT NOT NULL DEFAULT '',   -- digits only (leading + kept), space separated
  bday           TEXT NOT NULL DEFAULT '',   -- YYYY-MM-DD or --MM-DD
  anniversary    TEXT NOT NULL DEFAULT '',
  categories     TEXT NOT NULL DEFAULT '',   -- JSON array
  members        TEXT NOT NULL DEFAULT '',   -- JSON array of member uids (group cards)
  has_photo      INTEGER NOT NULL DEFAULT 0,
  photo_hash     TEXT NOT NULL DEFAULT '',
  source         TEXT NOT NULL DEFAULT '',   -- web | carddav | remote | import | <app slug>
  created_by     INTEGER NOT NULL DEFAULT 0,
  updated_by     INTEGER NOT NULL DEFAULT 0,
  seq            INTEGER NOT NULL DEFAULT 0,
  remote_href    TEXT NOT NULL DEFAULT '',
  remote_etag    TEXT NOT NULL DEFAULT '',
  dirty          INTEGER NOT NULL DEFAULT 0, -- local change not yet pushed to the remote address book
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE UNIQUE INDEX contacts_href ON contacts(addressbook_id, href);
CREATE INDEX contacts_uid ON contacts(addressbook_id, uid);
CREATE INDEX contacts_sort ON contacts(addressbook_id, sort_given);
CREATE INDEX contacts_seq ON contacts(addressbook_id, seq);
CREATE INDEX contacts_bday ON contacts(substr(bday, -5)) WHERE bday <> '';
CREATE INDEX contacts_dirty ON contacts(addressbook_id) WHERE dirty = 1;
CREATE INDEX contacts_remote ON contacts(addressbook_id, remote_href) WHERE remote_href <> '';

-- Search index (rowid = contacts.id). names includes a Latin transliteration
-- of Cyrillic names; phones holds every suffix-friendly digit form.
CREATE VIRTUAL TABLE contacts_fts USING fts5(names, emails, phones, org, other, content='', contentless_delete=1, tokenize='unicode61 remove_diacritics 2');

-- A person's own marks on a contact they can see.
CREATE TABLE contact_prefs (
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  contact_id  INTEGER NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
  starred     INTEGER NOT NULL DEFAULT 0,
  viewed_at   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, contact_id)
);
CREATE INDEX contact_prefs_starred ON contact_prefs(user_id) WHERE starred = 1;

-- Where a resource used to be, for CardDAV sync-collection after a delete or a move.
CREATE TABLE tombstones (
  id             INTEGER PRIMARY KEY,
  addressbook_id INTEGER NOT NULL,
  href           TEXT NOT NULL,
  seq            INTEGER NOT NULL
);
CREATE INDEX tombstones_ab ON tombstones(addressbook_id, seq);

-- Deletes the remote address book has not heard of yet.
CREATE TABLE remote_deletes (
  id             INTEGER PRIMARY KEY,
  addressbook_id INTEGER NOT NULL REFERENCES addressbooks(id) ON DELETE CASCADE,
  remote_href    TEXT NOT NULL,
  remote_etag    TEXT NOT NULL DEFAULT ''
);

-- Recently deleted contacts, restorable for 30 days.
CREATE TABLE deleted_contacts (
  id             INTEGER PRIMARY KEY,
  user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  addressbook_id INTEGER NOT NULL REFERENCES addressbooks(id) ON DELETE CASCADE,
  uid            TEXT NOT NULL,
  fn             TEXT NOT NULL DEFAULT '',
  vcf            TEXT NOT NULL,
  deleted_by     INTEGER NOT NULL DEFAULT 0,
  deleted_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX deleted_user ON deleted_contacts(user_id, deleted_at);

-- Pairs a person said are not duplicates.
CREATE TABLE not_duplicates (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  a_uid   TEXT NOT NULL,
  b_uid   TEXT NOT NULL,
  PRIMARY KEY (user_id, a_uid, b_uid)
);

-- Which birthday reminders already went out (per person, contact, date and kind).
CREATE TABLE reminder_log (
  user_id    INTEGER NOT NULL,
  contact_id INTEGER NOT NULL,
  day        TEXT NOT NULL,
  kind       TEXT NOT NULL,
  fired_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  PRIMARY KEY (user_id, contact_id, day, kind)
);

CREATE TABLE activity (
  id             INTEGER PRIMARY KEY,
  user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  addressbook_id INTEGER,
  contact_id     INTEGER,
  action         TEXT NOT NULL,
  summary        TEXT NOT NULL DEFAULT '',
  detail         TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX activity_user ON activity(user_id, id);
