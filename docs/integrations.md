# Pocket Contacts and the other Pocket apps

A contact's page shows the person in Pocket Calendar, Pocket Notes, Pocket Tasks, Pocket Projects, Pocket Forms,
Pocket CRM, Pocket Tables, Pocket Rentals and Pocket Booking
(`GET /api/v1/contacts/{id}/related`, internal/server/related.go). Contacts reaches each app **as the person**:
through a Pocket Core app link (`pcx_` token, scopes below) or a direct address + API token pasted in
Settings → Integrations. The contact's page address is `BASE_URL/contacts/<id>`; that is what notes and tasks link to.

## Pocket Calendar — `calendar:read`, `calendar:write`

| What | Request | Used from the answer |
|---|---|---|
| Meetings with the person | `GET /api/v1/search?q=<email>` for each email of the contact (up to 5) | `[Occurrence]`: `id`, `title`, `start`, `end`, `all_day`, `organizer.email`, `attendees[].email` — kept only when an email of the contact is a guest or the organiser; deduplicated by `id`; past = `end` before now |
| "New event" | `POST /api/v1/events` `{title, start, end, location, description, attendees: [{email, name}]}` (local `YYYY-MM-DDTHH:MM`, the person's zone) | the Occurrence (`id`, `title`, `start`, `end`, `all_day`) |
| Link to an event | `<calendar>/event/<occurrence id>` | |

Works with today's Calendar link routes (`GET /search` = calendar:read, `POST /events` = calendar:write).
Limitation: Calendar's search returns one (the next, else the last) occurrence per event and only events in the
person's own visible calendars — invitations from other people's calendars are not found. Nice to have in Calendar:
`GET /api/v1/search?attendee=<email>` (calendar:read) that matches attendee/organizer emails exactly and includes
accepted invitations, so Contacts does not have to filter word matches.

## Pocket Notes — `notes:read`

| What | Request | Used from the answer |
|---|---|---|
| Notes about the person | `GET /api/v1/mentions?url=<contact page>&limit=20` | `[Note]`: `id`, `title`, `excerpt` (→ `snippet`), `updated_at` |
| Link to a note | `<notes>/note/<id>` | |
| "New note" | browser link `<notes>/new?title=<name>&url=<contact page>&label=<name>&folder=People` | |

Works with today's Notes (mentions.go, link route `GET /mentions` = notes:read). No change needed.

## Pocket Tasks — `tasks:read`, `tasks:write`

| What | Request | Used from the answer |
|---|---|---|
| Open to-dos about the person | `GET /api/v1/tasks?view=all&status=open&limit=500&url=<contact page>` | `{tasks: [Task]}`: `id`, `title`, `due`, `status`, `url`, `notes` — kept when `url` is the contact page or `notes` links to it |
| "Remind me" | `POST /api/v1/tasks` `{title, due?: "YYYY-MM-DD", url: <contact page>, notes: "About [Name](<contact page>)"}` | the Task |
| Link to a task | `<tasks>/today?task=<id>` | |

**Pocket Tasks** (from its `contacts` branch on): `GET /api/v1/tasks?url=<address>` answers only the tasks whose
`url` is that address or whose notes link to it — the same rule as Pocket Notes' `itemKey`/`linksTo` (host in any
case, same path or below, own query allowed, `/contacts/12` never matches `/contacts/123`). Older Tasks versions
ignore `url=` and answer up to 500 open tasks; the local `mentionsPage` guard keeps the result right with both.

## Pocket Forms — `forms:read`

| What | Request | Used from the answer |
|---|---|---|
| Answers the person sent | `GET /api/v1/responses?email=<email>&limit=20` for each email of the contact (up to 5) | `{items: [Response]}`: `id`, `form_id`, `form_title`, `form_emoji`, `number`, `preview` (→ `snippet`), `status`, `created_at` — deduplicated by `id`, `spam` left out, newest first, at most 20 |
| Link to an answer | `<forms>/answers/<id>` | |

Works with Pocket Forms 0.1.0 (link route `GET /responses` = forms:read; `email=` matches exactly, any case).

## Pocket CRM — `crm:read`

