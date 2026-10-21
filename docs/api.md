# Pocket Contacts API

JSON over HTTP under `/api/v1`. Authenticate with the session cookie (web app), `Authorization: Bearer pcon_…`
(an API token from Settings → Tokens), a Pocket Core token (`pcd_`/`pcu_`/`pco_`, paired phones) or a Core
link token (`pcx_`, other Pocket apps, only the routes of their scope). Errors are `{"error": "…"}` with
400 (bad input), 401, 403 (you may see it but not change it), 404 (missing **or not yours**), 409, 412
(the contact changed meanwhile). The web app sends `X-Pocket-Source: web`.

## Shapes

**AddressBook**
```json
{"id": 1, "owner_id": 1, "uid": "contacts", "name": "Contacts", "color": "#e11d48", "description": "",
 "kind": "local", "account_id": null, "read_only": false, "is_default": true, "sort_order": 1,
 "role": "owner", "visible": true, "owner": {"id": 1, "name": "Aleksandar", "email": "a@x"},
 "contacts": 456, "shares": [Share…], "created_at": "…", "updated_at": "…"}
```
`role`: `owner | manage | write | read`. `shares` only for owner/manage. `kind`: `local | carddav | nextcloud | icloud`.
`read_only: true` (a book mirrored from a read-only account, or read-only over there) refuses edits from everyone: 403.

**Share** `{"id", "addressbook_id", "user_id": 2|null, "email": "", "everyone": false, "role": "read|write|manage", "person": {"id","name","email"}|null, "pending": false}`

**Summary** (list rows)
```json
{"id": 12, "addressbook_id": 1, "uid": "…", "kind": "individual|org|group", "fn": "Marko Petrović",
 "given": "Marko", "family": "Petrović", "nickname": "", "org": "Acme", "title": "Inženjer",
 "emails": ["marko@example.com"], "phones": ["+387 65 123 456"], "birthday": "1988-05-03|--05-03|",
 "categories": ["Posao"], "has_photo": true, "photo_url": "/api/v1/contacts/12/photo?v=ab12cd", "starred": false,
 "initials": "MP", "color": "#0ea5e9", "updated_at": "…"}
```
`emails`/`phones` are the values in card order (preferred first). `color` is a stable avatar colour from the UID.

**Card** = the structured contact (internal/vcard/contact.go):
```json
{"kind": "individual", "fn": "", "prefix": "", "given": "", "middle": "", "family": "", "suffix": "",
 "nickname": "", "phonetic_given": "", "phonetic_family": "", "org": "", "department": "", "title": "",
 "emails":    [{"label": "home|work|other|<own words>", "value": "", "pref": true}],
 "phones":    [{"label": "mobile|iphone|home|work|main|home fax|work fax|other fax|pager|other|<own>", "value": ""}],
 "addresses": [{"label": "home|work|other|<own>", "street": "", "ext": "", "pobox": "", "city": "", "region": "", "postcode": "", "country": "", "pref": false}],
 "urls":      [{"label": "homepage|home|work|other|<own>", "value": ""}],
 "ims":       [{"label": "skype|telegram|whatsapp|signal|viber|jabber|matrix|other", "value": "handle"}],
 "socials":   [{"label": "twitter|linkedin|facebook|instagram|github|mastodon|other", "value": "url or handle"}],
 "birthday": "1988-05-03 | --05-03 | ", "anniversary": "", "dates": [{"label": "", "value": "YYYY-MM-DD|--MM-DD"}],
 "related":   [{"label": "mother|father|parent|brother|sister|child|friend|spouse|partner|assistant|manager|other|<own>", "value": "Name"}],
 "note": "", "categories": ["Posao"], "members": ["uid", …]}
```
`fn` empty → the server composes the display name (prefix given middle family suffix, else org).

**Contact** (one contact) = Summary + `{"card": Card, "etag": "\"…\"", "role": "owner|manage|write|read",
"addressbook": {"id","name","color","owner": Person}, "source": "web|carddav|import|remote|<app>",
"groups": [{"uid","name"}], "url": "https://contacts.example/contacts/12", "created_at"}`.

