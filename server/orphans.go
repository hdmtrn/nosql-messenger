package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const (
	orphanSweepInterval = time.Hour
	orphanSweeperKey    = "orphans:sweeper"
	// A file younger than this with no record may still be getting one.
	orphanFileMinAge = time.Hour
)

// runOrphanSweep collects what no channel reaches any more: what discard left
// when it stopped past its commit point, and writes that passed a membership
// check just before their channel went.
func (s *server) runOrphanSweep(ctx context.Context) {
	runEvery(ctx, sweepStart(), orphanSweepInterval, func(now time.Time) {
		if !s.takeOrphanRound(ctx) {
			return
		}
		if err := s.sweepOrphans(ctx, now); err != nil {
			log.Printf("orphans: %v", err)
		}
	})
}

// takeOrphanRound lets one node sweep per round, the way presence does. The key
// only saves the other nodes the work: the sweep is idempotent, so with Redis
// down every node sweeps rather than none.
func (s *server) takeOrphanRound(ctx context.Context) bool {
	ok, err := s.presence.takeRound(ctx, orphanSweeperKey, orphanSweepInterval)
	if err != nil {
		log.Printf("orphans: %v", err)
		return true
	}
	return ok
}

// sweepOrphans goes on past a collection it cannot read: one bad document must
// not keep the others from being swept every round after.
func (s *server) sweepOrphans(ctx context.Context, now time.Time) error {
	var errs []error
	gone := map[bson.ObjectID]bool{}
	for _, col := range []*mongo.Collection{s.messages.col, s.invites.col, s.media.col} {
		ids, err := s.channels.missingFrom(ctx, col)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, id := range ids {
			gone[id] = true
		}
	}

	for id := range gone {
		if err := s.channels.purge(ctx, s.messages, s.invites, s.media, id); err != nil {
			errs = append(errs, fmt.Errorf("purging channel %s: %w", id.Hex(), err))
		}
	}
	files, err := s.media.deleteUnreferencedBefore(ctx, now.Add(-orphanFileMinAge))
	if err != nil {
		errs = append(errs, err)
	}

	if len(gone) > 0 || files > 0 {
		log.Printf("orphans: purged %d gone channels, deleted %d unreferenced files", len(gone), files)
	}
	return errors.Join(errs...)
}
