package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	purgeEvery = 5 * time.Minute
	// A message that passed its membership check just before the channel was
	// marked lands moments later; waiting this long purges it with the rest.
	purgeGrace = 10 * time.Minute
	purgeLease = 5 * time.Minute
	purgeBatch = 1000

	fileSweepEvery = time.Hour
	fileSweeperKey = "files:sweeper"
	// A file younger than this with no record may still be getting one.
	fileMinAge = time.Hour
	// A crash leaves a file or two; more than this means the media collection
	// itself is wrong, and that is for a person to look at.
	fileSweepLimit = 100
)

var errClaimLost = errors.New("another node took the channel over")

// runPurge removes the channels Leave marked. It runs on every node: claims
// keep two nodes off the same channel, so no round needs a leader.
func (s *server) runPurge(ctx context.Context) {
	runEvery(ctx, sweepStart(), purgeEvery, func(time.Time) {
		if err := s.purgeDiscarded(ctx, s.presence.nodeID, purgeBatch); err != nil {
			log.Printf("purge: %v", err)
		}
	})
}

// purgeDiscarded claims marked channels one at a time until none is left to
// take. A channel that fails stays claimed until the claim runs out, so the
// loop moves on to the next one, and a later round retries it.
func (s *server) purgeDiscarded(ctx context.Context, owner string, batch int) error {
	for {
		id, ok, err := s.channels.claimDiscarded(ctx, owner, time.Now())
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if err := s.purgeChannel(ctx, id, owner, batch); err != nil {
			log.Printf("purge: channel %s: %v", id.Hex(), err)
		}
	}
}

// purgeChannel deletes a claimed channel's data, then the channel itself. The
// channel goes last: while it exists, its mark is the record of what is left
// to do, and a purge cut short anywhere is picked up again from it.
func (s *server) purgeChannel(ctx context.Context, id bson.ObjectID, owner string, batch int) error {
	for {
		n, err := s.messages.deleteBatch(ctx, id, batch)
		if err != nil {
			return err
		}
		if n == 0 {
			break
		}
		// A long history takes many batches, and the claim has to outlive them.
		held, err := s.channels.renewClaim(ctx, id, owner, time.Now())
		if err != nil {
			return err
		}
		if !held {
			return errClaimLost
		}
	}
	if _, err := s.invites.col.DeleteMany(ctx, bson.M{"channel_id": id}); err != nil {
		return fmt.Errorf("deleting invites: %w", err)
	}
	if err := s.media.purgeChannel(ctx, id); err != nil {
		return err
	}
	dropped, err := s.channels.dropDiscarded(ctx, id, owner)
	if err != nil {
		return err
	}
	if !dropped {
		return errClaimLost
	}
	return nil
}

// runFileSweep deletes stored files no media record names. It is the one place
// left that infers garbage from absence, so one node does it per round and it
// stops short of anything that looks like a broken database.
func (s *server) runFileSweep(ctx context.Context) {
	runEvery(ctx, sweepStart(), fileSweepEvery, func(now time.Time) {
		if !s.takeFileRound(ctx) {
			return
		}
		n, err := s.media.deleteUnreferencedBefore(ctx, now.Add(-fileMinAge), fileSweepLimit)
		if err != nil {
			log.Printf("file sweep: %v", err)
		}
		if n > 0 {
			log.Printf("file sweep: deleted %d unreferenced files", n)
		}
	})
}

// takeFileRound lets one node sweep per round, the way presence does. The key
// only saves the other nodes the work: the sweep is idempotent, so with Redis
// down every node sweeps rather than none.
func (s *server) takeFileRound(ctx context.Context) bool {
	ok, err := s.presence.takeRound(ctx, fileSweeperKey, fileSweepEvery)
	if err != nil {
		log.Printf("file sweep: %v", err)
		return true
	}
	return ok
}
