# Messenger on Go and MongoDB

[![CI](https://github.com/hdmtrn/nosql-messenger/actions/workflows/ci.yml/badge.svg)](https://github.com/hdmtrn/nosql-messenger/actions/workflows/ci.yml)

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

Compose brings up a standalone MongoDB, Redis without persistence, and **two** application
replicas on ports 8080 and 8081. Two replicas are deliberate: anything that only works inside
one process fails here rather than in production. There is no load balancer in front of them
on purpose — two browsers on two ports make it obvious which node answered.

The frontend in dev mode:

```bash
npm run dev --prefix web
```

Vite serves the page on 5173 and proxies the API. It rewrites `Host`, so the WebSocket
origin check refuses the socket unless 5173 is listed in `WS_ALLOWED_ORIGINS` — REST keeps
working while realtime events silently do not.

### A volume from the replica-set days

MongoDB used to run as a single-node replica set with a one-shot `mongo-init` container. A
data volume created then still starts, but mongod keeps the TTL monitor off for it, so expired
sessions are never removed, and nothing reports it. Bring the stack up without the old
container, then clear the leftover configuration once:

```bash
docker compose up -d --build --remove-orphans
```

```bash
docker compose exec mongo mongosh --quiet --eval 'db.getSiblingDB("local").dropDatabase()'
```

```bash
docker compose restart mongo
```

Rebuild before running the tests against an old stack: a replica set advertises itself as
`mongo:27017`, which does not resolve from the host, so the tests cannot reach it and skip.

## Tests

```bash
go test -race -short ./...   # everything that needs no database
go test -race ./...          # the full run, with MONGO_URI and REDIS_ADDR set
```

The database-backed tests need MongoDB and Redis. Start them the way CI does:

```bash
docker compose up -d --wait mongo redis
```

`CI=true` makes those tests fail when the database is unreachable instead of skipping —
a skipped test looks like a passed one. `-race` matters for the hub: concurrent access to it
is exactly the kind of bug that reading the code does not find.

Locally MongoDB and Redis run without passwords. CI sets `MONGO_ROOT_USERNAME`,
`MONGO_ROOT_PASSWORD`, `MONGO_USERNAME`, `MONGO_PASSWORD` and `REDIS_PASSWORD`, and the same
compose file then starts both with a login, the way production runs. Set them only for a
fresh data volume: on an existing one the mongo image turns `--auth` on but creates no user,
and nothing can log in.

CI runs four independent jobs on every push: `go vet` plus a `gofmt` check, the short test
run, the full run against a real MongoDB and Redis, and a throwaway Docker image build.

## Profiling and readiness

Each node serves `net/http/pprof` on `127.0.0.1:9090`, reachable only from inside its own
container: compose publishes nothing for it. Take a profile there and read it on the host:

```bash
docker compose exec --index 1 app wget -qO- 127.0.0.1:9090/debug/pprof/heap > heap.pb.gz
```

```bash
go tool pprof -top heap.pb.gz
```

A CPU profile is `/debug/pprof/profile?seconds=30`; `--index 2` picks the other node.

The same listener answers `/readyz`: whether the node can write to MongoDB and reach Redis,
with the error of whichever cannot. The public `/healthz` only says the process serves.

```bash
docker compose exec --index 1 app wget -qO- 127.0.0.1:9090/readyz
```

## Rate limits

Counted in Redis, so both nodes share them; a 429 says in `Retry-After` when to come back,
and the client waits that long and sends again.

| What | Limit |
|---|---|
| login, per address | 100 attempts a minute |
| login, per account and address | 10 failures in 15 minutes; logging in clears it |
| login, per account | 100 attempts an hour, from all addresses together |
| registration, per address | 50 an hour |
| messages, per user | 30 in 10 seconds, forwards included |
| uploads, per user | 60 a minute, avatars included |

The address is the connection's. Behind a load balancer set `TRUST_PROXY=true`, and the
last `X-Forwarded-For` entry is used instead: the balancer appends the address that
connected to it, and anything before that comes from the client. An IPv6 address counts by
its /64. Login attempts are counted before the password is checked, so a burst of parallel
attempts gets no more tries than the limit. With Redis down the
limits let everything through.

## Privacy notice and terms of use

`/privacy` and `/terms` are rendered from `server/legal/`. The texts name whoever runs the
instance, and that comes from the environment rather than the repository:

```bash
OPERATOR_NAME="Your Name" OPERATOR_EMAIL="you@example.com" docker compose -p thesis up -d
```

Without both the pages say that no operator has been set, and the server logs it at start.
The texts describe the deployment this repository is built for, hosted in Frankfurt with the
Hungarian authority named for complaints; anyone running it elsewhere has to change them.

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
