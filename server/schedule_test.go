package main

import (
	"context"
	"testing"
	"time"
)

func TestRunEveryDoesNotWaitAFullInterval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ran := make(chan struct{}, 1)
	go runEvery(ctx, time.Millisecond, time.Hour, func(time.Time) {
		select {
		case ran <- struct{}{}:
		default:
		}
	})

	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatalf("the first run waited for the interval")
	}
}

func TestRunEveryKeepsGoing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ran := make(chan struct{}, 3)
	go runEvery(ctx, time.Millisecond, time.Millisecond, func(time.Time) {
		select {
		case ran <- struct{}{}:
		default:
		}
	})

	for i := range 3 {
		select {
		case <-ran:
		case <-time.After(5 * time.Second):
			t.Fatalf("run %d never came", i+1)
		}
	}
}
