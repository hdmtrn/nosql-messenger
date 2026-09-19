package main

import (
	"encoding/json"
	"log"
	"time"
)

// A client repeats "typing" about every three seconds while the user types. A
// faster repeat is not a user typing, and each frame fans out to every reader
// of the channel on every node, so the extra ones are dropped. Mattermost
// refuses to configure its own interval below one second for the same reason.
const typingMinInterval = time.Second

// The gap between any two typing frames of one connection, whatever channel
// they name. It is checked before anything that takes a lock: the per-channel
// interval alone would not stop a flood that names a new channel id in every
// frame, and each of those would take the hub's global mutex in Reads — the
// mutex every broadcast on the node waits for. A real client sends one frame
// every few seconds, so this never touches it.
const typingMinGap = 100 * time.Millisecond

// A frame from a client. Only ephemeral things come this way — anything that is
// stored or needs a status code goes through REST.
type clientFrame struct {
	Type      string `json:"type"`
	ChannelID string `json:"channel_id"`
}

// typingEvent shares the channel's topic with messages, so it carries a type
// that a message never has; the client tells the two apart by it.
type typingEvent struct {
	Type      string        `json:"type"`
	ChannelID string        `json:"channel_id"`
	User      MessageAuthor `json:"user"`
}

// handleFrame runs on the connection's read goroutine. A frame that is not
// understood, or names a channel this socket does not read, is ignored without
// an answer: nobody is waiting for one, as with Revolt's BeginTyping.
func (s *server) handleFrame(c *Subscriber, sess Session, raw []byte) {
	var f clientFrame
	if err := json.Unmarshal(raw, &f); err != nil || f.Type != "typing" {
		return
	}
	s.typing(c, sess, f.ChannelID)
}

// typing announces that the user is typing in a channel. Nothing is stored:
// it is worth something for a few seconds and never again, which is why it
// goes through the bus and not through MongoDB — the reason Redis was picked
// over change streams, which can only carry what was written.
//
// There is no "stopped typing" event. A listener forgets the typist a little
// later than the next repeat would come (3 s and 4 s; Telegram: 5 s and 6 s),
// and a message from the typist clears it at once. A lost event costs a
// flicker, and a closed tab needs no cleanup.
func (s *server) typing(c *Subscriber, sess Session, chID string) {
	// The cheap checks come first and take no lock: both fields belong to this
	// connection's read goroutine. A dropped frame still moves lastTyping, so a
	// flood stays dropped for as long as it lasts.
	now := time.Now()
	if now.Sub(c.lastTyping) < typingMinGap {
		return
	}
	c.lastTyping = now
	if now.Sub(c.typedAt[chID]) < typingMinInterval {
		return
	}

	// Membership comes from the hub, not from MongoDB: this socket is routed to
	// exactly the channels its user is in, and asking costs a map lookup. Only
	// a channel that passed it is remembered, so typedAt holds this user's
	// channels and not every id a client ever sent.
	if !s.hub.Reads(c, chID) {
		return
	}
	c.typedAt[chID] = now

	payload, err := json.Marshal(typingEvent{
		Type:      "typing",
		ChannelID: chID,
		User:      MessageAuthor{ID: sess.UserID, Username: sess.Username},
	})
	if err != nil {
		log.Printf("encoding typing event: %v", err)
		return
	}
	s.bus.Publish(chID, payload)
}
