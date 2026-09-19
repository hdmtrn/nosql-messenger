package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// A user is online while they hold a socket on a node that is still beating.
// Nothing here survives a restart of Redis, and nothing here has to: the
// durable side of presence is last_seen_at in MongoDB.
const (
	// The user's sockets. A socket id carries the node it lives on, so this
	// set also says which nodes hold the user.
	presenceSocketsKey = "presence:sockets:"
	// The same pairs indexed by node: who to announce when a node dies.
	presenceNodeKey = "presence:node:"
	// Events about one user come from different nodes, and Pub/Sub keeps no
	// order between publishers. The version says which event is newer.
	presenceVersionKey = "presence:version:"
	// A node's pulse, and the registry to look for a missing pulse in.
	presenceAliveKey = "presence:alive:"
	presenceNodesKey = "presence:nodes"
	// Held for one round by the node that cleans up after the dead ones.
	presenceSweeperKey = "presence:sweeper"
	// A version means something only within one life of Redis; the epoch says
	// which life. Taken from the clock, so it grows across restarts.
	presenceEpochKey = "presence:epoch"

	presenceBeat     = 10 * time.Second
	presenceAliveFor = 30 * time.Second
	// A socket that is closing has no request context left to borrow.
	presenceTimeout = 2 * time.Second
)

type presence struct {
	rdb    *redis.Client
	nodeID string

	// What this node believes about the others, refreshed every beat: read on
	// every connect and lookup, written once a round.
	mu    sync.RWMutex
	alive map[string]struct{}
	epoch int64
}

func newPresence(rdb *redis.Client) *presence {
	id := bson.NewObjectID().Hex()
	return &presence{
		rdb:    rdb,
		nodeID: id,
		alive:  map[string]struct{}{id: {}},
	}
}

// Socket ids are <node>:<socket>, so one set answers both "how many sockets"
// and "on which nodes".
func (p *presence) socketID(socket string) string { return p.nodeID + ":" + socket }

func nodeOf(socketID string) string {
	node, _, _ := strings.Cut(socketID, ":")
	return node
}

// onlineIn ignores sockets left behind by a node that stopped beating, so a
// read is right as soon as the pulse expires, without waiting for the sweep.
func (p *presence) onlineIn(sockets []string, except string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, s := range sockets {
		if s == except {
			continue
		}
		if _, ok := p.alive[nodeOf(s)]; ok {
			return true
		}
	}
	return false
}

func (p *presence) epochValue() int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.epoch
}

type presenceState struct {
	Online  bool  `json:"online"`
	Version int64 `json:"version"`
	Epoch   int64 `json:"epoch"`
}

// connect records an open socket. changed is true when it brought the user
// online, which is the only case the other nodes have to hear about.
func (p *presence) connect(ctx context.Context, userID, socket string) (state presenceState, changed bool, err error) {
	full := p.socketID(socket)
	var added, version *redis.IntCmd
	var members *redis.StringSliceCmd

	// MULTI/EXEC: both indexes move together, and nothing lands between the
	// change and the reading of what it produced.
	_, err = p.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		added = pipe.SAdd(ctx, presenceSocketsKey+userID, full)
		pipe.SAdd(ctx, presenceNodeKey+p.nodeID, userID+":"+full)
		members = pipe.SMembers(ctx, presenceSocketsKey+userID)
		version = pipe.Incr(ctx, presenceVersionKey+userID)
		return nil
	})
	if err != nil {
		return presenceState{}, false, fmt.Errorf("recording socket %s of %s: %w", full, userID, err)
	}

	state = presenceState{Online: true, Version: version.Val(), Epoch: p.epochValue()}
	return state, added.Val() == 1 && !p.onlineIn(members.Val(), full), nil
}

// disconnect forgets a socket of this node; changed is true when the user has
// no live socket left anywhere.
func (p *presence) disconnect(ctx context.Context, userID, socket string) (presenceState, bool, error) {
	return p.remove(ctx, userID, p.socketID(socket))
}

