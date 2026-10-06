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

// Removing a friend ends the friendship and leaves the conversation: both stay
// in it and can still write there. Either side can do it, and doing it twice
// is not an error and tells nobody anything.
func TestRemovingAFriendKeepsTheConversation(t *testing.T) {
	for _, remover := range []string{"the one who asked", "the one who accepted"} {
		t.Run(remover, func(t *testing.T) {
			ctx := context.Background()
			db := testDB(t)
			users, friends := newUserStore(db), newFriendStore(db)
			channels, messages := newMessageTestStores(t, db)
			hub := NewHub()
			s := &server{hub: hub, bus: hub, users: users, friends: friends, channels: channels, messages: messages}

			mara, nik := person("mara"), person("nik")
			for _, u := range []Session{mara, nik} {
				if err := users.Create(ctx, &User{ID: u.UserID, Username: u.Username, DisplayName: u.Username}); err != nil {
					t.Fatalf("creating %s: %v", u.Username, err)
				}
			}
			req, err := friends.Send(ctx, mara, &User{ID: nik.UserID, Username: nik.Username})
			if err != nil {
				t.Fatalf("sending: %v", err)
			}
			if _, err := friends.Respond(ctx, req.ID, nik, friendAccepted); err != nil {
				t.Fatalf("accepting: %v", err)
			}
			ch, err := channels.Direct(ctx, mara, &User{ID: nik.UserID, Username: nik.Username})
			if err != nil {
				t.Fatalf("opening the conversation: %v", err)
			}

			by, other := mara, nik
			if remover == "the one who accepted" {
				by, other = nik, mara
			}
			maraSocket, nikSocket := newSubscriber(mara.UserID.Hex()), newSubscriber(nik.UserID.Hex())
			hub.Connect(maraSocket, nil)
			hub.Connect(nikSocket, nil)

			if code, body := removeFriend(s, by, other.UserID); code != http.StatusOK {
				t.Fatalf("removing gave %d, want 200: %s", code, body)
			}
			expect(t, maraSocket, "mara was not told the friendship ended")
			expect(t, nikSocket, "nik was not told the friendship ended")
			if still, err := friends.AreFriends(ctx, mara.UserID, nik.UserID); err != nil || still {
				t.Fatalf("still friends after removing (err = %v)", err)
			}

			for _, from := range []Session{mara, nik} {
				send := map[string]string{"channel_id": ch.ID.Hex(), "text": "still here", "client_msg_id": from.Username}
				if code, body := callMessageHandler(t, s.handleSendMessage, "", send, from); code != http.StatusCreated {
					t.Fatalf("%s writing in the conversation got %d, want 201: %s", from.Username, code, body)
				}
			}

			if code, body := removeFriend(s, by, other.UserID); code != http.StatusOK {
				t.Fatalf("removing again gave %d, want 200: %s", code, body)
			}
			for _, c := range []*Subscriber{maraSocket, nikSocket} {
				select {
				case raw := <-c.send:
					t.Fatalf("removing again was announced: %s", raw)
				default:
				}
			}

			if _, err := friends.Send(ctx, nik, &User{ID: mara.UserID, Username: mara.Username}); err != nil {
				t.Fatalf("asking again after removing: %v", err)
			}
		})
	}
}

func removeFriend(s *server, by Session, id bson.ObjectID) (int, []byte) {
	r := httptest.NewRequest(http.MethodDelete, "/friends/"+id.Hex(), nil)
	r.SetPathValue("id", id.Hex())
	w := httptest.NewRecorder()
	s.handleRemoveFriend(w, r, by)
	return w.Code, w.Body.Bytes()
}

func sendFriendRequest(s *server, from Session, username string) (int, []byte) {
	body := bytes.NewReader([]byte(`{"username":"` + username + `"}`))
	r := httptest.NewRequest(http.MethodPost, "/friends/requests", body)
	w := httptest.NewRecorder()
	s.handleSendFriendRequest(w, r, from)
	return w.Code, w.Body.Bytes()
}
