# Configuration

Everything is an environment variable. Nothing is required: `docker run` with one volume works.

| Variable | Default | What it does |
|---|---|---|
| `PORT`, `HOST` | `8080`, `0.0.0.0` | Where to listen |
| `BASE_URL` | | The public address (`https://contacts.example.com`). Needed for contact links (what notes and tasks point at), the CardDAV and feed addresses shown in Settings, the iPhone profile and OIDC |
| `DATA_DIR` | `./data` | Database, `secret.key`, backups |
| `DB_PATH` | `$DATA_DIR/pocket-contacts.db` | |
| `TZ` | `UTC` | The box's time zone (when birthday reminders go out) |
| `LOG_LEVEL`, `LOG_FORMAT` | `info`, `text` | `debug` logs every request; `json` for log shippers |
| `TRUST_PROXY` | `true` | Read the client address from `X-Forwarded-For` |
| `METRICS_ENABLED` | `true` | `/metrics` for Prometheus |
| `AUTH_MODE` | `local` | `local` (accounts and passwords), `proxy` (trust `AUTH_PROXY_HEADER`), `none` (everyone is the admin; demos only) |
| `AUTH_PROXY_HEADER`, `AUTH_PROXY_EMAIL_HEADER` | `Remote-User`, `Remote-Email` | For `AUTH_MODE=proxy` |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD`, `ADMIN_NAME` | | Create the admin on first start instead of the setup screen |
| `SESSION_TTL` | `720h` | |
| `SECURE_COOKIES` | `false` | Set behind HTTPS |
| `OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_NAME`, `OIDC_AUTO_CREATE` | | Sign in with any OpenID Connect provider. Not needed with Pocket Core |
| `CORE_URL`, `CORE_REGISTRATION_KEY`, `CORE_APP_URL` | | Pocket Core: registration, sign-in, links to Pocket Calendar/Notes/Tasks, the person's Nextcloud login for sync, events |
| `NOTIFY_URL`, `NOTIFY_KIND` | | The owner's default route for birthday reminders: an Apprise route (`http://apprise:8000/notify/contacts`), an ntfy topic URL or a webhook; `apprise` / `ntfy` / `webhook`. Everyone else sets their own in Settings |
| `DEMO_DATA` | `false` | Seed made-up contacts into an empty box |

## Commands

```
pocket-contacts                       start
pocket-contacts backup [-o FILE]      consistent SQLite copy (VACUUM INTO)
pocket-contacts reset-password [-email USER] [-password NEW]
pocket-contacts version
```

## Files in the volume

- `pocket-contacts.db` (+ `-wal`, `-shm`): everything. Back it up with `pocket-contacts backup`, never by copying a live file.
- `secret.key`: seals the passwords of connected CardDAV / Nextcloud accounts. Back it up with the database; without it those accounts must be connected again.
- `core.token`, `core.token.oidc.json`: the app's Pocket Core credentials.
- `backups/`: snapshots made by the backup command.
