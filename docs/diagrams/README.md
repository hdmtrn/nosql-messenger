# Architecture diagrams

Every diagram here is derived from the code on `main`, not from a design document. Names in
the diagrams are the names in the source: Go functions and types, Redis key and channel
names, HTTP routes, WebSocket event types. Every source path quoted below
(`server/bus.go`, `web/src/api.js`, and so on) is relative to the repository root.

Each `.mmd` file is also rendered to an `.svg` next to it. To re-render after an edit,
from this folder:

```bash
mmdc -i system.mmd -o system.svg
```

The Mermaid block under each heading is a copy of the matching `.mmd` file — editing one
means editing the other. The diagrams carry identifiers only; the reasoning behind each
step is in the **Reading it** list under the diagram, keyed by the label the step carries
in the diagram rather than by its number — numbers shift whenever a step is added.

## How multi-server delivery works

The backend runs as **two instances of the same binary** (`docker-compose.yml`,
`deploy.replicas: 2`, published on host ports 8080 and 8081). There is deliberately
**no load balancer**: the compose file says a round-robin proxy would hide which node
answered, and seeing that is the point of running two.

Each instance holds its own `Hub` — two maps, `byChannel` and `byUser`, under one mutex —
which knows only about the WebSocket sockets **open on that process**. Nothing about the
hub is shared. What ties the instances together is Redis, in two distinct roles.

### 1. Redis Pub/Sub as the fan-out bus

`server/bus.go`. Three kinds of topic:

| Topic | Payload | Subscribed |
|---|---|---|
| `ch:{channelID}` | a stored `Message` as JSON, or `typingEvent` / `presenceEvent` / `profileEvent`, told apart by a `type` field a message never has | dynamically, while this node has at least one local socket reading that channel |
| `session-revoked` | a session `ObjectID` hex — never the token | permanently, from `newBus` |
| `subscription` | `subscriptionChange{user_id, channel_id, subscribed}` | permanently, from `newBus` |

The delivery path is always the same, and it always goes through Redis — even when
sender and recipient are on the same instance:

```
handler → bus.Publish(chID, payload) → Redis PUBLISH ch:{chID}
        → every subscribed instance's bus.Run → Hub.Publish → Subscriber.send → writePump → browser
```

Publishing to Redis rather than short-cutting to the local hub buys two things, both
stated in `bus.go`: every node sees messages in one order, and no node has to recognise
and filter the echo of its own event. The sender's own socket receives its message back
like everyone else's; the client ignores it because `client_msg_id` is already in the feed.

A node subscribes to `ch:{id}` only while somebody connected *there* reads that channel.
`Hub.attach` calls `watch` on the first local reader, `Hub.drop` and `Hub.Unsubscribe`
call `unwatch` on the last one. Those run under the hub's mutex and must never block, so
they only record the channel in `bus.pending` — a **set**, `map[string]bool`, plus a
one-slot `wake` channel — and `bus.Run` drains the whole set in `applyWatches` with one
`SUBSCRIBE` and one `UNSUBSCRIBE`.

