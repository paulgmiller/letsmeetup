# Let’s meet up

A small Go server, SQL database, and plain JavaScript page. Open a meetup, share a
location snapshot or choose a pin, and send the link to a friend. The map suggests
three places near the center of the shared locations, with links to directions.
No accounts, frontend framework, package installation, or frontend build step.

## Run

Use Go 1.27.2 and a PostgreSQL-compatible database, including CockroachDB. Create
the database first and supply its connection string through `DATABASE_URL`:

```sh
export DATABASE_URL='postgresql://USER:PASSWORD@HOST:26257/DATABASE?sslmode=verify-full'
go run .
```

Use your provider’s full connection string, including its TLS settings and
certificate parameters. Keep credentials outside Git. The server listens on
port 8080. HTML, CSS, and JavaScript are embedded in the binary, so a compiled
binary can run from any directory. Rebuild after changing frontend files.

Serve the app over HTTPS in production. Geolocation requires a secure browser context; `http://localhost:8080` also works for local use.
The identity cookie becomes Secure on HTTPS or behind a trusted reverse proxy
that sets `X-Forwarded-Proto: https`.

The browser key in `static/index.html` must belong to a Google Cloud project with
**Maps JavaScript API and Places API (New)** enabled and billing configured. Keep
its website and API restrictions appropriate for the deployed origin. The UI
uses the new Place text search and Advanced Markers, with Google’s `DEMO_MAP_ID`;
replace that map ID with your project’s map ID if you want custom map styling.
If Places API (New) is disabled on an older project, the app falls back to its
already-enabled original Places API for that page session. It does not fall back
for quota or other permission errors. Enable Places API (New) to use the modern
search API on the next page load. See Google’s
[Places migration guide](https://developers.google.com/maps/documentation/javascript/legacy/places-migration-overview).

## Meetup behavior

- New links use 22-character random tokens (128 bits); existing `?m=` and `?meetup=` links still open.
- Opening a link requests your location and automatically joins with a snapshot
  once the browser allows access, including when permission was previously granted.
  An existing participant’s name is preserved. Denying access leaves the map and
  manual pin selection available.
- “Use my location” shares one snapshot. “Choose on map” lets you pan, zoom, and
  tap a location instead. The optional name can be edited after joining.
- Visible pages refresh participants and the shared search every 12 seconds,
  and when returning to the tab. Polling never updates a location. Place searches
  run only when meetup state changes or the user submits a search.
- “Leave meetup” deletes your location from that meetup. A private HttpOnly,
  SameSite cookie identifies your browser; public participant IDs cannot be used
  to overwrite or remove someone else. Clearing cookies loses this identity.
- Anyone with the invite link can see locations and change the shared search.
- Meetups expire 24 hours after their first API access; visits and edits do not
  extend that deadline. Expired API requests return 410, and open pages clear
  locations and disable edits at expiry. Start a new meetup for a new link.
- A startup and hourly cleanup deletes expired locations, names, and searches
  (within an hour while the server is running). Small expiry records remain so
  expired links cannot recreate a meetup. Backups follow the database’s retention.

Startup creates the `meetups`, `guests`, and `meetup_searches` tables if needed.
The database user needs table creation and read/write permissions. Existing SQL
meetups get a one-time 24-hour lifetime on the first startup with this version.
Existing guest rows remain visible until expiry, but old `GuestId` browser cookies
are not imported as credentials; a returning visitor joins again when location access is allowed.
Existing Cosmos data is neither imported nor deleted.

## API

All meetup endpoints validate the URL’s meetup ID and use it as the room ID.
Responses are not cacheable. Active rooms include an `X-Meetup-Expires` response
header. An empty search defaults to `food` until a search is saved.

| Endpoint | Behavior |
| --- | --- |
| `GET /meetups/<id>` | List participants without joining. |
| `POST /meetups/<id>` | Save this browser’s snapshot: `{"GuestName":"Sam","Lat":47.6,"Long":-122.3}`; return participants. |
| `DELETE /meetups/<id>` | Remove this browser’s snapshot; return 204. |
| `GET /meetup-search/<id>` | Read the shared `{"query":"food"}`. |
| `PUT /meetup-search/<id>` | Save a trimmed search of 1–200 characters. |

Participant responses retain `MeetupId`, `GuestId`, `GuestName`, `Lat`, and `Long`.
`GuestId` is a public label; the server ignores caller-supplied guest identities.
`X-Guest-ID` identifies this browser’s public label. Names are limited to 60
characters and coordinates are required and range checked. Request bodies are
limited to 4 KiB. Request logs omit invite URLs and identity credentials.

## Verify

```sh
go build ./...
go test ./...
go vet ./...
node tests/frontend.test.cjs
TEST_DATABASE_URL='postgresql://USER:PASSWORD@HOST:26257/TEST_DATABASE?sslmode=verify-full' go test -run TestGuestStoreIntegration -v .
docker build -t letsmeetup:local .
```

Use a disposable database for integration tests. They cover initialization,
updates, meetup isolation, persistence, read-only lists, leaving, expiry, and
cleanup without deleting active meetups. Tests remove their own rows afterward.
The dependency-free frontend tests cover automatic and manual joining, polling, map pin
selection, stale responses, directions, leaving, expiry, and sharing fallbacks.

For a browser smoke check, open the same link in two separate browser profiles.
Join in each, confirm that both participants appear within 12 seconds, change the
shared search, and leave in one profile. Check a narrow mobile viewport and deny
location permission to try pin selection. Live place results require the Google
Cloud configuration described above.

## Container and Kubernetes

```sh
docker run --rm -p 8080:8080 --env DATABASE_URL letsmeetup:local
```

`deploy.yaml` reads `DATABASE_URL` from a Kubernetes Secret in the `letsmeetup`
namespace. Keep its Secret reference aligned with your cluster. The process uses
HTTP timeouts and drains requests for up to 10 seconds on shutdown.

`bash build.sh` builds and **pushes** `paulgmiller/letsmeetup:<short-commit>` from
the repository root. It does not deploy the image. Update the deployment’s image
tag when publishing a new application build.
