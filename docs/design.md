# Pocket Contacts — design

Pocket Contacts keeps everyone a household knows in one searchable place, syncs it to every phone over
CardDAV, imports what people already have (vCard, CSV, iCloud, Google, Nextcloud), and connects each person
to the other Pocket apps: the meetings you have with them (Pocket Calendar), the notes about them (Pocket
Notes) and the to-dos about them (Pocket Tasks).

Same build as the other Pocket apps: Go + chi + modernc SQLite + React 19/Vite, one binary, one volume, Pocket
Core SDK snapshot in `third_party/pocket-core`. Token prefix `pcon_`, session cookie `pcon_session`, theme key
`pcon-theme`, accent rose `#e11d48` (dark `#fb7185`). Slug `contacts`, Core env prefix `CONTACTS`.

## Model

- **Address book** = one CardDAV collection (`addressbooks`). Every person gets "Contacts" on first sign-in.
  `kind` is `local` or the account kind (`carddav`, `nextcloud`, `icloud`) when it mirrors a remote address book.
- **Contact** = one vCard resource (`contacts`). **`vcf` is the source of truth**, stored as the client sent
  it, unknown properties included; the other columns are an index rebuilt from it. `store.SaveContact` is the
  only write path (index columns, FTS row, change number, tombstones, remote dirty flag, Core hook) and
  `store.DeleteContact` the only delete path (tombstone, Recently deleted copy, pending remote delete).
- **Structured view** = `vcard.Contact` (internal/vcard/contact.go). The API and web app edit that; the
  server applies an edit with `card.Apply(prev, next)`, which rewrites only the sections that changed (name,
  emails, phones, …). A phone number added in the web app never drops the iOS `X-ABADR` of an address or an
  Android custom property.
- **Labels** (`CATEGORIES`) are the groups people use day to day (Nextcloud, Android and Google all use
  them). Apple **group cards** (`X-ADDRESSBOOKSERVER-KIND:group` with `X-ADDRESSBOOKSERVER-MEMBER`) are kept
  as resources with `kind = group`, hidden from the contact list, and shown as groups (filter
  `category=group:<uid>`).
- **Per person**: `contact_prefs` holds stars and "last viewed" per person; `addressbook_prefs` whether a
  book is part of "All contacts". Settings in `user_settings`.

## Rules

1. **Strictly per person** (see pocket-apps-strictly-per-person): every address book has an owner; others
   see it only through `addressbook_shares` (person, pending email, everyone) with role `read`, `write` or
   `manage`. Any id a person may not see answers **404**; seeing but not writing answers 403. Admins get no
   read access to other people's books. The `/backup` download refuses once a second person exists.
2. A link token (`pcx_`) may only use the routes in `linkRoutes` (internal/server/links.go). With
   `contacts:write` an app may create contacts and edit/delete only those whose `source` is its own slug.
3. Search folds case, diacritics (`č→c`, `đ→d`/`dj`) and Cyrillic (`Јелена` is found by `jelena`); phone
   search matches any 3+ digits anywhere in a number, whatever the formatting.
4. Deletes go to Recently deleted for 30 days (restorable, own book only).
5. Duplicates are found, never merged on their own: same multi-word name (folded), same email, or same phone
   (last 8 digits). Single-word names only match on a shared email/phone (three different "Zoran"s exist).
   Merging keeps the chosen card's name and UID, unions every other field, concatenates notes, and moves the
   others to Recently deleted. "Not duplicates" is remembered per pair.
6. Import never writes outside the chosen address book. Re-importing the same file is a no-op (match by UID,
   then by the duplicate rules) unless the person chose "add all".

## Sync

- **CardDAV server** (internal/server/dav.go, docs/carddav.md) for iOS/macOS Contacts, DAVx5, Thunderbird.
  Principal = `username` (Settings → Profile). Password = an API token (`pcon_…`) or the local password.
  Sync tokens `http://pocket-contacts/sync/<seq>`; `settings.seq` is the global change counter.
- **Accounts** (internal/server/accounts.go): a CardDAV address book elsewhere (Nextcloud through Pocket Core
  with the person's own credential, Nextcloud/iCloud/Fastmail/any CardDAV directly). Modes: `import` (pull
  once, keep the mapping, then switch itself to `off`), `two_way`, `read_only`. Pull by sync-collection when
  the server supports it, else ctag + etag listing; push dirty contacts with If-Match; a 412 pulls first.
  Remote wins only when the local copy is not dirty; when both changed, REV (else updated time) wins and the
  loser is kept in Recently deleted.

## Integrations

Contacts **offers** `contacts:read` and `contacts:write` and **uses** (Core app links, or a direct address +
token the person pastes; never a box key):

| App | Scopes | Shown on a contact |
|-----|--------|--------------------|
| Pocket Calendar | `calendar:read`, `calendar:write` | past/upcoming events where an email of the contact is a guest (`/api/v1/search?q=<email>`), "New event" with the contact invited |
| Pocket Notes | `notes:read` | notes that link to the contact's page (`/api/v1/mentions?url=`), "New note" (`<notes>/new?title&url&label&folder=People`) |
| Pocket Tasks | `tasks:read`, `tasks:write` | open tasks that link to the contact's page, "Remind me" creates one |

The contact's page address is `BASE_URL/contacts/<id>`; that is what notes and tasks link to.
Other apps use Contacts the other way round: Calendar shows birthdays and suggests guests (`contacts:read`
→ `/api/v1/birthdays`, `/api/v1/suggest`), Notes' "Link to something" finds people (`/api/v1/search`).

Publishes `contacts.created|updated|deleted` (payload `{id, uid, fn, addressbook_id, source}`, `user_email` of
the book owner) and `contacts.birthday` on the morning of a birthday.

## Birthdays

`/api/v1/birthdays` lists the next birthdays and anniversaries of the contacts in a person's visible books
(age when the year is known, "--MM-DD" otherwise; 29 February falls on 28 February in other years). A
reminder goes out at `birthday_time` on the day (and `birthday_ahead` days before) through the person's
notification route (Apprise route `contacts` for the owner). `GET /feed/birthdays/<feed_token>.ics` is a
subscribable calendar of them.
