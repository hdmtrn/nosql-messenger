# Messenger on Go and MongoDB

A real-time group messenger built on a document database: Go on the backend, MongoDB for
storage, Redis Pub/Sub as the bus between application instances, Vue 3 on the frontend,
WebSocket for delivery, Docker Compose for the whole environment. Written as a university
thesis project, which is why the repository carries measurements and written-out reasoning
rather than the shortest path to a working chat.

The interesting parts are the ones a document database forces you to decide:
what the message collection looks like when an array inside the channel document is not an
option, how a cursor paginates when thousands of documents share a timestamp, and how
fan-out survives a second instance. `docs/architecture.md` covers those decisions and their
cost; `docs/diagrams/` shows the same system as diagrams derived from the code.

## Running it

```bash
docker compose up -d --build
```

Compose brings up MongoDB as a single-node replica set, a one-shot container that initiates
it, Redis without persistence, and **two** application replicas on ports 8080 and 8081. Two
replicas are deliberate: anything that only works inside one process fails here rather than
in production. There is no load balancer in front of them on purpose — two browsers on two
ports make it obvious which node answered.

The frontend in dev mode:

```bash
npm run dev --prefix web
```

Vite serves the page on 5173 and proxies the API. It rewrites `Host`, so the WebSocket
origin check refuses the socket unless 5173 is listed in `WS_ALLOWED_ORIGINS` — REST keeps
working while realtime events silently do not.

Connecting to MongoDB from the host needs `?directConnection=true`. The replica set is
initiated as `mongo:27017`, and without the flag the driver follows the advertised topology,
fails to resolve that name and hangs until the server selection timeout.

## Tests

```bash
go test -race -short ./...   # everything that needs no database
go test -race ./...          # the full run, with MONGO_URI and REDIS_ADDR set
```

The database-backed tests need an **initiated** replica set rather than a standalone
`mongod`, because the code uses transactions. Start it the way CI does:

```bash
docker compose up -d --wait mongo redis
docker compose run --rm mongo-init
```

`CI=true` makes those tests fail when the database is unreachable instead of skipping —
a skipped test looks like a passed one. `-race` matters for the hub: concurrent access to it
is exactly the kind of bug that reading the code does not find.

CI runs four independent jobs on every push: `go vet` plus a `gofmt` check, the short test
run, the full run against a real replica set, and a throwaway Docker image build.

## Layout

| Path | What is in it |
|---|---|
| `server/` | The Go backend: HTTP handlers, the WebSocket hub, the Redis bus, presence, storage |
| `web/` | The Vue 3 client |
| `docs/architecture.md` | The design decisions and the invariants the code depends on |
| `docs/diagrams/` | Architecture and sequence diagrams as Mermaid sources with rendered SVG |

The backend keeps a flat layout on purpose: files next to each other rather than
`internal/domain/entity/repository`, split into packages only when a file grows past the
point where the seam is obvious.

## License

MIT — see [LICENSE](LICENSE).