## Address books and sharing

| | |
|---|---|
| `GET /addressbooks` | every book the person owns or that is shared with them |
| `POST /addressbooks` `{name, color?, description?}` | new local book (201) |
| `PUT /addressbooks/{id}` `{name?, color?, description?, visible?, sort_order?, is_default?}` | `visible`/`sort_order` are the person's own view and allowed for any role; the rest needs `manage` |
| `DELETE /addressbooks/{id}` | owner only; the last own book can't go |
| `POST /addressbooks/{id}/leave` | drop a share made to me (an "everyone" share is hidden instead) |
| `GET /addressbooks/{id}/shares`, `POST /addressbooks/{id}/shares` `{user_id?|email?|everyone?, role}` | manage+ |
| `PUT /shares/{sid}` `{role}`, `DELETE /shares/{sid}` | manage+ |
| `GET /addressbooks/{id}/export.vcf` | the whole book as one .vcf (read+) |
| `GET /people` | everyone on the box `[{id,name,email}]` (share picker) |

## Contacts

| | |
|---|---|
| `GET /contacts?q=&book=&category=&starred=1&kind=&has=&sort=given\|family&recent=1&limit=&offset=` | `{"contacts": [Summary], "total": n}`. Default: every visible book, no group cards, sorted by the person's `sort_by`. `category` = a label, `-` (none) or `group:<uid>`. `has` = `email\|phone\|birthday\|photo\|none`. `kind=all` includes group cards. Limit up to 5000 (the web app loads everything and filters locally; `q` searches server side). |
| `POST /contacts` `{addressbook_id?, card, starred?}` | 201 Contact; default book = settings `default_addressbook_id` else the person's default |
| `GET /contacts/{id}` | Contact; records "viewed" for Recent |
| `PUT /contacts/{id}` `{card, etag?}` | replaces the structured card (server applies the diff); `etag` → 412 when stale |
| `PATCH /contacts/{id}` `{starred?, add_categories?, remove_categories?, addressbook_id?}` | small changes; `addressbook_id` moves it (write on both books) |
| `DELETE /contacts/{id}` | 204, to Recently deleted |
| `POST /contacts/bulk` `{ids, action: "delete\|star\|unstar\|add_category\|remove_category\|move", category?, addressbook_id?}` | `{"done": n, "failed": [{id, error}]}` |
| `GET /contacts/{id}/vcf` | `text/vcard` download (`?download=1` sets a filename) |
| `GET /contacts/{id}/photo?s=96` | square JPEG thumbnail (s = 32…512, default 256; `s=0` original), `Cache-Control` + `ETag`; 404 when none |
| `PUT /contacts/{id}/photo` | body = image (jpeg/png/gif/webp) or multipart field `file`; stored as a ≤512 px JPEG → Contact |
| `DELETE /contacts/{id}/photo` | → Contact |
| `GET /contacts/{id}/related` | the contact in the other Pocket apps, see below |
| `POST /contacts/{id}/task` `{title, due?: "YYYY-MM-DD"}` | creates a task in Pocket Tasks linking to the contact → `{"item": RelatedItem}` |
| `POST /contacts/{id}/event` `{title, start: "2026-10-22T10:00", duration_min: 60, location?}` | creates an event in Pocket Calendar with the contact's first email invited → `{"item": RelatedItem}` |
| `GET /contacts/{id}/activity` | that contact's change log `[Activity]` |

