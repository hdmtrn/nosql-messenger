# Architecture

The decisions below were taken deliberately and with their cost understood. Several of them
are invariants: code that breaks one of them will look like it works and will fail later,
under load or on a second node. The measurements quoted here were taken on an M-series
machine (arm64).

## Several instances behind a Redis bus

Fan-out lives in the memory of each process: two indexes (`byChannel`, `byUser`) under one
mutex, a ceiling of ~36,000 messages/s. Instances are connected by Redis Pub/Sub, so the two
parties to a conversation need not sit on the same node. Before that, only one instance
could run, and that was a documented limitation.

What travels over the bus is what has to reach the other nodes and need not survive a
restart: a new message, a revoked session, a change in which sockets read a channel,
"typing". Everything durable stays in MongoDB, and the delivery guarantee stays where it
was — the message is written before it is announced, and the client backfills what it missed
on reconnect. The bus is the fast path, not the guarantee. Redis therefore runs without
persistence and holds nothing of record: not a queue, not a store, not a cache of MongoDB.

## All broadcast goes through one method

`hub.Publish(channelID, msg)` is the only broadcast point. Nothing writes to a client socket
outside it. That single seam is what let the Redis bus be added in a day without touching
the rest of the code, and it is the reason the in-memory hub can be swapped for another
fan-out mechanism at all.

## Persist first, then broadcast

A message is written to MongoDB and only then broadcast; the order is strict. Because of it,
losing the hub's in-memory state on restart loses no data: on reconnect the client fetches
what it missed with `GET /messages?before=...`.

## WebSocket writes come from one goroutine

`gorilla/websocket` forbids concurrent writes. Each client has a buffered channel
`send chan []byte` and its own `writePump`. When the buffer overflows the client is dropped
rather than blocked — under the hub's global mutex, one stuck reader would otherwise stall
broadcasting to an entire channel. The buffer and the drop exist because of that global
lock; remove the lock and neither is needed.

## Messages are a separate collection

Not an array inside the channel document: the 16 MB BSON document limit and the unbounded
array anti-pattern rule that out. The index is `{channel_id: 1, _id: 1}`, ascending — it
serves the descending sort as a backward scan (IXSCAN backward, no SORT stage). Pagination
is cursor-based on `before` rather than `skip`, and the cursor is `_id` rather than a
timestamp: a measurement found up to 4173 documents sharing the same `created_at`.

## MongoDB runs as a replica set

`--replSet rs0` from the first commit, even for a single node. Change streams and
transactions are only available in that mode, and the measurements that go into the thesis
have to be taken on the configuration that is actually shipped.

## One WebSocket connection per user

Not a socket per channel. The connection subscribes to all of the user's channels at once
(`ForUser` on connect). Anything that is stored or needs a status code goes over REST; the
socket accepts only ephemeral frames such as "typing", which store nothing, answer nothing,
and are validated against the hub's in-memory routing rather than against MongoDB. This is
the same split as in Mattermost, Revolt and Rocket.Chat. A subscriber carries its own set of
channels and a `dropped` flag; eviction removes it from every channel before `close(send)`
and is therefore idempotent.

## The schema follows the queries

The standard MongoDB approach: before a new collection or field, the question is which
concrete queries will hit it. Denormalisation is welcome where an access pattern justifies
it (extended reference, subset).

## Deliberately out of scope

Kafka, NATS, gRPC and Kubernetes are left out: a single Redis Pub/Sub channel carries what
has to cross process boundaries here, and the hubs of Mattermost, Revolt and Rocket.Chat
solve the same problem without a broker either. JWT was dropped in favour of server-side
sessions, which can be revoked.
