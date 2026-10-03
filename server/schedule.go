package main

import (
	"context"
	"math/rand/v2"
	"time"
)

const (
	firstSweepDelay  = 2 * time.Minute
	firstSweepJitter = 5 * time.Minute
)

// sweepStart is when a background sweep first runs after the process starts.
// The jitter keeps the nodes that one deploy restarted from all arriving at
// the same second.
func sweepStart() time.Duration {
	return firstSweepDelay + rand.N(firstSweepJitter)
}

// runEvery calls fn after first, and then every interval until ctx ends. A
// ticker started at boot fires only after a full interval, so a node restarted
// more often than that would never get to run fn at all.
func runEvery(ctx context.Context, first, every time.Duration, fn func(now time.Time)) {
	timer := time.NewTimer(first)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-timer.C:
			fn(now)
			timer.Reset(every)
		}
	}
}