**Related**
```json
{"calendar": {"status": "ok|off|error", "detail": "", "app_url": "https://calendar…",
              "items": [{"id": "77", "title": "Lunch", "start": "2026-10-22T10:00:00Z|2026-10-22", "end": "…", "all_day": false, "url": "https://calendar…/event/77", "past": false}]},
 "notes":    {"status": "ok", "detail": "", "app_url": "https://notes…", "new_url": "https://notes…/new?title=Marko%20Petrovi%C4%87&url=…&label=Marko%20Petrovi%C4%87&folder=People",
              "items": [{"id": "401450", "title": "Kickoff", "snippet": "…", "updated_at": "…", "url": "https://notes…/notes/401450"}]},
 "tasks":    {"status": "ok", "detail": "", "app_url": "https://tasks…",
              "items": [{"id": "9", "title": "Call Marko back", "due": "2026-10-23", "done": false, "url": "https://tasks…/today?task=9"}]}}
```
Every section stands alone: one app being off or down never fails the others (`status: "error"`, `detail` says why
in plain words, e.g. "you switched the Contacts → Pocket Notes link off in Pocket Core").

## Search, labels, lookups

| | |
|---|---|
| `GET /search?q=&limit=20` | `{"contacts": [Summary]}` best matches first (name prefix > other) |
| `GET /suggest?q=&limit=10` | `[{"id", "name", "email", "phone", "photo_url"}]` one row per email (then per phone), for guest/recipient pickers in other apps |
| `GET /lookup?email=…` or `?phone=…` | `{"contacts": [Summary]}` exact match (phone by its last 8 digits) |
| `GET /categories` | `{"labels": [{"name", "count"}], "groups": [{"uid", "name", "count", "addressbook_id"}]}` |
| `POST /categories/rename` `{from, to}` | renames a label on every contact the person may write → `{"changed": n}` |
| `POST /categories/delete` `{name}` | removes a label from every contact the person may write |

## Birthdays

`GET /birthdays?days=60&from=YYYY-MM-DD` → `[{"contact": Summary, "kind": "birthday|anniversary", "date": "2026-11-03",
"original": "1988-11-03|--11-03", "age": 38|null, "days": 13}]` sorted by date. `days` ≤ 366.
`GET /feed/birthdays/{feed_token}.ics` (no login, outside `/api`) → a calendar of every birthday and anniversary
(yearly all-day events). `GET /feeds` → `{"birthdays_url": "https://…/feed/birthdays/<tok>.ics", "birthdays_webcal": "webcal://…"}`;
`POST /feeds/rotate` makes a new secret.

## Duplicates

| | |
|---|---|
| `GET /duplicates` | `[{"key": "…", "reason": "same name\|same email\|same phone", "contacts": [Summary]}]` — only books the person may write |
| `POST /duplicates/merge` `{ids, primary_id?}` | merged Contact (others to Recently deleted) |
| `POST /duplicates/ignore` `{ids}` | remember these as different people |

## Import and export

`POST /import` — multipart (`file`, may repeat; `.vcf`, `.csv` from Google/Outlook/Apple/any header row, or a
`.zip` of them) or JSON `{"text": "BEGIN:VCARD…"}`; fields `addressbook_id`, `duplicates` = `merge` (default: update
the matching contact, union of fields) | `skip` | `add`, `dry_run` = `1` for a preview.
→ `{"total": 456, "created": 450, "merged": 4, "skipped": 2, "failed": 0, "errors": ["line 31: …"],
"preview": [{"fn", "emails", "phones", "action": "create|merge|skip", "match_id": 12|null}] (dry run, first 200)}`.
vCard 2.1 (quoted-printable, Windows-1250) is upgraded to 3.0; photos are kept.

`GET /export.vcf?book=&ids=1,2,3` → `text/vcard` of the person's visible contacts (or the given ones).

## Recently deleted, activity, changes

`GET /deleted` → `[{"id", "addressbook_id", "uid", "fn", "deleted_at"}]`; `POST /deleted/{id}/restore` → Contact;
`DELETE /deleted/{id}`. `GET /activity` → `[{"id", "addressbook_id", "contact_id", "action", "summary", "detail", "created_at"}]`.
`GET /changes` → `{"stamp": n}`; the web app polls it every 10 s and reloads when it moves.

## Phones and other devices

