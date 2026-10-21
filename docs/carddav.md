# CardDAV: phones, Macs and other apps

Pocket Contacts is a CardDAV server (RFC 6352 with sync-collection, RFC 6578). iPhone and Mac Contacts, DAVx5 on
Android and Thunderbird sync with it directly. It can also pull from (and push to) another CardDAV server, such as
Nextcloud or iCloud: see "Bringing contacts over" below.

**User name**: your CardDAV name (Settings → Profile; `GET /api/v1/devices` → `username`).
**Password**: an app password per device. Settings → Phones → *Add a phone* makes an API token named "Device: <name>".
Your Pocket Contacts password also works when the box uses local accounts. Revoke a lost phone's token and it stops syncing.

## iPhone and iPad

**One tap.** In Settings → Phones → *Add a phone*, scan the QR code with the iPhone camera (or open the link on the
phone). The link works once, for 15 minutes. It downloads a configuration profile. Then open Settings → *Profile
Downloaded* → Install. The profile holds the address, your user name and the new device token.

**By hand.** Open Settings → Apps → Contacts → Contacts Accounts → Add Account → Other → Add CardDAV Account (on iOS 17
and older: Settings → Contacts → Accounts → …):

| Field | Value |
|---|---|
| Server | `contacts.example.com/remote.php/carddav/principals/<username>/` |
| User Name | `<username>` |
| Password | the device token (`pcon_…`) |
| Description | Pocket Contacts |

Use that exact server path. iOS only offers an account under Settings → Contacts → **Default Account** if it was
added with the legacy `/remote.php/carddav/principals/<user>/` path (nextcloud/contacts#1870). With the plain host it
syncs, but new contacts may land in iCloud. After adding the account, set Default Account → Pocket Contacts.

iOS (and macOS) show **one address book per account**: the first one in your home, which is your default address book
(Settings → Address books → *Default*). Make the book you want on the phone the default one.

## Mac

Open the profile link (or a `.mobileconfig` file you saved), then go to System Settings → Privacy & Security → Profiles →
Install. To set it up by hand: System Settings → Internet Accounts → Add Other Account → CardDAV account. Choose Account
Type *Manual*, give the user name and token, and use the server address `contacts.example.com`. Discovery goes through
`/.well-known/carddav`. If the Mac asks for a server path, give it `/remote.php/carddav/principals/<username>/`.

## Android (DAVx5)

Add an account → *Login with URL and user name*. Base URL: `https://contacts.example.com/dav/`. Then enter your user
name and the token, and pick the address books to sync. Under *Contact group method*, "Groups are per-contact
categories" maps to labels (CATEGORIES), which is what the web app shows as labels. Apple group cards also sync either
way.

## Thunderbird

Address Book → New Address Book → Add CardDAV Address Book. Enter your user name and the location
`https://contacts.example.com/dav/`, then the token as the password. Thunderbird lists every book; pick the ones you want.

## Addresses

The same tree answers under three prefixes. Use whichever a client likes:

| | |
|---|---|
| `/.well-known/carddav` | 301 → `/dav/` |
| `/dav/principals/<user>/` | you (also `/principals/<user>/`) |
| `/dav/addressbooks/<user>/` | your address book home |
| `/dav/addressbooks/<user>/<book>/` | one of your books (`<book>` = its uid, e.g. `contacts`) |
| `/dav/addressbooks/<user>/shared-<id>/` | a book someone shared with you (shown as "Name (Owner)") |
| `/dav/addressbooks/<user>/<book>/<name>.vcf` | one contact |
| `/remote.php/dav/principals/users/<user>/`, `/remote.php/dav/addressbooks/users/<user>/…` | Nextcloud's shape |
| `/remote.php/carddav/principals/<user>/`, `/remote.php/carddav/addressbooks/<user>/…` | Nextcloud's legacy shape (iOS Default Account) |

`<user>` may also be your email or `me`. Another person's name answers 404, so nobody learns who else is on the box.
Without credentials the server answers 401 with `WWW-Authenticate: Basic realm="Pocket Contacts"`.

Supported: OPTIONS (`DAV: 1, 3, addressbook, extended-mkcol`), PROPFIND (Depth 0/1), PROPPATCH (`displayname`,
`addressbook-description`), REPORT (`addressbook-multiget`, `addressbook-query` with prop-filter / text-match /
param-filter / is-not-defined, `sync-collection`), GET/HEAD (a book's GET is the whole book as one `.vcf`), PUT with
`If-Match` / `If-None-Match: *` (412 when stale), DELETE, MKCOL (extended, makes an address book), MOVE between your
books. Sync tokens look like `http://pocket-contacts/sync/<n>`. An unknown token answers 403 `DAV:valid-sync-token`, and
the client starts over.

## What syncs

The server keeps every contact **byte for byte as the client sent it**. That covers photos, Apple's custom labels
(`item1.X-ABLabel`), `X-ABADR`, birthdays without a year, notes, related names, Android and Nextcloud extras, and any
property this app does not know. The web app edits only the parts you change and leaves the rest of the card as it was.

- **Labels** are `CATEGORIES`, as Nextcloud, Android and Google use them.
- **Apple groups** (`X-ADDRESSBOOKSERVER-KIND:group` cards with `X-ADDRESSBOOKSERVER-MEMBER`) are stored and served like
  any card. The web app shows them as groups.
- **Shared books** carry the share's role. A read share is read-only on the phone: the server advertises read
  privileges only and refuses edits with 403. Hidden or left shares are not listed.
- **Deletes** from a phone go to Recently deleted (30 days) in the web app.
- **Several books**: DAVx5 and Thunderbird show all of them. iOS and macOS show only the first, your default book.

## Bringing contacts over (Nextcloud, iCloud, any CardDAV)

Settings → Accounts connects another address book server:

- **Nextcloud**, either through Pocket Core (your own Nextcloud login kept in Core; nothing to type) or with its address
  and an app password (Nextcloud → Settings → Security → *Create new app password*). The address may be what the
  browser shows (`https://cloud.example.com/apps/contacts/`). The system address book and "Recently contacted" are
  left out.
- **iCloud**: your Apple ID and an app-specific password (appleid.apple.com → Sign-In and Security → App-Specific
  Passwords).
- **Any CardDAV server** (Fastmail, mailbox.org, Radicale, Baïkal…): its address, user name and password.

Each address book you pick becomes an address book here. Modes:

- **Import** pulls every contact once, byte for byte, then the account switches itself off. The books stay as ordinary
  books, and later edits stay here (nothing is queued or sent). This is the way off Nextcloud: import, check the
  count, add Pocket Contacts to the iPhone as above, make it the Default Account, then remove the Nextcloud account from
  the phone.
- **Two-way** pulls and pushes. Edits made here go over with `If-Match`, and new contacts with `If-None-Match: *`. A
  contact deleted on one side is deleted on the other (and kept in Recently deleted here). When both sides changed a
  contact, the newer `REV` wins, else the newer change time. The losing version is kept in Recently deleted. A push
  that meets 412 fetches the other side first and applies the same rule.
- **Read-only** pulls only. Its books are read-only here, on the web and over CardDAV.

Syncs run on the account's interval (default 15 minutes). Local changes in two-way books go out within about 20
seconds, and *Sync now* runs one at once. If the remote server suddenly lists an address book as empty, nothing is
deleted here (that is far more often a glitch than intent). If a remote address book disappears, its copy here stays
as an ordinary book.
