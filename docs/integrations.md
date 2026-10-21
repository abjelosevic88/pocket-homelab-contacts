# Pocket Contacts and the other Pocket apps

A contact's page shows the person in Pocket Calendar, Pocket Notes and Pocket Tasks
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

**Change needed in Pocket Tasks:** `GET /api/v1/tasks` has no `url` filter today, so it ignores `url=` and answers
with (up to 500) open tasks, which Contacts then filters itself — correct, but heavy, and tasks beyond the first 500
open ones are missed. Add to Tasks:

- `GET /api/v1/tasks?url=<address>` (scope `tasks:read`, already allowed by the `GET /api/v1/tasks` link route):
  only tasks whose `url` is that address (compared without scheme, query and trailing slash), or whose notes link to
  it — the same rule as Pocket Notes' `itemKey`/`linksTo` in mentions.go. Combine with `status=open`.
- (alternatively `GET /api/v1/mentions?url=` with the Notes answer shape, plus a `tasks:read` link route for it.)

Contacts needs no change when Tasks adds it: the request already sends `url=` and the local filter stays as a guard.

## Other apps using Contacts

Contacts offers `contacts:read` (`GET /contacts`, `/contacts/{id}`, `/vcf`, `/photo`, `/addressbooks`, `/birthdays`,
`/search`, `/suggest`, `/lookup`, `/categories`, `/changes`) and `contacts:write` (`POST /contacts`, `PUT`/`PATCH`/
`DELETE /contacts/{id}` — only for contacts whose `source` is the acting app's slug, else 403).
Core events: `contacts.created|updated|deleted` `{id, uid, fn, addressbook_id, source, user_email}` (book owner;
imports publish none per contact) and `contacts.birthday` `{id, fn, kind: birthday|anniversary, age, user_email}` once
per contact, person and day, from the person's `birthday_time` on.