`GET /devices` → `{"carddav_url": "https://…/dav/", "username": "aleksandar", "ios_server": "contacts.example/remote.php/carddav/principals/aleksandar/",
"principal_url": "https://…/dav/principals/aleksandar/", "tokens": [APIToken whose name starts with "Device: "]}`.
`POST /devices` `{name: "iPhone"}` → `{"token": "pcon_…", "profile_url": "https://…/dav-profile/<code>.mobileconfig", "expires_in": 900}`:
a new API token named "Device: iPhone" plus a one-time link (15 minutes, no login) to an Apple configuration
profile that sets up the CardDAV account with the legacy principal path (so it shows up under Default Account).
The web app shows the link as a QR code to scan with the iPhone camera. The answer also carries `"device": APIToken`.
`DELETE /devices/{id}` revokes a device's token (204; 404 for a token that is not a device's).
See docs/carddav.md for the URL shapes and how to connect each client.

## External accounts (sync)

| | |
|---|---|
| `GET /accounts` | `[Account]` (`{id, kind, name, url, username, via, mode, enabled, sync_minutes, last_sync_at, last_error, has_secret, addressbooks, addressbook_list: [{id, name, color, remote_href, read_only, contacts}], syncing, created_at, updated_at}`); `GET /accounts/{id}` one |
| `GET /accounts/nextcloud` | `{"core": true\|false, "detail": "", "url": "https://cloud…", "username": "…"}` — whether Pocket Core has a Nextcloud login for me |
| `POST /accounts/discover` `{kind, via, url?, username?, password?}` | `{"addressbooks": [{"href", "name", "count", "read_only"}], "name": "Nextcloud"}` without saving; Nextcloud's system and "Recently contacted" books are left out; a failed sign-in is 400 in plain words |
| `POST /accounts` `{kind: "nextcloud\|carddav\|icloud", via: "core\|direct", url?, username?, password?, mode: "import\|two_way\|read_only", hrefs?: […], name?, sync_minutes?}` | 201 Account; the books appear at once, the first sync starts in the background. `mode` defaults to `import`; no `hrefs` = every book discover lists; `icloud` needs no url (Apple ID + app-specific password); 409 when the same account is there already |
| `PUT /accounts/{id}` `{name?, password?, mode?, enabled?, sync_minutes?}` | `mode` may also be `off`. Leaving `two_way` drops unsent changes; entering it queues contacts made here and edits since the last sync (deletes made meanwhile are not replayed) |
| `DELETE /accounts/{id}?keep=1` | `keep=1` keeps the mirrored books as local ones |
| `POST /accounts/{id}/sync` | sync now → Account (after the sync, up to 60 s; `syncing: true` if it goes on); 400 when the account is off |

Modes: `import` pulls everything once (vCards byte for byte, the remote mapping kept), then the account switches itself to
`off` and its books are ordinary books (edits stay here, nothing is queued). `two_way` pulls and pushes. `read_only` pulls
only and its books are `read_only` here. `last_error` says what went wrong in plain words.

## Settings, integrations, people

`GET /settings` → `{"settings": {"title", "sort_by": "given|family", "display_order": "given_first|family_first",
"default_addressbook_id", "density", "phone_region", "notify_kind", "notify_url", "birthday_notify", "birthday_ahead",
"birthday_time", "anniversary_note", "integrations", "integration_urls", "integration_tokens"}, "notify_has_token",
"notify_box_route", "core"}`; `PUT /settings` takes the same keys (plus `notify_token`). `POST /notify/test`.
`GET /integrations` → `[{"id": "calendar|notes|tasks", "name", "description", "scopes", "mode": "core|direct|off",
"url", "has_token", "status": "ok|off|error", "detail", "public_url"}]`; `PUT /integrations/{id}` `{mode?, url?, token?}`.
Auth, users, tokens, backup, `/settings/system`: same as the other Pocket apps (`/auth/status`, `/auth/login`,
`/auth/setup`, `/auth/oidc/start`, `/auth/me`, `/users`, `/tokens`, `/backup`).
