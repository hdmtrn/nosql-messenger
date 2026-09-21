package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Two strangers share no channel, so no channel topic could have carried this:
// the request travels to the recipient's own topic, and the answer back to the
// sender's. Without it either side sees the other's move only after a reload.
func TestFriendRequestAndItsAnswerReachTheOtherSide(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	users := newUserStore(db)

	hub := NewHub()
	s := &server{hub: hub, bus: hub, users: users, friends: newFriendStore(db)}

	mara := Session{UserID: bson.NewObjectID(), Username: "mara"}
	nik := Session{UserID: bson.NewObjectID(), Username: "nik"}
	for _, u := range []Session{mara, nik} {
		if err := users.Create(ctx, &User{ID: u.UserID, Username: u.Username, DisplayName: u.Username}); err != nil {
			t.Fatalf("creating %s: %v", u.Username, err)
		}
	}

	maraSocket := newSubscriber(mara.UserID.Hex())
	nikSocket := newSubscriber(nik.UserID.Hex())
	hub.Connect(maraSocket, nil)
	hub.Connect(nikSocket, nil)

	body := bytes.NewReader([]byte(`{"username":"nik"}`))
	r := httptest.NewRequest(http.MethodPost, "/friends/requests", body)
	w := httptest.NewRecorder()
	s.handleSendFriendRequest(w, r, mara)
	if w.Code != http.StatusCreated {
		t.Fatalf("sending gave %d, want 201: %s", w.Code, w.Body)
	}
	expect(t, nikSocket, "the request did not reach nik")
	// The sender hears it too: this tab was answered over HTTP, their others were not.
	expect(t, maraSocket, "mara's other tabs heard nothing about her own request")

	var req FriendRequest
	if err := json.Unmarshal(w.Body.Bytes(), &req); err != nil {
		t.Fatalf("decoding %s: %v", w.Body.Bytes(), err)
	}

	r = httptest.NewRequest(http.MethodPost, "/friends/requests/"+req.ID.Hex()+"/accept", nil)
	r.SetPathValue("id", req.ID.Hex())
	w = httptest.NewRecorder()
	s.handleAcceptFriendRequest(w, r, nik)
	if w.Code != http.StatusOK {
		t.Fatalf("accepting gave %d, want 200: %s", w.Code, w.Body)
	}
	expect(t, maraSocket, "mara was not told her request was accepted")
	expect(t, nikSocket, "nik's other tabs heard nothing about the answer")
}

func expect(t *testing.T, c *Subscriber, complaint string) {
	t.Helper()
	select {
	case raw := <-c.send:
		if string(raw) != string(friendsPayload) {
			t.Fatalf("got %s, want %s", raw, friendsPayload)
		}
	default:
		t.Fatal(complaint)
	}
}