| What | Request | Used from the answer |
|---|---|---|
| Deals with the person | `GET /api/v1/lookup?email=<email>` for each email of the contact (up to 5); the first ask also carries `&company=<org>` for a company card (its name) or a card with an organisation; a card with no email asks by `company=` alone | `{deals: [Deal], url}`: `id`, `title`, `stage_name`, `stage_color`, `status` (open/won/lost), `amount`, `currency`, `next_action`, `next_due`, `overdue`, `company`, `closed_at`, `updated_at` — deduplicated by `id`, open first (soonest next step first), then won/lost newest first, at most 30 |
| Link to a deal | `<url>/deals/<id>` (`url` from the answer, else the CRM address) | |
| Start a deal | `<crm>/deals/new?name=<name>&email=<first email>&company=<org>` (a link, nothing is sent) | |

Works with Pocket CRM 0.1.0 (link route `GET /lookup` = crm:read; `email=` matches exactly, any case).

## Pocket Tables — `tables:read`

| What | Request | Used from the answer |
|---|---|---|
| Rows about the person | `GET /api/v1/lookup?contact=<card id>&name=<full name>&email=<first email>`, then `?email=<email>` for each further email (up to 5) | `{items: [Row]}`: `id`, `title`, `table`, `base`, `field` (the field that matched: an email field, a contact field with this card picked, or a text field holding the name) — deduplicated by `id`, at most 30 |
| Link to a row | `<tables>/r/<id>` (the Tables address, else the row's `url`) | |

Works with Pocket Tables 0.1.0 (link route `GET /lookup` = tables:read; `email=` and `name=` match exactly, any case;
`contact=` is the Contacts card id a contact field keeps).

## Pocket Rentals — `rentals:read`

| What | Request | Used from the answer |
|---|---|---|
| Where the person rents from you ("Renting") | `GET /api/v1/lookup?email=<email>` for each email (up to 5) | `{tenant, leases: [Lease], url}`: `id`, `place`, `status` (active, draft, ended), `start_date`, `end_date`, `rent` and `balance` (cents; balance > 0 owed now, < 0 paid ahead), `currency` — deduplicated by `id`, active first, at most 30 |
| Link to a lease / the tenant | `<rentals>/leases/<id>`, `<rentals>/tenants/<tenant id>` (the Rentals address, else the answer's `url`s) | |

Works with Pocket Rentals 0.1.0 (link route `GET /lookup` = rentals:read; `email=` matches the tenant's email exactly, any case).

## Pocket Booking — `booking:read`, `booking:write`

| What | Request | Used from the answer |
|---|---|---|
| The times the person booked with you ("Bookings") | `GET /api/v1/lookup?email=<email>` for each email (up to 5) | `{bookings, upcoming}` (each a Booking): `id`, `title`, `emoji`, `status` (pending, confirmed, declined, cancelled, done, no_show), `start`, `end`, `minutes`, `location_kind`, `location`, `meeting_url` — deduplicated by `id`, upcoming first (soonest first), then past (latest first), at most 30. The next upcoming one that is not cancelled or declined shows on its own as "Next" |
| Which page a link is for | `GET /api/v1/types` | `{types: [Type]}`: `id`, `title`, `emoji`, `durations`, `url` — only `status: open` pages |
| "Send a booking link" | `POST /api/v1/invites` `{type_id, name, email, note, days}` (name = the card's name, email = its first email) | `{id, url, type, expires_at}` — the link is shown to copy, or to email from the person's own email app |
| Link to a booking | `<booking>/bookings/<id>` (the Booking address, else the booking's `url`) | |

Works with Pocket Booking 0.1.0 (link routes `GET /lookup`, `/types` = booking:read; `POST /invites` = booking:write;
`email=` matches the guest's email exactly, any case). The section asks for a token with both scopes, so switching
either off in Pocket Core greys the whole section.

## Other apps using Contacts

Contacts offers `contacts:read` (`GET /contacts`, `/contacts/{id}`, `/vcf`, `/photo`, `/addressbooks`, `/birthdays`,
`/search`, `/suggest`, `/lookup`, `/categories`, `/changes`) and `contacts:write` (`POST /contacts`, `PUT`/`PATCH`/
`DELETE /contacts/{id}` — only for contacts whose `source` is the acting app's slug, else 403).
It also offers `pocket:search`, `pocket:day` and `pocket:widget`, one route each (`GET /pocket/search`,
`/pocket/day/{date}`, `/pocket/widget`), which Pocket Search, Pocket Day and Pocket Hub find on their own:
[api.md](api.md#pocket-search-pocket-day-and-pocket-hub).
Core events: `contacts.created|updated|deleted` `{id, uid, fn, addressbook_id, source, user_email}` (book owner;
imports publish none per contact) and `contacts.birthday` `{id, fn, kind: birthday|anniversary, age, user_email}` once
per contact, person and day, from the person's `birthday_time` on.
