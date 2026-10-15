# Lets meet up

A Go server and map frontend. Posting a guest location to `/meetups/<meetup>` updates that guest and returns the guests in the meetup. The JSON body supplies the meetup ID, as in the existing frontend.

## Run

Use Go 1.27.2 and a PostgreSQL-compatible database, including CockroachDB. Create the database first and supply its connection string through `DATABASE_URL`:

```sh
export DATABASE_URL='postgresql://USER:PASSWORD@HOST:26257/DATABASE?sslmode=verify-full'
go run .
```

Use the full connection string supplied by your database provider, including its TLS settings and certificate parameters. Keep credentials outside Git. The server listens on port 8080 and serves `static/` from its working directory, so run it from the repository root.

Startup verifies connectivity and creates the `guests` table if missing. The database user needs permission to create tables in its default schema and to read/write the guests table. Guests are identified by `(meetup_id, guest_id)`; updates replace the name and coordinates, and responses list only the selected meetup's guests, ordered by guest ID. The frontend's JSON fields are unchanged, and responses omit the internal document ID.

This starts fresh; existing Cosmos data is neither imported nor deleted.

The bottom search field is shared per meetup. `GET /meetup-search/<meetup>` returns
`{"query":"food"}` until a search is saved. `PUT` to the same URL with
`{"query":"coffee"}` saves the search in the `meetup_searches` table, which is
created automatically at startup. Searches must contain 1–200 characters.
Anyone with the meetup link can change its search; the latest save wins, and
other visitors see it when opening or reloading the page. The database value
takes precedence over any old `query` URL parameter.

## Verify

```sh
go build ./...
go test ./...
go vet ./...
TEST_DATABASE_URL='postgresql://USER:PASSWORD@HOST:26257/TEST_DATABASE?sslmode=verify-full' go test -run TestGuestStoreIntegration -v .
docker build -t letsmeetup:local .
```

Use a disposable database for integration tests. They verify startup table creation, repeated initialization, insertion, updates, meetup isolation, ordering, and persistence across connection pools. Tests remove their own guest rows afterward.

## Container and Kubernetes

```sh
docker run --rm -p 8080:8080 --env DATABASE_URL letsmeetup:local
```

`deploy.yaml` reads `DATABASE_URL` from the `connection-string` key of the `roachmotel-credentials` Kubernetes Secret in the `letsmeetup` namespace. The connection string uses `sslmode=require` for TLS without certificate verification; the deployment needs no certificate mounts. The deployment image is pinned to the SQL migration commit. Update its tag when publishing a new application build.

`bash build.sh` builds and **pushes** `paulgmiller/letsmeetup:<short-commit>` from the repository root. It does not deploy the image.