It used to be a 256-slot queue, and that was a real hole (#23). `watch` fires only on the
transition into `byChannel`, so a note dropped on overflow was never reissued and the node
went deaf on that channel for as long as any local reader stayed. `Connect` enqueues one
note per channel synchronously under the mutex while the consumer paid a round trip each,
so the buffer had to hold the entire burst. Measured against a live Redis: four sockets of
200 channels each left **373 of 800 channels without a `SUBSCRIBE`**. A restart is exactly
what produces it — every client reconnects at once against a cold node. The set collapses
repeats, so it is bounded by the node's own channels and nothing is dropped.

**The bus is the fast path, never the guarantee.** A message is written to MongoDB
*before* it is announced (`deliverMessage`). If no node is subscribed, or Redis is down,
the event is lost and the message is still in the database — the client recovers it with
`GET /messages` on the next reconnect (`backfill()` in `Messenger.vue`).

### 2. Redis as the presence store

`server/presence.go`. Separate mechanism, its own keys, no Pub/Sub:

| Key | Type | Meaning |
|---|---|---|
| `presence:sockets:{userID}` | SET of `{nodeID}:{socketID}` | the user's open sockets — the node is embedded in the member, so this set also says *where* they are |
| `presence:node:{nodeID}` | SET of `{userID}:{nodeID}:{socketID}` | the same pairs indexed by node, so a dead node's sockets can be found |
| `presence:version:{userID}` | counter (`INCR`) | events about one user come from different nodes and Pub/Sub keeps no order between publishers, so the version says which is newer |
| `presence:alive:{nodeID}` | string, 30 s TTL | a node's pulse, rewritten every 10 s |
| `presence:nodes` | SET | the registry to look for a missing pulse in |
| `presence:sweeper` | `SETNX`, 10 s TTL | held for one round by the node cleaning up after the dead ones |
| `presence:epoch` | `SETNX` unix millis | a version means something only within one life of Redis — the epoch says which life |

A user is online while they hold a socket on a node that is still beating. `onlineIn`
ignores sockets belonging to nodes whose pulse has expired, so a read is correct as soon
as the pulse dies, without waiting for the sweep. When a node dies for real, one surviving
node wins `presence:sweeper`, claims the dead node with `SREM presence:nodes` (Redis is
single-threaded, so exactly one caller sees it removed), and closes its orphaned sockets
the way their own node would have.

### What Redis is not used for

Sessions live in MongoDB with a per-process in-memory cache — Redis only carries the
invalidation. There is no rate limiting in Redis (the typing limits are counters on the
connection struct), and no cache of MongoDB documents. This is a rule rather than an accident:
nothing durable goes into Redis, which runs with `--save '' --appendonly no`.

---

## system.mmd

The whole system: the Vue client, the two Go instances, Redis and MongoDB, showing which
traffic is HTTP, which is WebSocket, and which goes through Redis. There is no proxy in
the picture because there is none in the deployment.

```mermaid
%% Whole system: Vue client, two Go instances, Redis, MongoDB.
%% Exact Redis key and topic formats are in the README tables.
%% Sources: docker-compose.yml, Dockerfile, server/main.go, server/server.go,
%% server/bus.go, server/presence.go, web/src/api.js, web/src/socket.js
flowchart LR
  subgraph client["Browser"]
    vue["Vue 3 app<br/>web/dist"]
    apijs["api.js<br/>fetch"]
    wsjs["socket.js<br/>WebSocket"]
    vue --> apijs
    vue --> wsjs
  end

  subgraph app["service 'app' — replicas: 2, no proxy in front"]
    subgraph n1["instance 1 — host port 8080"]
      mux1["routes()<br/>http.ServeMux"]
      hub1["Hub<br/>this process only"]
      bus1["bus<br/>redis.PubSub"]
      pres1["presence<br/>nodeID"]
      st1["7 stores"]
      mux1 --> st1
      mux1 --> bus1
      mux1 --> hub1
      hub1 <--> bus1
      mux1 --> pres1
    end
    subgraph n2["instance 2 — host port 8081"]
      mux2["routes()"]
      hub2["Hub"]
      bus2["bus"]
      pres2["presence"]
      st2["7 stores"]
      mux2 --> st2
      mux2 --> bus2
      mux2 --> hub2
      hub2 <--> bus2
      mux2 --> pres2
    end
  end

  redis[("Redis 8.0<br/>no persistence")]
  mongo[("MongoDB 8.0<br/>replica set rs0")]

  apijs -->|"HTTP REST"| mux1
  wsjs -->|"WebSocket /ws"| mux1
  apijs -.->|"second browser, by hand"| mux2
  wsjs -.-> mux2
  mux1 -->|"GET / serveWeb"| vue

  bus1 <-->|"PUBLISH / SUBSCRIBE<br/>ch:{channelID}<br/>session-revoked<br/>subscription"| redis
  bus2 <--> redis
  pres1 <-->|"presence:* keys<br/>sockets, node, version<br/>alive, nodes, sweeper, epoch"| redis
  pres2 <--> redis

  st1 -->|"users, sessions, channels,<br/>messages, friend_requests,<br/>invites, media, GridFS"| mongo
  st2 --> mongo
```

**Reading it**

- The Go process serves the built frontend itself: `GET /` falls through to `serveWeb`,
  which returns `web/dist/index.html` for any path that is not a file on disk, because
  `/invite/{code}` is a client-side route.
- The dashed edges to instance 2 are dashed for a reason: nothing routes a client there.
  A second browser is pointed at `http://localhost:8081` by hand. In dev, Vite proxies
  everything to `:8080` only (`web/vite.config.js`).
- Both instances talk to the same `redis.Client` config; `presence` is constructed with
  `bus.rdb`, so the bus and the presence store share one connection pool.
- `GET /health` pings MongoDB and nothing else, so a node whose Redis is gone — no
  fan-out, no presence — still reports `{"status":"ok"}`.

**Based on:** `docker-compose.yml`, `Dockerfile`, `server/main.go`, `server/server.go`, `server/bus.go`, `server/presence.go`, `server/health.go`, `web/src/api.js`, `web/src/socket.js`, `web/vite.config.js`

---

## backend-architecture.mmd

Inside one Go instance, grouped by layer: entry and middleware, HTTP handlers, the
WebSocket hub and the Redis bus, the two services, the seven stores, and the two adapters.

```mermaid
%% Inside one Go instance (package main, messenger/server), grouped by layer.
%% Sources: server/main.go, server/server.go, server/middleware.go, server/auth.go,
%% server/ws.go, server/hub.go, server/bus.go, server/typing.go, server/presence.go,
%% server/presence_nodes.go, server/*_http.go, server/users.go, server/sessions.go,
%% server/channels.go, server/messages.go, server/friends.go, server/invites.go,
%% server/media.go, server/mongo.go
flowchart LR
  subgraph entry["Entry — main.go, server.go, middleware.go"]
    run["run(ctx)<br/>builds the stores, Hub,<br/>bus, presence"]
    routes["routes()<br/>http.ServeMux"]
    logging["withLogging<br/>responseRecorder"]
    reqauth["requireAuth<br/>sessions.ByToken"]
    wire["wireRevocation<br/>onRevoked / onSessionRevoked"]
    run --> routes
    routes --> logging
    routes --> reqauth
    run --> wire
  end

  subgraph handlers["Handlers — *_http.go, auth.go, health.go"]
    hauth["handleRegister / handleLogin<br/>handleLogout / handleMe"]
    husers["handleSearchUsers, handleGetUser,<br/>handleUpdateProfile, handleSetAvatar,<br/>handleDeleteAvatar, announceProfile"]
    hsess["handleListSessions<br/>handleRevokeSession"]
    hch["handleCreateChannel, handleListChannels,<br/>handleGetChannel, handleOpenDirect,<br/>handleLeaveChannel,<br/>handleChannelsInCommon,<br/>handleSetChannelAvatar"]
    hinv["handleCreateInvite, handleListInvites,<br/>handleRevokeInvite, handleFollowInvite"]
    hmsg["handleSendMessage,<br/>handleForwardMessage,<br/>handleListMessages,<br/>deliverMessage"]
    hfr["handleSendFriendRequest,<br/>handleListFriendRequests,<br/>handleAcceptFriendRequest,<br/>handleDeclineFriendRequest,<br/>handleListFriends"]
    hmedia["handleUploadMedia,<br/>handleGetMedia, saveUpload"]
    hpres["handlePresence<br/>visibleTo"]
    hhealth["handleHealth — mongo.Ping"]
    hweb["serveWeb — SPA fallback"]
  end

  subgraph realtime["WebSocket and fan-out — ws.go, hub.go, typing.go, bus.go"]
    hws["handleWS<br/>Upgrade, then sessions.ByToken"]
    sub["Subscriber<br/>send chan, socketID"]
    pumps["readPump / writePump<br/>ping 30s, pong 60s"]
    hub["Hub<br/>Connect, Publish, Subscribe,<br/>Unsubscribe, CloseSession, Reads"]
    frame["handleFrame / typing<br/>clientFrame type 'typing'"]
    pubif["publisher interface<br/>Publish, Subscribe,<br/>Unsubscribe"]
    busrun["bus.Run(ctx)<br/>one PubSub per process<br/>pending set + wake, applyWatches"]
    hws --> sub
    hws --> hub
    sub --> pumps
    pumps --> frame
    frame --> hub
    hub -->|"watch/unwatch into the pending set"| busrun
    busrun -->|"hub.Publish(chID, payload)"| hub
  end

  subgraph services["Services — auth.go, presence.go, presence_nodes.go"]
    authsvc["auth<br/>argon2, sem(6), dummyHash"]
    pres["presence<br/>connect, disconnect, remove, lookup,<br/>beat, listen, sweeping, claim"]
    presloop["runPresence / presenceRound<br/>sweepNode, reregister"]
    announce["socketOpened / socketClosed<br/>announcePresence, markLastSeen"]
    presloop --> pres
    announce --> pres
  end

  subgraph repos["Repositories — one Mongo collection each"]
    ustore["userStore — users"]
    sstore["sessionStore — sessions<br/>+ in-process cache"]
    cstore["channelStore — channels"]
    mstore["messageStore — messages"]
    fstore["friendStore — friend_requests"]
    istore["inviteStore — invites"]
    mdstore["mediaStore — media, GridFS"]
  end

  subgraph adapters["Adapters"]
    mgo["connectMongo<br/>mongo.Client"]
    rdb["newBus<br/>redis.NewClient"]
  end

  mongo[("MongoDB")]
  redis[("Redis")]

  routes --> hauth & husers & hsess & hch & hinv & hmsg & hfr & hmedia & hpres & hhealth & hweb
  routes --> hws
  reqauth --> sstore

  hauth --> authsvc
  authsvc --> ustore
  authsvc --> sstore
  husers --> ustore
  husers -->|"profile event"| pubif
  hsess --> sstore
  hch --> cstore
  hch -->|"Subscribe / Unsubscribe"| pubif
  hinv --> istore
  hinv --> cstore
  hinv -->|"Subscribe"| pubif
  hmsg --> mstore
  hmsg --> cstore
  hmsg --> mdstore
  hmsg -->|"Publish(chID, Message JSON)"| pubif
  hfr --> fstore
  hmedia --> mdstore
  hpres --> pres
  hpres --> ustore
  hpres -->|"SharingAChannelWith"| cstore
  hpres -->|"FriendsAmong"| fstore
  hws --> cstore
  hws --> sstore
  hws --> announce
  frame -->|"Publish(chID, typingEvent)"| pubif
  announce -->|"Publish(chID, presenceEvent)"| pubif
  pubif --> busrun
  wire --> sstore
  wire --> hub
  sstore -->|"onRevoked → PublishRevoked"| busrun

  ustore & sstore & cstore & mstore & fstore & istore & mdstore --> mgo
  busrun --> rdb
  pres --> rdb
  mgo --> mongo
  rdb --> redis
```

**Reading it**

- **`publisher` is the only broadcast seam.** Every handler that announces anything calls
  `Publish` / `Subscribe` / `Unsubscribe` on that interface, never `Hub` directly.
  That is an invariant (`docs/architecture.md`), and it is why swapping single-instance
  fan-out for the Redis bus touched one file.
- `*bus` and `*Hub` both satisfy `publisher`. Tests use `*Hub`, which reaches this process
  only, so they need neither Redis nor a second instance.
- `requireAuth` wraps every route except `GET /health`, `GET /ws` and `GET /`. `/ws` is
  outside on purpose — see `seq-ws-connect.mmd`.
- The layering here is by file, not by package: there is only one package (see
  `packages.mmd`).
- `handlePresence` is the only handler that reaches into two stores purely to answer
  "may this caller ask?" — `visibleTo` unions `channelStore.SharingAChannelWith` and
  `friendStore.FriendsAmong` before anything is read from Redis.

**Based on:** `server/main.go`, `server.go`, `middleware.go`, `auth.go`, `ws.go`, `hub.go`, `bus.go`, `typing.go`, `presence.go`, `presence_nodes.go`, `*_http.go`, `users.go`, `sessions.go`, `channels.go`, `messages.go`, `friends.go`, `invites.go`, `media.go`, `mongo.go`, `health.go`

---

## packages.mmd

Internal Go package dependencies. There is exactly one internal package — every file
under `server/` is `package main` — so there are no internal edges to draw, and the
diagram shows that plus the external modules it imports.

```mermaid
%% Internal Go package dependencies.
%% Source: `go list ./...` and `go list -f '{{.Imports}}' ./...` on go.mod module 'messenger'.
%% There is exactly ONE internal package, so there are no internal edges to draw:
%% every .go file under server/ is `package main`. The layering that a package
%% graph would normally show lives in backend-architecture.mmd instead.
flowchart TD
  subgraph internal["module messenger — internal packages"]
    srv["messenger/server<br/>package main<br/>39 files (27 source + 12 test), no sub-packages<br/>no internal imports"]
  end

  subgraph external["external modules — go.mod require block"]
    gws["github.com/gorilla/websocket v1.5.3"]
    grds["github.com/redis/go-redis/v9 v9.22.0"]
    bson["go.mongodb.org/mongo-driver/v2/bson"]
    mgo["go.mongodb.org/mongo-driver/v2/mongo"]
    mopt["go.mongodb.org/mongo-driver/v2/mongo/options"]
    mref["go.mongodb.org/mongo-driver/v2/mongo/readpref"]
    argon["golang.org/x/crypto/argon2"]
  end

  srv -->|"ws.go, hub.go"| gws
  srv -->|"bus.go, presence.go"| grds
  srv -->|"every store, hub.go"| bson
  srv -->|"mongo.go and every store"| mgo
  srv -->|"index and query options"| mopt
  srv -->|"mongo.go, health.go — Ping"| mref
  srv -->|"password.go"| argon
```

**Reading it**

- This is a finding, not a simplification: `go list ./...` returns the single line
  `messenger/server`. 39 files, 27 source and 12 test, no sub-packages.
- It is also deliberate: the package layout is not designed up front, a file is split off
  only when the cut becomes obvious.
- The layering a package graph would normally show lives in `backend-architecture.mmd`.
- `goda` is not installed on this machine; it would produce the same single node.

**Based on:** `go list ./...`, `go list -f '{{.Imports}}' ./...`, `go.mod`

---

## frontend.mmd

The Vue app: views and components, the client modules, and the backend surface each one
touches. There is **no vue-router and no pinia** — `views/Messenger.vue` is both the
router and the store.

```mermaid
%% Vue app: views, components, client modules, and the backend surface each one
%% touches. The exact api.js call per module is in the README table.
%% There is no vue-router and no pinia: routing is paneFromUrl/paneToUrl in
%% views/Messenger.vue, state is refs there plus a reactive Map in naming.js.
%% Sources: web/src/main.js, App.vue, api.js, socket.js, naming.js, pending.js,
%% images.js, views/*.vue, components/*.vue, web/vite.config.js
flowchart TD
  main["main.js"]
  app["App.vue<br/>me, re-checked on focus<br/>sessionEnded remounts"]
  signin["views/SignIn.vue"]
  messenger["views/Messenger.vue<br/>THE store and THE router<br/>channels, messages, unread, presence,<br/>typing, friends, pendingAction, originals<br/>paneFromUrl / paneToUrl"]

  subgraph views["views/"]
    conv["Conversation.vue<br/>feed, scroll anchoring"]
    info["InfoPanel.vue<br/>channel info, invites"]
    profile["Profile.vue<br/>own profile, devices"]
    userp["UserPanel.vue<br/>another person"]
  end

  subgraph comps["components/"]
    rail["ChannelRail → ChannelRow"]
    bubble["MessageBubble"]
    composer["MessageComposer → SendFilesDialog"]
    menu["MessageMenu"]
    viewer["MediaViewer"]
    prims["ChannelGlyph, SgAvatar, SgButton,<br/>SgDialog, SgInput, SgSpinner, PaneHeader"]
  end

  subgraph clients["client modules"]
    apijs["api.js<br/>request / upload"]
    wsjs["socket.js<br/>backoff 1s..15s<br/>close 4001 ends the session"]
    naming["naming.js<br/>reactive Map people"]
    pending["pending.js<br/>/invite/{code} at load"]
    images["images.js<br/>1280 px, JPEG 0.87"]
  end

  http["HTTP, same origin"]
  ws["WebSocket /ws"]

  main --> app
  app --> signin
  app --> messenger
  app -->|"/auth/me, /auth/logout"| apijs
  signin -->|"/auth/login, /auth/register"| apijs

  messenger --> conv & info & profile & userp & rail
  conv --> bubble & composer & menu & viewer & prims
  info --> viewer & prims
  profile --> viewer & prims
  userp --> viewer & prims
  rail --> prims
  menu --> prims
  bubble --> prims

  messenger -->|"/channels, /messages,<br/>/presence, /friends,<br/>/invites/{code}"| apijs
  info -->|"/channels/{id},<br/>/channels/{id}/invites,<br/>/channels/{id}/avatar"| apijs
  profile -->|"/auth/me/profile,<br/>/auth/me/avatar,<br/>/auth/sessions"| apijs
  userp -->|"/users/{username}"| apijs
  rail -->|"/users?q="| apijs
  composer -->|"POST /media"| apijs
  naming -->|"/users/{username}"| apijs

  messenger --> wsjs & naming & pending
  info --> naming & images & pending
  profile --> naming & images
  userp --> naming
  conv --> naming
  rail --> naming
  composer --> images
  bubble -.->|"/media/{id}"| http
  viewer -.->|"/media/{id}"| http

  apijs --> http
  wsjs --> ws
  ws -->|"in: Message, typing,<br/>presence, profile"| messenger
  messenger -->|"out: typing only"| wsjs
```

**Reading it**

- Routing is `paneFromUrl` / `paneToUrl` plus `history.replaceState` (not `pushState`,
  so switching chats does not pile up back-button entries). The paths are `/c/{id}`,
  `/c/{id}/info`, `/c/{id}/u/{username}`, `/u/{username}` and `/profile`. The prefix is
  `/c/` and not `/channels/` because `GET /channels/{id}` is an API route the dev proxy
  would answer with JSON.
- `/invite/{code}` is handled separately in `pending.js`, at module-evaluation time,
  before `createApp` — the code is stashed and the URL rewritten to `/`, because whoever
  followed the link may still have to sign in.
- State is `ref`s inside `Messenger.vue` plus one module-level `reactive(new Map())` in
  `naming.js`, which caches display names and avatars per session. Documents embed the
  username only, so the display name has to be looked up and kept current — which is what
  the `profile` WebSocket event is for.
- `GET /users/{username}` and `GET /users?q=` no longer return `last_seen_at` (#24);
  `GET /presence` is the only place that answers it, and only about people the caller
  meets somewhere.
- The socket carries four inbound shapes. Three have a `type` field (`typing`,
  `presence`, `profile`); a stored `Message` has none, and that is how `receive()` tells
  them apart. Outbound there is exactly one: `{type: 'typing', channel_id}`.

Which module calls what:

| Module | `api.js` calls |
|---|---|
| `App.vue` | `me`, `logout` |
| `views/SignIn.vue` | `login`, `register`, `me` |
| `views/Messenger.vue` | `channels`, `createChannel`, `openDirect`, `leaveChannel`, `followInvite`, `messages`, `messagesByIds`, `send`, `forward`, `presence`, `friends`, `friendRequests`, `sendFriendRequest`, `acceptFriendRequest`, `declineFriendRequest` |
| `views/InfoPanel.vue` | `channel`, `channelsInCommon`, `invites`, `createInvite`, `revokeInvite`, `setChannelAvatar`, `removeChannelAvatar`, `user` |
| `views/Profile.vue` | `updateProfile`, `setAvatar`, `removeAvatar`, `sessions`, `revokeSession` |
| `views/UserPanel.vue` | `user`, `channelsInCommon` |
| `components/ChannelRail.vue` | `searchUsers` |
| `components/MessageComposer.vue` | `uploadMedia` |
| `naming.js` | `user` |

**Based on:** `web/src/main.js`, `App.vue`, `api.js`, `socket.js`, `naming.js`, `pending.js`, `images.js`, `views/*.vue`, `components/*.vue`, `web/vite.config.js`

---

## data-model.mmd

The persisted structs with their MongoDB collection, the runtime structs that carry
cluster state, the JSON events that travel Pub/Sub, and the Redis keys.

```mermaid
%% Persisted structs with their MongoDB collection, the runtime structs that
%% hold cluster state, the JSON events that travel Pub/Sub, and the Redis keys.
%% Key and index detail is in the README tables. Placeholders such as
%% ch:CHANNEL_ID stand for ch:{channelID} — Mermaid rejects braces in a member.
%% Sources: server/users.go, sessions.go, channels.go, messages.go, friends.go,
%% invites.go, media.go, hub.go, bus.go, presence.go, typing.go, users_http.go
classDiagram
  direction LR

  class User {
    <<collection users>>
    +ObjectID ID
    +string Username
    +string DisplayName
    +string Bio
    +string PasswordHash
    +ObjectID AvatarID
    +time_Time CreatedAt
    +time_Time LastSeenAt
  }

  class Session {
    <<collection sessions>>
    +ObjectID ID
    +string Token
    +ObjectID UserID
    +string Username
    +time_Time CreatedAt
    +time_Time ExpiresAt
    +time_Time LastActivityAt
    +string UserAgent
    +expired() bool
  }

  class Channel {
    <<collection channels>>
    +ObjectID ID
    +string Kind
    +string Name
    +ObjectID CreatedBy
    +time_Time CreatedAt
    +List~ChannelMember~ Members
    +ObjectID AvatarID
    +int MemberCount
    +string DirectKey
  }

  class ChannelMember {
    <<embedded>>
    +ObjectID UserID
    +string Username
    +string Role
    +time_Time JoinedAt
  }

  class Message {
    <<collection messages>>
    +ObjectID ID
    +ObjectID ChannelID
    +MessageAuthor Author
    +string Text
    +time_Time CreatedAt
    +string ClientMsgID
    +ForwardedFrom Forwarded
    +ObjectID ReplyTo
    +List~Attachment~ Attachments
    +forwardOf() ForwardedFrom
  }

  class MessageAuthor {
    <<embedded>>
    +ObjectID ID
    +string Username
  }

  class ForwardedFrom {
    <<embedded>>
    +ObjectID MessageID
    +ObjectID ChannelID
    +MessageAuthor Author
    +time_Time CreatedAt
  }

  class Attachment {
    <<embedded>>
    +ObjectID ID
    +string ContentType
    +int Width
    +int Height
  }

  class Media {
    <<collection media>>
    +ObjectID ID
    +ObjectID FileID
    +ObjectID OwnerID
    +string Kind
    +string ContentType
    +int Width
    +int Height
    +ObjectID ChannelID
    +time_Time CreatedAt
    +time_Time ExpiresAt
  }

  class GridFS {
    <<fs.files, fs.chunks>>
    +raw image bytes
  }

  class FriendRequest {
    <<collection friend_requests>>
    +ObjectID ID
    +FriendParty From
    +FriendParty To
    +string Status
    +time_Time CreatedAt
    +time_Time RespondedAt
  }

  class FriendParty {
    <<embedded>>
    +ObjectID ID
    +string Username
  }

  class Invite {
    <<collection invites>>
    +string Code
    +ObjectID ChannelID
    +ObjectID CreatedBy
    +time_Time CreatedAt
  }

  User "1" --> "0..*" Session : user_id
  Channel *-- ChannelMember : members
  Channel "1" --> "0..*" Message : channel_id
  Channel "1" --> "0..*" Invite : channel_id
  Message *-- MessageAuthor : author
  Message o-- ForwardedFrom : forwarded
  Message *-- Attachment : attachments
  Message --> Message : reply_to
  Attachment ..> Media : same _id
  Media --> GridFS : file_id
  Media ..> Channel : channel_id
  User ..> Media : avatar_id
  Channel ..> Media : avatar_id
  FriendRequest *-- FriendParty : from, to
  User ..> MessageAuthor : denormalised
  User ..> ChannelMember : denormalised

  class publisher {
    <<interface>>
    +Publish(chID, msg)
    +Subscribe(userID, chID)
    +Unsubscribe(userID, chID)
  }

  class bus {
    +redis_Client rdb
    +redis_PubSub sub
    +sync_Mutex mu
    +map pending
    +chan~struct~ wake
    +func onSessionRevoked
    +Run(ctx)
    +applyWatches(ctx)
    +PublishRevoked(sessionID)
    +Watch(chID)
    +Unwatch(chID)
  }

  class Hub {
    +sync_Mutex mu
    +map byChannel
    +map byUser
    +func watch
    +func unwatch
    +Connect(c, channelIDs)
    +CloseSession(sessionID)
    +Reads(c, chID) bool
    +ChannelsOf(c) List~string~
  }

  class Subscriber {
    +string userID
    +string sessionID
    +string socketID
    +chan~bytes~ send
    +int closeCode
    +map channels
    +bool dropped
    +time_Time lastTyping
    +map typedAt
  }

  class presence {
    +redis_Client rdb
    +string nodeID
    +map alive
    +int64 epoch
    +connect(ctx, userID, socket)
    +disconnect(ctx, userID, socket)
    +lookup(ctx, userIDs)
    +beat(ctx) bool
    +listen(ctx) List~string~
    +sweeping(ctx) bool
    +claim(ctx, nodeID) bool
  }

  class presenceState {
    +bool Online
    +int64 Version
    +int64 Epoch
  }

  class RedisTopics {
    <<pub/sub>>
    ch:CHANNEL_ID per channel, watched
    session-revoked permanent
    subscription permanent
  }

  class RedisPresenceKeys {
    <<plain keys>>
    presence:sockets:USER_ID SET
    presence:node:NODE_ID SET
    presence:version:USER_ID counter
    presence:alive:NODE_ID 30s TTL
    presence:nodes SET
    presence:sweeper SETNX 10s
    presence:epoch SETNX millis
  }

  class subscriptionChange {
    +string UserID
    +string ChID
    +bool Subscribed
  }

  class typingEvent {
    +string Type
    +string ChannelID
    +MessageAuthor User
  }

  class presenceEvent {
    +string Type
    +string UserID
    +bool Online
    +int64 Version
    +int64 Epoch
    +time_Time LastSeen
  }

  class profileEvent {
    +string Type
    +profileView User
  }

  publisher <|.. bus
  publisher <|.. Hub
  bus --> Hub : Publish on incoming
  Hub --> bus : watch, unwatch
  Hub *-- Subscriber
  bus --> RedisTopics
  presence --> RedisPresenceKeys
  presence ..> presenceState
  presence ..> User : SetLastSeen
  subscriptionChange ..> RedisTopics : subscription
  Message ..> RedisTopics : channel topic
  typingEvent ..> RedisTopics : channel topic
  presenceEvent ..> RedisTopics : channel topic
  profileEvent ..> RedisTopics : channel topic
```

**Reading it**

Mermaid rejects `{` inside a class member, so the diagram writes `ch:CHANNEL_ID` and
`presence:sockets:USER_ID` where the code uses `ch:{channelID}` and
`presence:sockets:{userID}`. The exact forms are in the two tables above.

Indexes, from the `ensureIndexes` method of each store:

| Collection | Indexes |
|---|---|
| `users` | unique `username` |
| `sessions` | unique `token`; TTL on `expires_at` (`expireAfterSeconds: 0`); `user_id + _id` |
| `channels` | `members.user_id + _id` (multikey); unique sparse `direct_key` |
| `messages` | `channel_id + _id`; unique `channel_id + client_msg_id`, partial on `client_msg_id` existing |
| `friend_requests` | `to.id + status + _id`; `from.id + status + _id`; unique `from.id + to.id`, partial on `status: 'pending'` |
| `invites` | `channel_id + created_at desc`; the code is `_id`, so uniqueness is free |
| `media` | partial on `expires_at`; `channel_id`; `file_id` |

- **Denormalisation is one-directional.** `ChannelMember`, `MessageAuthor` and
  `FriendParty` embed `username`, which never changes, and never `display_name` or
  `avatar_id`, which do. That is the whole reason the `profile` event exists.
- `Channel.DirectKey` is the sorted pair `"userA:userB"`, which turns "the conversation
  between these two" into a value the unique index can enforce. A direct channel can
  therefore never gain a third member — `AddMember` filters on `kind: 'channel'`.
- `Message.Forwarded` is a snapshot, not a reference, so a forward survives its source
  channel being discarded, and `forwardOf()` keeps chains from nesting.
- `Message.ReplyTo` is only an id: the quote is filled in by the client, so an edited
  original would show its current text.
- `Message.ClientMsgID` is unique **within its channel**, not globally (#26). It used to
  be a global unique index, which made the sender's retry key also a read key: posting
  with an id somebody else had used failed the insert, fell into the duplicate branch and
  answered with *their* message — text, author and channel id — to someone not in that
  channel. The index is partial rather than sparse because a sparse compound index takes
  any document holding one of its keys, and every message has a `channel_id`, so messages
  sent without a `client_msg_id` would all index as `{channel, null}` and the second one
  in a channel would be refused.
- `User.LastSeenAt` is `json:"-"` (#24). It used to leak through `GET /users/{username}`
  and through search, which hand back `User` itself. The profile stays public so a
  stranger can be found and befriended at all; state is not part of the profile and is
  answered only by `GET /presence`.
- `Media` and `GridFS` are separate on purpose. A forwarded picture is a second `media`
  record over the same `FileID`, which is why `DeleteUnreferencedFiles` counts references
  before deleting bytes.

**Based on:** `server/users.go`, `sessions.go`, `channels.go`, `messages.go`, `friends.go`, `invites.go`, `media.go`, `hub.go`, `bus.go`, `presence.go`, `typing.go`, `users_http.go`

---

## seq-login.mmd

Login: `POST /auth/login` through to the session cookie.

```mermaid
%% Login. See README for the notes referenced by step number.
%% Sources: web/src/views/SignIn.vue, web/src/api.js, server/auth.go,
%% server/password.go, server/users.go, server/sessions.go
sequenceDiagram
  autonumber
  participant UI as SignIn.vue
  participant API as api.js
  participant A as auth.handleLogin
  participant US as userStore
  participant SS as sessionStore
  participant M as MongoDB

  UI->>API: api.login(username, password)
  API->>A: POST /auth/login
  A->>US: GetByUsername(username)
  US->>M: users.FindOne on username
  M-->>A: User or errUserNotFound
  Note over A: acquire() on sem, cap 6
  Note over A,US: verifyPassword against the hash or dummyHash
  alt no user or no match
    A-->>UI: 401 invalid username or password
  else verified
    A->>SS: Create(user, r.UserAgent())
    Note over SS: newToken(), 32 bytes, base64
    SS->>M: sessions.InsertOne
    M-->>SS: InsertedID
    Note over SS: put(sess) in the local cache
    SS-->>A: Session
    A-->>API: 200, Set-Cookie session
    API-->>UI: authResponse
    UI->>A: GET /auth/me
    A->>US: GetByUsername(sess.Username)
    A-->>UI: meResponse
  end
```

**Reading it**

- **`acquire() on sem, cap 6`.** It bounds concurrent argon2id hashes to `hashConcurrency = 6` with a
  `hashWaitTimeout` of 2 s, after which the answer is 503. The limit is per process, so
  two replicas on one host allow 12 hashes at 19 MB each.
- **`verifyPassword against the hash or dummyHash`.** An unknown username is verified against `a.dummyHash` rather than skipped, so a
  wrong username and a wrong password take the same time.
- **`newToken()` / `200, Set-Cookie session`.** The token is 32 random bytes, base64 `RawURLEncoding`. It goes into an
  `HttpOnly`, `SameSite=Lax` cookie and into the response body, for clients that prefer
  `Authorization: Bearer`. It never appears in `GET /auth/sessions`, which projects it
  away, and never travels the bus.
- **`put(sess) in the local cache`.** It writes to this instance's cache only. The other node learns nothing
  here — it reads the session from MongoDB the first time that token reaches it, then
  caches it for `cacheTTL = 10 min`.
- **`GET /auth/me`.** `handleMe` reads the *user*, not the session, so a display name changed in
  another tab is current rather than stale until the session expires.

**Based on:** `web/src/views/SignIn.vue`, `web/src/api.js`, `server/auth.go`, `server/password.go`, `server/users.go`, `server/sessions.go`

---

## seq-ws-connect.mmd

Opening the WebSocket: upgrade, session check, hub routing, the Redis subscription, and
the presence write.

```mermaid
%% WebSocket connect. See README for the notes referenced by step number.
%% Sources: web/src/socket.js, server/ws.go, server/hub.go, server/bus.go,
%% server/presence.go, server/channels.go, server/sessions.go
sequenceDiagram
  autonumber
  participant C as socket.js
  participant WS as handleWS
  participant ST as session / channel store
  participant HUB as Hub
  participant BUS as bus
  participant P as presence
  participant R as Redis
  participant M as MongoDB

  C->>WS: GET /ws with the session cookie
  Note over WS: Upgrade first, then checkOrigin
  WS->>ST: sessions.ByToken(token)
  ST->>M: sessions.FindOne on a cache miss
  alt errSessionNotFound
    WS-->>C: close 4001 session ended
  else session valid
    WS->>ST: channels.ForUser(userID, 200)
    ST->>M: channels.Aggregate on members.user_id
    ST-->>WS: channels
    WS->>HUB: Connect(subscriber, channelIDs)
    HUB->>BUS: watch(chID) per first local reader
    Note over BUS: pending[chID] = true, one wake signal
    BUS->>R: applyWatches, one SUBSCRIBE for the set
    WS->>ST: sessions.ByToken again
    Note over WS,ST: closes the revocation race
    WS->>P: socketOpened, presence.connect
    P->>R: MULTI SADD, SMEMBERS, INCR, EXEC
    R-->>P: added, members, version
    opt the user was offline until now
      P->>BUS: announcePresence per channel
      BUS->>R: PUBLISH ch:{channelID} presenceEvent
    end
    Note over WS: go writePump, then readPump
    WS-->>C: socket open
    Note over C: onStateChange('online')
  end
```

**Reading it**

- **`Upgrade first, then checkOrigin`.** The upgrade happens *before* the session check. Refused earlier, the answer is
  an HTTP 401 the browser hides from the page: the client sees close code 1006, the same
  as a network drop, and reconnects forever. Refused after, the client gets 4001 and stops.
- **`checkOrigin`.** It compares the `Origin` host against `r.Host`, plus anything in
  `WS_ALLOWED_ORIGINS` — needed only because `npm run dev` serves from port 5173 while the
  Vite proxy rewrites `Host` to 8080. A request with no `Origin` is allowed: only browsers
  send it, and only browsers attach cookies unprompted.
- **`channels.ForUser(userID, 200)`.** The socket subscribes to *all* of the user's channels at once, not one socket per
  channel. `channelsMaxLimit` is 200.
- **`watch(chID)` / `applyWatches`.** `watch` runs under the hub's mutex, so it must not block: it records the
  channel in `bus.pending` and pokes a one-slot `wake` channel. `bus.Run` then takes the
  whole set and issues one `SUBSCRIBE`, which turns a burst of a few hundred channels into
  a couple of round trips. A failed command is only logged — go-redis records the channels
  whatever the command returned and resubscribes them itself on reconnect.
- **`sessions.ByToken again`.** The second lookup closes a race: a revocation that landed between the first
  check and `Connect` would have found no socket to close. Revocation invalidates the
  cache before closing sockets, and this asks again only after the socket is in the hub,
  so one of the two always sees the other. It is almost always a cache hit.
- **`MULTI SADD, SMEMBERS, INCR, EXEC`.** `presence.connect` uses `MULTI`/`EXEC` so both indexes move together and the
  `SMEMBERS` reads the state the write produced.
- **`go writePump, then readPump`.** `writePump` is the only goroutine that writes to the socket — `gorilla/websocket`
  forbids concurrent writes. It also holds a timer on the session's `ExpiresAt` and
  re-reads the session when it fires, because expiry is otherwise invisible: the TTL index
  deletes the document without a word.

**Based on:** `web/src/socket.js`, `server/ws.go`, `server/hub.go`, `server/bus.go`, `server/presence.go`, `server/channels.go`, `server/sessions.go`

---

## seq-send-message-same-instance.mmd

Sending a message when both people hold a socket on the same instance.

```mermaid
%% Sending a message when both people are on the SAME instance.
%% It still travels Redis: bus.Publish is the only broadcast path.
%% See README for the notes referenced by step number.
%% Sources: web/src/views/Messenger.vue, server/messages_http.go,
%% server/messages.go, server/bus.go, server/hub.go
sequenceDiagram
  autonumber
  participant A as Alice
  participant H as handleSendMessage
  participant M as MongoDB
  participant BUS as bus
  participant R as Redis
  participant HUB as Hub
  participant B as Bob

  Note over A: send(), client_msg_id = randomUUID()
  A->>H: POST /messages
  Note over H: channelForMember, IsMember
  Note over H,M: parseMediaIDs, validateText, media.Attach
  H->>M: messages.Insert
  alt duplicate channel_id + client_msg_id
    M-->>H: errDuplicateMessage
    H->>M: ByClientMsgID(channelID, id)
    H-->>A: 200 stored, NOT broadcast again
  else stored
    M-->>H: stored Message
    H->>BUS: bus.Publish(channelID, payload)
    BUS->>R: PUBLISH ch:{channelID}
    H-->>A: 201 stored
  end
  R-->>BUS: ch:{channelID}
  BUS->>HUB: Hub.Publish(chID, payload)
  Note over HUB: c.send per Subscriber, drop if full
  HUB->>B: writePump, TextMessage
  HUB->>A: the sender's own socket too
  Note over B: receive(), no type field, so a message
  Note over A: already known by client_msg_id
```

**Reading it**

- **The message still goes through Redis.** There is no local short-cut. `bus.Publish`
  is the only broadcast path, which means one message order on every node and no echo to
  filter out.
- **`messages.Insert` before `bus.Publish`.** Persist first, announce second. This is an invariant (`docs/architecture.md`), and it is
  what makes a lost bus event cost latency instead of data.
- **`errDuplicateMessage` / `ByClientMsgID(channelID, id)`.** `channel_id + client_msg_id` carries a unique partial index, so a retried POST
  hits a duplicate-key error, gets the stored message back with 200, and is **not**
  broadcast a second time. The client generates the id with `crypto.randomUUID()`.
  The channel is half of both the index and the lookup (#26): the id is the sender's own
  retry key, never a way to read a message out of a channel they are not in.
- **`c.send per Subscriber, drop if full`.** `Hub.Publish` does a non-blocking send into each `Subscriber.send` (buffer
  `sendBuffer = 256`) and drops any socket whose buffer is full. The drop exists because
  the hub holds one global mutex: a stuck reader would otherwise block every broadcast on
  the node.
- **`the sender's own socket too`.** It receives the message like everyone else's. The client
  finds it already in the feed by `client_msg_id` and ignores it.
- Attachments are bound to the channel *last*, once nothing else can refuse the message: a
  file bound to a channel with no message is harmless, a message pointing at files nobody
  may open is not.

**Based on:** `web/src/views/Messenger.vue` (`send`, `deliver`, `receive`), `server/messages_http.go`, `server/messages.go`, `server/bus.go`, `server/hub.go`

---

## seq-send-message-cross-instance.mmd

The same send when the two people are on different instances. The only difference is
which nodes hold a subscription to `ch:{channelID}`.

```mermaid
%% Sending a message when the two people are on DIFFERENT instances.
%% The only difference is which nodes hold a subscription to ch:{channelID}.
%% See README for the notes referenced by step number.
%% Sources: server/messages_http.go, server/bus.go, server/hub.go, server/ws.go
sequenceDiagram
  autonumber
  participant A as Alice on 8080
  participant N1 as instance 1
  participant M as MongoDB
  participant R as Redis
  participant N2 as instance 2
  participant B as Bob on 8081

  Note over N2,B: Bob connected here earlier, so instance 2<br/>ran SUBSCRIBE ch:{channelID}
  A->>N1: POST /messages
  N1->>M: messages.Insert
  M-->>N1: stored Message
  Note over N1,M: persist first, announce second
  N1->>R: PUBLISH ch:{channelID}
  N1-->>A: 201 stored
  R-->>N2: ch:{channelID}
  Note over N2: bus.Run, prefix ch:, Hub.Publish
  N2->>B: writePump, TextMessage
  opt channel not in Bob's loaded list
    B->>N2: catchUpChannels, GET /channels
  end
  Note over R,N2: With no subscriber anywhere the event is dropped.<br/>The message is in MongoDB, and backfill() fetches it.
```

**Reading it**

- **The opening note.** Instance 2 subscribed when Bob's socket arrived there, not because of anything
  the sender did. No node knows where anyone else's sockets are — that is exactly why the
  `subscription` topic is global rather than addressed.
- **`bus.Run, prefix ch:, Hub.Publish`.** It routes by topic prefix: `ch:` goes to `Hub.Publish`, `session-revoked`
  to the revocation callback, `subscription` to `Hub.Subscribe` / `Unsubscribe`.
- **`catchUpChannels, GET /channels`.** The server can route a socket to a channel the client's list has never shown
  — a direct conversation the other side opened, or a join made in another tab.
  `catchUpChannels()` reloads the list, one request at a time so a burst does not fetch it
  once per message.
- **The closing note.** With no subscriber anywhere, Redis drops the event. Nothing is lost: the message
  is in MongoDB and `backfill()` fetches it on reconnect.
- There is no sticky-session problem to solve here, because no per-user state lives in the
  process. There is also no failover: `socket.js` reconnects to `location.host`, so a tab
  whose node dies retries that same port with 1–15 s backoff until it comes back.

**Based on:** `server/messages_http.go`, `server/bus.go`, `server/hub.go`, `server/ws.go`, `web/src/views/Messenger.vue`

---

## seq-load-history.mmd

Loading history: the first page, older pages on scroll, the originals that replies quote,
and the backfill after a reconnect.

```mermaid
%% Loading history: first page, older pages, reply originals, backfill.
%% See README for the notes referenced by step number.
%% Sources: web/src/views/Messenger.vue, web/src/views/Conversation.vue,
%% server/messages_http.go, server/messages.go
sequenceDiagram
  autonumber
  participant CV as Conversation.vue
  participant MG as Messenger.vue
  participant H as handleListMessages
  participant MS as messageStore
  participant M as MongoDB

  Note over MG: selectChannel(id)
  MG->>H: GET /messages?channel_id=ID
  Note over H: channelForMember, IsMember
  H->>MS: List(channelID, zero, 0)
  MS->>M: Find, sort _id desc, limit 50
  M-->>MG: newest 50
  Note over MG: .reverse(), then toBottom()

  CV->>MG: load-older on the top sentinel
  MG->>H: GET /messages?channel_id=ID&before=OID
  H->>MS: List(channelID, before, limit)
  MS->>M: Find _id lt before, sort desc, limit 50
  M-->>MG: older page
  alt empty page
    Note over MG: hasOlder = false
  else
    Note over CV,MG: distanceFromBottom, prepend, keepPosition
  end
  Note over H,M: _id is the cursor, not created_at

  Note over MG: watch(messages) runs resolveReplies()
  MG->>H: GET /messages?channel_id=ID&ids=a,b,c
  Note over H: ids excludes before and limit, max 100
  H->>MS: ByIDs(channelID, ids)
  MS->>M: Find _id in ids
  M-->>MG: the quoted originals
  Note over MG: a missing id becomes null in originals

  Note over MG: socket went offline, then online
  MG->>H: backfill(), GET /messages?channel_id=ID
```

**Reading it**

- **`List(channelID, zero, 0)`.** The default page is `messagesPageSize = 50`, capped at `messagesMaxLimit = 100`,
  sorted `_id` descending. The client reverses it for the feed.
- **`Find _id lt before`.** The cursor is `_id`, not `created_at`. A measurement found up to 4173
  documents sharing one `created_at`, which makes it useless as a cursor.
  `_id` is an ObjectID, so it is time-ordered anyway, and the index `channel_id + _id`
  serves the descending sort as a backward scan with no `SORT` stage. There is no `skip`.
- **`GET /messages?channel_id=ID&ids=a,b,c`.** `ids` asks for particular messages rather than a page, so it refuses to be
  combined with `before` or `limit`. `listMessagesByID` uses `SplitN` with the limit plus
  one, so a query with a million commas is rejected without allocating a million strings.
- **`a missing id becomes null in originals`.** An id the server leaves out becomes `null` in `originals`, and the quote renders
  as unavailable. `findMessage(id)` walks up to 20 older pages when a quote's original is
  further back than the loaded window.
- **`backfill()`.** It runs on every offline→online transition. This is the mechanism
  that makes a lost Pub/Sub event harmless, so it belongs to the delivery guarantee, not
  to the UI.

**Based on:** `web/src/views/Messenger.vue` (`selectChannel`, `loadOlder`, `findMessage`, `resolveReplies`, `backfill`), `web/src/views/Conversation.vue`, `server/messages_http.go`, `server/messages.go`

---

## seq-revoke-session.mmd

Revoking a session from the device list — the `session-revoked` topic, and both directions
of `wireRevocation`.

```mermaid
%% Revoking a session: the 'session-revoked' Redis topic.
%% See README for the notes referenced by step number.
%% Sources: web/src/views/Profile.vue, server/users_http.go, server/sessions.go,
%% server/main.go (wireRevocation), server/bus.go, server/hub.go, server/ws.go
sequenceDiagram
  autonumber
  participant P as Profile.vue
  participant N1 as instance 1
  participant M as MongoDB
  participant R as Redis
  participant N2 as instance 2
  participant D as the revoked device

  P->>N1: DELETE /auth/sessions/{id}
  N1->>M: sessions.FindOneAndDelete by _id and user_id
  M-->>N1: the deleted document, with its token
  Note over N1: invalidate(token), invalidations++
  Note over N1: onRevoked, set by wireRevocation
  par act here first
    N1->>D: Hub.CloseSession, close 4001
  and announce second
    N1->>R: PUBLISH session-revoked ID
  end
  N1-->>P: 200 status revoked
  R-->>N2: session-revoked ID
  Note over N2: invalidateByID scans the local cache
  N2->>D: Hub.CloseSession, close 4001
  Note over D: socket.js stops retrying on 4001,<br/>App.vue re-reads /auth/me
```

**Reading it**

- **`sessions.FindOneAndDelete by _id and user_id`.** `user_id` is part of the filter, so somebody else's session is simply not
  found rather than refused.
- **`the deleted document, with its token`.** It deletes by token but reads the document back, because the announcement
  carries the **id**, not the token. The token is the credential itself and has no business
  on the wire — Rocket.Chat keeps a hash of it for the same reason.
- **`invalidate(token), invalidations++`.** It bumps a counter as well as dropping the entry. A lookup notes the
  counter before querying and writes to the cache only if it has not moved, so a session
  deleted while an answer was in flight cannot be resurrected for `cacheTTL`.
- **The `par` block.** The revoking node acts first and announces second. With Redis down, revocation
  still works where it was asked for, and the other nodes catch up when their cache entry
  expires (at most 10 minutes).
- **`invalidateByID scans the local cache`.** It scans the cached sessions of that node, because the cache is
  keyed by token and the event carries an id. Revocations are rare, so this is cheaper
  than a second index kept in step on every login.
- **`Hub.CloseSession, close 4001`.** Invalidating the cache stops new requests; closing the sockets stops the ones
  already open, which authenticated once and would otherwise live on. A session can hold
  several sockets — every tab of a browser shares the cookie.
- The revoking node hearing its own event back changes nothing, which is why no filtering
  is needed.

**Based on:** `web/src/views/Profile.vue`, `server/users_http.go`, `server/sessions.go`, `server/main.go` (`wireRevocation`), `server/bus.go`, `server/hub.go`, `server/ws.go`

---

## seq-presence.mmd

Presence in three parts: a socket closing normally, a whole node dying and being swept,
and the REST lookup a client does on arrival.

```mermaid
%% Presence: a socket closing, a node dying, and the state a client asks for
%% on arrival. See README for the notes referenced by step number.
%% Sources: server/ws.go, server/presence.go, server/presence_nodes.go,
%% server/presence_http.go, server/users.go, web/src/views/Messenger.vue
sequenceDiagram
  autonumber
  participant B as Bob's last tab
  participant WS as handleWS
  participant P as presence
  participant US as userStore
  participant R as Redis
  participant SV as a surviving node
  participant A as Alice

  rect rgb(238, 244, 250)
  Note over B,A: 1. the socket closes normally
  B->>WS: socket closed, readPump returns
  Note over WS,P: ChannelsOf before Disconnect.<br/>Hub.drop keeps c.channels, so a socket the<br/>hub evicted is announced too
  WS->>P: socketClosed, presence.disconnect
  P->>R: MULTI SREM, SREM, SMEMBERS, INCR, EXEC
  R-->>P: removed, remaining, version
  Note over P: onlineIn ignores nodes with no pulse
  opt no live socket left anywhere
    P->>US: SetLastSeen(userID, now)
    P->>R: PUBLISH ch:{channelID} presenceEvent
    R-->>A: online=false with last_seen
    Note over A: notePresence, kept only if newer()
  end
  end

  rect rgb(250, 245, 238)
  Note over P,A: 2. the node dies without saying anything
  P--xR: presence:alive:{nodeID} expires after 30s
  loop every presenceBeat, 10s
    SV->>R: SET presence:alive:{me} EX 30, SADD presence:nodes
    SV->>R: SMEMBERS presence:nodes, EXISTS each pulse
    R-->>SV: the nodes with no pulse
  end
  SV->>R: SETNX presence:sweeper, one sweeper per round
  SV->>R: SREM presence:nodes {dead}, one winner
  SV->>R: SMEMBERS presence:node:{dead}
  R-->>SV: the orphaned user and socket pairs
  loop per orphaned socket
    SV->>R: presence.remove, INCR version
    SV->>US: SetLastSeen
    SV->>R: PUBLISH ch:{channelID} online=false
  end
  SV->>R: DEL presence:node:{dead}
  end

  rect rgb(242, 250, 240)
  Note over A,R: 3. state on arrival, events only say what changes
  A->>P: GET /presence?ids=a,b,c
  Note over P: visibleTo: SharingAChannelWith + FriendsAmong
  P->>R: pipelined SMEMBERS and GET version, visible ids only
  P->>US: LastSeenByIDs for the offline ones
  P-->>A: online, version, epoch, last_seen
  end
```

**Reading it**

- **`ChannelsOf before Disconnect`.** The channels are read *before* the hub forgets the socket, and the departure is
  announced *after* it is out of the hub, so the socket cannot hear about itself.
- **`Hub.drop keeps c.channels`.** It no longer clears `c.channels` (#25). It used to, and the hub drops a
  socket itself on a revoked session or a full send buffer — so by the time `handleWS`
  read the set it was empty and the offline event went to no channels at all. The user
  vanished from Redis and `last_seen_at` was written while everyone kept seeing them
  online until a reload. Clearing bought nothing: the subscriber is collected right after
  `handleWS` returns. `Reads` now answers `false` for a dropped socket, which it should
  have anyway — otherwise a typing frame could still leave a socket the hub had evicted.
- **`onlineIn ignores nodes with no pulse`.** It ignores sockets on nodes whose pulse has expired, so a read is correct
  the moment the pulse dies — no waiting for the sweep.
- **`SetLastSeen(userID, now)`.** `last_seen_at` is written only on the edge to offline. A second tab closing
  costs nothing, and a user who is online has no use for it. A failed write costs a stale
  "last seen", not the offline event, so it is only logged.
- **`notePresence, kept only if newer()`.** It keeps an event only if `newer(had, got)`: compare `epoch` first,
  then `version`. Events about one user come from different nodes and Pub/Sub keeps no
  order between publishers, so without this a stale "offline" could overwrite a fresh
  "online".
- **`SETNX presence:sweeper`.** One sweeper per round. The work is the same whoever does it, so doing it N
  times over would only race for the same claims. `presence:sweeper` expires by itself, so
  a node dying mid-sweep does not block the next round.
- **`SREM presence:nodes {dead}`.** This is the hand-off: Redis is single-threaded, so exactly one
  caller sees the entry actually disappear and owns the cleanup.
- **Not drawn:** `reregister()`. When `beat` reports this node was unknown — Redis
  restarted, or another node swept this one as merely slow — every local socket is
  registered again, so its people do not stay offline until they reload.
- **`GET /presence?ids=a,b,c`.** Events say only what *changes*, so state on arrival comes over REST. At most
  `presenceMaxIDs = 100` ids, asked for explicitly, as Mattermost's `/users/status/ids`
  does. `loadPresence()` runs on mount and on every reconnect.
- **`visibleTo` gates the answer** (#24). Online-ness and last-seen used to be readable by
  any logged-in account for any user id, and ids are not secret — search hands them out —
  so a hundred per request bought a running record of when a stranger sits at their
  computer. The boundary is the one already drawn for writing to a person: a channel in
  common, or friendship. Somebody outside it is **left out** of the answer rather than
  reported offline, since offline still confirms the account exists. Mattermost gates the
  profile and leaves status open; Revolt gates everything behind friendship or a shared
  server. This follows Revolt for state and Mattermost for the profile.
- **Known limit, stated in the commit:** a friend with no channel in common is answered by
  the REST snapshot but hears no live updates, because `announcePresence` publishes per
  channel and the bus has no channel to carry it on.
- Each node's `alive` map is a snapshot refreshed every 10 s, so two nodes can briefly
  disagree about whether a third is alive. `version` and `epoch` exist to let clients
  order the resulting events.

**Based on:** `server/ws.go`, `server/presence.go`, `server/presence_nodes.go`, `server/presence_http.go`, `server/users.go`, `web/src/views/Messenger.vue`

---

## seq-typing.mmd

Typing — the only frame a client may send over the socket, and the only flow here that
stores nothing at all.

```mermaid
%% Typing: the only frame a client may send over the socket. Nothing is stored.
%% See README for the notes referenced by step number.
%% Sources: web/src/views/Messenger.vue, web/src/socket.js, server/ws.go,
%% server/typing.go, server/hub.go, server/bus.go
sequenceDiagram
  autonumber
  participant A as Alice
  participant T as server.typing
  participant HUB as Hub
  participant R as Redis
  participant N2 as instance 2
  participant B as Bob

  Note over A: keystroke, announceTyping()
  Note over A: one frame per chat per 3000 ms
  A->>T: {type: 'typing', channel_id}
  Note over T: handleFrame drops anything else
  Note over T: lastTyping gap under 100 ms, drop
  Note over T: typedAt gap under 1 s, drop
  T->>HUB: Hub.Reads(c, chID)
  alt this socket does not read that channel
    HUB-->>T: false, ignored silently
  else
    HUB-->>T: true
    T->>R: PUBLISH ch:{channelID} typingEvent
    R-->>N2: typingEvent
    N2->>B: Hub.Publish, writePump
    Note over B: noteTyping, shown for 4000 ms
    R-->>A: the same event reaches Alice's tabs
    Note over A: dropped, ev.user.id is me.id
  end
  Note over A,B: no stop event: the listener forgets after 4 s,<br/>and receive() calls forgetTyping on the message
```

**Reading it**

- **Nothing is stored.** This is the flow that decided the bus: a MongoDB change stream
  can only carry what was written, and typing is worth something for a few seconds and
  never again.
- **`announceTyping()`.** Client side, it sends at most one frame per chat per
  `TYPING_REPEAT_MS = 3000`. After sending a message, `rearmTyping` pulls the next
  announcement forward — listeners forget a typist when their message arrives — but never
  closer than `TYPING_SERVER_GAP_MS = 1100`, because the server would drop it anyway.
- **`handleFrame drops anything else`.** It ignores anything that is not `type: 'typing'`, without an answer.
  Nobody is waiting for one, as with Revolt's `BeginTyping`. The read limit is 512 bytes.
- **`lastTyping gap under 100 ms`.** `typingMinGap` is checked first and takes no lock. Without it, a flood
  naming a new channel id in every frame would take the hub's global mutex in `Reads` —
  the mutex every broadcast on the node waits for. A dropped frame still moves
  `lastTyping`, so a flood stays dropped for as long as it lasts.
- **`typedAt gap under 1 s`.** `typingMinInterval` is per channel. Mattermost refuses to configure its own
  interval below one second for the same reason.
- **`Hub.Reads(c, chID)`.** Membership comes from the hub, not MongoDB: the socket is routed to exactly the
  channels its user is in, so asking costs a map lookup. It trails MongoDB by the time a
  `subscription` change takes to cross the bus, which is fine for typing and would not be
  for anything stored. Only a channel that passed the check is remembered in `typedAt`.
- **Both server limits are per connection**, not per user: `lastTyping` and `typedAt` live
  on `Subscriber`. Three tabs, possibly on three nodes, can each emit at the full rate.
  Harmless — the client keys typists by user id — but "one frame per second per channel"
  is per socket.
- **The closing note.** No "stopped typing" event exists. A listener forgets the typist after
  `TYPING_SHOWN_MS = 4000`, a little later than the next repeat would arrive, and a
  message from the typist clears it at once. Telegram uses 5 s and 6 s but also sends an
  explicit cancel.

**Based on:** `web/src/views/Messenger.vue` (`announceTyping`, `noteTyping`, `rearmTyping`), `web/src/socket.js`, `server/ws.go`, `server/typing.go`, `server/hub.go`, `server/bus.go`
