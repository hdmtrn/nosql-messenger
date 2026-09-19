package main

import (
	"context"
	"log"
	"strings"
	"time"
)

// runPresence keeps this node's pulse and cleans up after the nodes that
// stopped beating. A dead node announces nothing itself: its users would stay
// online forever, and their last_seen would never be written.
func (s *server) runPresence(ctx context.Context) {
	ticker := time.NewTicker(presenceBeat)
	defer ticker.Stop()

	// The first beat introduces this node, so being unknown then is normal.
	first := true
	for {
		s.presenceRound(ctx, first)
		first = false

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *server) presenceRound(ctx context.Context, first bool) {
	beatCtx, cancel := context.WithTimeout(ctx, presenceTimeout)
	defer cancel()

	known, err := s.presence.beat(beatCtx)
	if err != nil {
		log.Printf("presence: %v", err)
		return
	}
	if !known && !first {
		s.reregister(beatCtx)
	}

	dead, err := s.presence.listen(beatCtx)
	if err != nil {
		log.Printf("presence: %v", err)
		return
	}
	if len(dead) == 0 {
		return
	}

	// One sweeper per round: the work is the same whoever does it, and doing it
	// N times over would only race for the same claims.
	sweeping, err := s.presence.sweeping(beatCtx)
	if err != nil {
		log.Printf("presence: %v", err)
		return
	}
	if !sweeping {
		return
	}

	for _, node := range dead {
		s.sweepNode(ctx, node)
	}
}

// sweepNode takes the sockets a dead node left in Redis and closes them the way
// their own node would have.
func (s *server) sweepNode(ctx context.Context, nodeID string) {
	ctx, cancel := context.WithTimeout(ctx, presenceBeat)
	defer cancel()

	mine, err := s.presence.claim(ctx, nodeID)
	if err != nil {
		log.Printf("presence: %v", err)
		return
	}
	if !mine {
		return
	}

	sockets, err := s.presence.socketsOf(ctx, nodeID)
	if err != nil {
		log.Printf("presence: %v", err)
		return
	}

	for _, entry := range sockets {
		userID, socketID, ok := strings.Cut(entry, ":")
		if !ok {
			log.Printf("presence: unreadable socket %q of node %s", entry, nodeID)
			continue
		}

		state, changed, err := s.presence.remove(ctx, userID, socketID)
		if err != nil {
			log.Printf("presence: %v", err)
			continue
		}
		if !changed {
			continue
		}
		s.announcePresence(userID, state, s.markLastSeen(ctx, userID), s.channelsOf(ctx, userID))
	}

	s.presence.forget(ctx, nodeID)
	log.Printf("presence: swept %d sockets of node %s", len(sockets), nodeID)
}

// reregister puts this node's sockets back after Redis lost them, so that the
// people they belong to do not stay offline until they reload.
func (s *server) reregister(ctx context.Context) {
	sockets := s.hub.Subscribers()
	for _, c := range sockets {
		state, changed, err := s.presence.connect(ctx, c.userID, c.socketID)
		if err != nil {
			log.Printf("presence: %v", err)
			continue
		}
		if changed {
			s.announcePresence(c.userID, state, nil, s.hub.ChannelsOf(c))
		}
	}
	log.Printf("presence: Redis had forgotten this node, %d sockets registered again", len(sockets))
}