// remove takes any socket, this node's or a dead node's, which is what the
// sweep needs. Removing a socket twice changes nothing, as with the hub's drop.
func (p *presence) remove(ctx context.Context, userID, socketID string) (state presenceState, changed bool, err error) {
	var removed, version *redis.IntCmd
	var members *redis.StringSliceCmd

	_, err = p.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		removed = pipe.SRem(ctx, presenceSocketsKey+userID, socketID)
		pipe.SRem(ctx, presenceNodeKey+nodeOf(socketID), userID+":"+socketID)
		members = pipe.SMembers(ctx, presenceSocketsKey+userID)
		version = pipe.Incr(ctx, presenceVersionKey+userID)
		return nil
	})
	if err != nil {
		return presenceState{}, false, fmt.Errorf("forgetting socket %s of %s: %w", socketID, userID, err)
	}

	online := p.onlineIn(members.Val(), "")
	state = presenceState{Online: online, Version: version.Val(), Epoch: p.epochValue()}
	return state, removed.Val() == 1 && !online, nil
}

// lookup answers for many users in one round trip. No MULTI: the answers are
// about different users and need not agree on one moment.
func (p *presence) lookup(ctx context.Context, userIDs []string) (map[string]presenceState, error) {
	if len(userIDs) == 0 {
		return map[string]presenceState{}, nil
	}

	sockets := make([]*redis.StringSliceCmd, len(userIDs))
	versions := make([]*redis.StringCmd, len(userIDs))

	_, err := p.rdb.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for i, id := range userIDs {
			sockets[i] = pipe.SMembers(ctx, presenceSocketsKey+id)
			versions[i] = pipe.Get(ctx, presenceVersionKey+id)
		}
		return nil
	})
	// A user who never connected has no version key: expected, not an error.
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("looking up presence: %w", err)
	}

	epoch := p.epochValue()
	out := make(map[string]presenceState, len(userIDs))
	for i, id := range userIDs {
		version, err := versions[i].Int64()
		if err != nil && err != redis.Nil {
			return nil, fmt.Errorf("reading the presence version of %s: %w", id, err)
		}
		out[id] = presenceState{
			Online:  p.onlineIn(sockets[i].Val(), ""),
			Version: version,
			Epoch:   epoch,
		}
	}
	return out, nil
}

// presenceEvent shares the channel's topic with messages and typing, so it
// carries a type the client tells them apart by.
type presenceEvent struct {
	Type    string `json:"type"`
	UserID  string `json:"user_id"`
	Online  bool   `json:"online"`
	Version int64  `json:"version"`
	Epoch   int64  `json:"epoch"`
	// Only in an offline event: while the user is online there is nothing to
	// tell, as with Telegram's userStatusOffline{was_online}.
	LastSeen *time.Time `json:"last_seen,omitempty"`
}

// socketOpened and socketClosed record the socket and, when the user crossed
// between offline and online, tell the channels they share with others. Without
// a presence store both do nothing, which is what the tests of the socket want.
func (s *server) socketOpened(ctx context.Context, c *Subscriber, channels []string) {
	if s.presence == nil {
		return
	}
	state, changed, err := s.presence.connect(ctx, c.userID, c.socketID)
	if err != nil {
		log.Printf("presence: %v", err)
		return
	}
	if changed {
		s.announcePresence(c.userID, state, nil, channels)
	}
}

func (s *server) socketClosed(c *Subscriber, channels []string) {
	if s.presence == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), presenceTimeout)
	defer cancel()

	state, changed, err := s.presence.disconnect(ctx, c.userID, c.socketID)
	if err != nil {
		log.Printf("presence: %v", err)
		return
	}
	if !changed {
		return
	}
	s.announcePresence(c.userID, state, s.markLastSeen(ctx, c.userID), channels)
}

// markLastSeen records when the user's last socket closed. A failed write costs
// a stale "last seen", not the offline event, so the error is only logged.
func (s *server) markLastSeen(ctx context.Context, userID string) *time.Time {
	if s.users == nil {
		return nil
	}
	id, err := bson.ObjectIDFromHex(userID)
	if err != nil {
		log.Printf("presence: unreadable user id %q", userID)
		return nil
	}
	at := time.Now().UTC()
	if err := s.users.SetLastSeen(ctx, id, at); err != nil {
		log.Printf("presence: recording the last seen of %s: %v", userID, err)
		return nil
	}
	return &at
}

