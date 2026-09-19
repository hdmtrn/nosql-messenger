package main

import (
	"encoding/json"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// typingSetup connects a typist and a watcher to one channel. No writePump reads
// the watcher, so every event stays in its buffer and len(send) counts them.
func typingSetup() (s *server, typist, watcher *Subscriber, sess Session, chID string) {
	h := NewHub()
	s = &server{hub: h, bus: h}
	sess = Session{UserID: bson.NewObjectID(), Username: "owner"}
	chID = bson.NewObjectID().Hex()

	typist = newSubscriber(sess.UserID.Hex())
	watcher = newSubscriber("watcher")
	h.Connect(typist, []string{chID})
	h.Connect(watcher, []string{chID})
	return
}

func typingFrame(chID string) []byte {
	return []byte(`{"type":"typing","channel_id":"` + chID + `"}`)
}

func TestTypingReachesTheChannel(t *testing.T) {
	s, typist, watcher, sess, chID := typingSetup()

	s.handleFrame(typist, sess, typingFrame(chID))

	if n := len(watcher.send); n != 1 {
		t.Fatalf("watcher got %d events, want 1", n)
	}
	var ev typingEvent
	if err := json.Unmarshal(<-watcher.send, &ev); err != nil {
		t.Fatalf("decoding event: %v", err)
	}
	if ev.Type != "typing" || ev.ChannelID != chID || ev.User.Username != "owner" {
		t.Fatalf("event %+v, want owner typing in %s", ev, chID)
	}
}

// A socket is routed only to its user's channels, so naming another one must
// reach nobody — this is the whole membership check.
func TestTypingOnlyInChannelsTheSocketReads(t *testing.T) {
	s, _, watcher, _, chID := typingSetup()

	stranger := newSubscriber("mallory")
	s.hub.Connect(stranger, nil)
	s.handleFrame(stranger, Session{Username: "mallory"}, typingFrame(chID))

	if n := len(watcher.send); n != 0 {
		t.Fatalf("watcher got %d events from a stranger, want 0", n)
	}
}

func TestTypingRepeatedTooFastIsDropped(t *testing.T) {
	s, typist, watcher, sess, chID := typingSetup()

	s.handleFrame(typist, sess, typingFrame(chID))
	// Past the per-connection gap, so only the per-channel interval can drop it.
	typist.lastTyping = time.Time{}
	s.handleFrame(typist, sess, typingFrame(chID))

	if n := len(watcher.send); n != 1 {
		t.Fatalf("watcher got %d events for two frames within %v, want 1", n, typingMinInterval)
	}
}

func TestUnknownFramesAreIgnored(t *testing.T) {
	s, typist, watcher, sess, chID := typingSetup()

	for _, frame := range []string{
		`not json`,
		`{"type":"message","channel_id":"` + chID + `"}`,
		`{"channel_id":"` + chID + `"}`,
	} {
		s.handleFrame(typist, sess, []byte(frame))
	}

	if n := len(watcher.send); n != 0 {
		t.Fatalf("watcher got %d events from frames that are not typing, want 0", n)
	}
}

// A flood naming a new channel id in every frame gets past the per-channel
// interval, and each frame would take the hub's mutex in Reads. The
// per-connection gap drops it before any lock: the real channel right after
// the flood is dropped too, which a client typing every few seconds never hits.
func TestTypingFloodAcrossChannelsIsCapped(t *testing.T) {
	s, typist, watcher, sess, chID := typingSetup()

	for range 50 {
		s.handleFrame(typist, sess, typingFrame(bson.NewObjectID().Hex()))
	}
	s.handleFrame(typist, sess, typingFrame(chID))

	if n := len(watcher.send); n != 0 {
		t.Fatalf("watcher got %d events right after a flood, want 0", n)
	}
	if n := len(typist.typedAt); n != 0 {
		t.Fatalf("typedAt holds %d channels from the flood, want 0", n)
	}
}