// announcePresence publishes once per channel the user is in: a reader of any
// of them may be watching, and nobody knows which node holds their socket.
func (s *server) announcePresence(userID string, state presenceState, lastSeen *time.Time, channels []string) {
	payload, err := json.Marshal(presenceEvent{
		Type:     "presence",
		UserID:   userID,
		Online:   state.Online,
		Version:  state.Version,
		Epoch:    state.Epoch,
		LastSeen: lastSeen,
	})
	if err != nil {
		log.Printf("presence: encoding an event for %s: %v", userID, err)
		return
	}
	for _, chID := range channels {
		s.bus.Publish(chID, payload)
	}
}

// beat writes this node's pulse and refreshes the epoch. known is false when
// Redis had forgotten this node — after its own restart, or when another node
// swept this one as dead while it was only slow.
func (p *presence) beat(ctx context.Context) (known bool, err error) {
	var joined *redis.IntCmd
	var epoch *redis.StringCmd

	_, err = p.rdb.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, presenceAliveKey+p.nodeID, "1", presenceAliveFor)
		joined = pipe.SAdd(ctx, presenceNodesKey, p.nodeID)
		pipe.SetNX(ctx, presenceEpochKey, time.Now().UnixMilli(), 0)
		epoch = pipe.Get(ctx, presenceEpochKey)
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("beating for node %s: %w", p.nodeID, err)
	}

	value, err := epoch.Int64()
	if err != nil {
		return false, fmt.Errorf("reading the presence epoch: %w", err)
	}

	p.mu.Lock()
	p.epoch = value
	p.mu.Unlock()

	return joined.Val() == 0, nil
}

// listen refreshes what this node believes about the others and returns the
// ones whose pulse is gone.
func (p *presence) listen(ctx context.Context) ([]string, error) {
	nodes, err := p.rdb.SMembers(ctx, presenceNodesKey).Result()
	if err != nil {
		return nil, fmt.Errorf("listing the nodes: %w", err)
	}

	beats := make([]*redis.IntCmd, len(nodes))
	_, err = p.rdb.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for i, id := range nodes {
			beats[i] = pipe.Exists(ctx, presenceAliveKey+id)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listening for the nodes: %w", err)
	}

	alive := map[string]struct{}{p.nodeID: {}}
	var dead []string
	for i, id := range nodes {
		switch {
		case beats[i].Val() == 1:
			alive[id] = struct{}{}
		case id != p.nodeID:
			dead = append(dead, id)
		}
	}

	p.mu.Lock()
	p.alive = alive
	p.mu.Unlock()

	return dead, nil
}

// sweeping takes the right to clean up for one round. The key expires by
// itself, so a node that dies mid-sweep does not block the next one.
func (p *presence) sweeping(ctx context.Context) (bool, error) {
	ok, err := p.rdb.SetNX(ctx, presenceSweeperKey, p.nodeID, presenceBeat).Result()
	if err != nil {
		return false, fmt.Errorf("taking the sweeper key: %w", err)
	}
	return ok, nil
}

// claim hands the dead node to exactly one cleaner: Redis is single-threaded,
// so only one caller sees the registry entry actually disappear.
func (p *presence) claim(ctx context.Context, nodeID string) (bool, error) {
	removed, err := p.rdb.SRem(ctx, presenceNodesKey, nodeID).Result()
	if err != nil {
		return false, fmt.Errorf("claiming node %s: %w", nodeID, err)
	}
	return removed == 1, nil
}

// socketsOf returns the <user>:<socket> pairs a node held.
func (p *presence) socketsOf(ctx context.Context, nodeID string) ([]string, error) {
	members, err := p.rdb.SMembers(ctx, presenceNodeKey+nodeID).Result()
	if err != nil {
		return nil, fmt.Errorf("reading the sockets of node %s: %w", nodeID, err)
	}
	return members, nil
}

func (p *presence) forget(ctx context.Context, nodeID string) {
	if err := p.rdb.Del(ctx, presenceNodeKey+nodeID).Err(); err != nil {
		log.Printf("presence: dropping the sockets of node %s: %v", nodeID, err)
	}
}
