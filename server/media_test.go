package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatalf("encoding png: %v", err)
	}
	return buf.Bytes()
}

func uploadMedia(s *server, body []byte, sess Session) (int, []byte) {
	r := httptest.NewRequest(http.MethodPost, "/media", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleUploadMedia(w, r, sess)
	return w.Code, w.Body.Bytes()
}

func getMedia(s *server, id string, sess Session) (int, []byte) {
	r := httptest.NewRequest(http.MethodGet, "/media/"+id, nil)
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	s.handleGetMedia(w, r, sess)
	return w.Code, w.Body.Bytes()
}

func TestUploadRejectsWhatIsNotAnImage(t *testing.T) {
	db := testDB(t)
	s := &server{media: newMediaStore(db)}
	alice := person("alice")

	if code, body := uploadMedia(s, []byte("<svg onload=alert(1)></svg>"), alice); code != http.StatusUnsupportedMediaType {
		t.Fatalf("svg upload: got %d %s, want 415", code, body)
	}

	big := append(testPNG(t, 1, 1), make([]byte, mediaMaxBytes)...)
	if code, body := uploadMedia(s, big, alice); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload: got %d %s, want 413", code, body)
	}
}

func TestMediaIsReadableOnlyWhereItWasSent(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, _ := newMessageTestStores(t, db)
	s := &server{channels: channels, media: newMediaStore(db)}

	alice, bob, mallory := person("alice"), person("bob"), person("mallory")
	room := createChannel(t, channels, "room", alice)
	if err := channels.AddMember(ctx, room.ID, bob); err != nil {
		t.Fatalf("adding bob: %v", err)
	}

	img := testPNG(t, 3, 2)
	code, body := uploadMedia(s, img, alice)
	if code != http.StatusCreated {
		t.Fatalf("upload: got %d %s, want 201", code, body)
	}
	var att Attachment
	if err := json.Unmarshal(body, &att); err != nil {
		t.Fatalf("decoding attachment: %v", err)
	}
	if att.ContentType != "image/png" || att.Width != 3 || att.Height != 2 {
		t.Fatalf("attachment is %+v, want image/png 3x2", att)
	}
	id := att.ID.Hex()

	if code, got := getMedia(s, id, alice); code != http.StatusOK || !bytes.Equal(got, img) {
		t.Fatalf("owner before sending: got %d and %d bytes, want 200 and the uploaded %d", code, len(got), len(img))
	}
	if code, _ := getMedia(s, id, bob); code != http.StatusNotFound {
		t.Fatalf("bob before sending: got %d, want 404", code)
	}

	if _, err := s.media.col.UpdateOne(ctx, bson.M{"_id": att.ID}, bson.M{"$set": bson.M{"channel_id": room.ID}}); err != nil {
		t.Fatalf("attaching media: %v", err)
	}

	if code, got := getMedia(s, id, bob); code != http.StatusOK || !bytes.Equal(got, img) {
		t.Fatalf("member after sending: got %d, want 200 with the file", code)
	}
	foreign, foreignBody := getMedia(s, id, mallory)
	invented, inventedBody := getMedia(s, bson.NewObjectID().Hex(), mallory)
	if foreign != http.StatusNotFound || invented != http.StatusNotFound || !bytes.Equal(foreignBody, inventedBody) {
		t.Fatalf("outsider got %d %s, invented id %d %s: want identical 404s", foreign, foreignBody, invented, inventedBody)
	}
}

func TestAvatarIsReadableByEveryone(t *testing.T) {
	db := testDB(t)
	s := &server{media: newMediaStore(db)}

	m, err := s.media.Save(context.Background(), person("alice").UserID, mediaKindAvatar, bytes.NewReader(testPNG(t, 4, 4)))
	if err != nil {
		t.Fatalf("saving avatar: %v", err)
	}
	if code, _ := getMedia(s, m.ID.Hex(), person("mallory")); code != http.StatusOK {
		t.Fatalf("stranger reading an avatar: got %d, want 200", code)
	}
}

func setAvatar(s *server, body []byte, sess Session) (int, []byte) {
	r := httptest.NewRequest(http.MethodPost, "/auth/me/avatar", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleSetAvatar(w, r, sess)
	return w.Code, w.Body.Bytes()
}

func TestReplacedAvatarIsDeleted(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	s := &server{users: newUserStore(db), media: newMediaStore(db)}

	u := &User{Username: "alice", DisplayName: "Alice"}
	if err := s.users.Create(ctx, u); err != nil {
		t.Fatalf("creating user: %v", err)
	}
	alice := Session{UserID: u.ID, Username: u.Username}

	avatarOf := func() *bson.ObjectID {
		t.Helper()
		got, err := s.users.GetByUsername(ctx, "alice")
		if err != nil {
			t.Fatalf("loading user: %v", err)
		}
		return got.AvatarID
	}
	gone := func(id bson.ObjectID) {
		t.Helper()
		m, err := s.media.col.CountDocuments(ctx, bson.M{"_id": id})
		if err != nil {
			t.Fatalf("counting media: %v", err)
		}
		files, err := db.Collection("fs.files").CountDocuments(ctx, bson.M{})
		if err != nil {
			t.Fatalf("counting files: %v", err)
		}
		if m != 0 || files != 1 {
			t.Fatalf("after dropping %s: %d media documents and %d files, want 0 and 1", id.Hex(), m, files)
		}
	}

	var first, second map[string]string
	for _, into := range []*map[string]string{&first, &second} {
		code, body := setAvatar(s, testPNG(t, 8, 8), alice)
		if code != http.StatusOK {
			t.Fatalf("setting avatar: got %d %s, want 200", code, body)
		}
		if err := json.Unmarshal(body, into); err != nil {
			t.Fatalf("decoding answer: %v", err)
		}
	}

	firstID, _ := bson.ObjectIDFromHex(first["avatar_id"])
	secondID, _ := bson.ObjectIDFromHex(second["avatar_id"])
	if got := avatarOf(); got == nil || *got != secondID {
		t.Fatalf("user avatar is %v, want the second upload %s", got, secondID.Hex())
	}
	gone(firstID)

	if code, body := setAvatar(s, append(testPNG(t, 1, 1), make([]byte, avatarMaxBytes)...), alice); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized avatar: got %d %s, want 413", code, body)
	}
	if got := avatarOf(); got == nil || *got != secondID {
		t.Fatalf("a refused upload changed the avatar to %v", got)
	}

	w := httptest.NewRecorder()
	s.handleDeleteAvatar(w, httptest.NewRequest(http.MethodDelete, "/auth/me/avatar", nil), alice)
	if w.Code != http.StatusOK {
		t.Fatalf("removing avatar: got %d %s, want 200", w.Code, w.Body)
	}
	if got := avatarOf(); got != nil {
		t.Fatalf("avatar after removal is %s, want none", got.Hex())
	}
	if n, _ := s.media.col.CountDocuments(ctx, bson.M{}); n != 0 {
		t.Fatalf("%d media documents left after removal, want 0", n)
	}
}

func postJSON(t *testing.T, h func(http.ResponseWriter, *http.Request, Session), pathID string, body any, sess Session) (int, []byte) {
	t.Helper()

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encoding request: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
	if pathID != "" {
		r.SetPathValue("id", pathID)
	}
	w := httptest.NewRecorder()
	h(w, r, sess)
	return w.Code, w.Body.Bytes()
}

func uploaded(t *testing.T, s *server, sess Session) string {
	t.Helper()

	m, err := s.media.Save(context.Background(), sess.UserID, mediaKindAttachment, bytes.NewReader(testPNG(t, 5, 3)))
	if err != nil {
		t.Fatalf("saving media: %v", err)
	}
	return m.ID.Hex()
}

func TestPicturesGoOnlyWhereTheirOwnerSendsThem(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, messages := newMessageTestStores(t, db)
	s := &server{channels: channels, messages: messages, media: newMediaStore(db), hub: NewHub()}

	alice, bob, mallory := person("alice"), person("bob"), person("mallory")
	room := createChannel(t, channels, "room", alice)
	notes := createChannel(t, channels, "notes", alice)
	malloryRoom := createChannel(t, channels, "mallory-room", mallory)
	if err := channels.AddMember(ctx, room.ID, bob); err != nil {
		t.Fatalf("adding bob: %v", err)
	}

	pic := uploaded(t, s, alice)
	send := func(ch Channel, text string, ids []string, clientID string, as Session) (int, []byte) {
		return postJSON(t, s.handleSendMessage, "", map[string]any{
			"channel_id": ch.ID.Hex(), "text": text, "attachments": ids, "client_msg_id": clientID,
		}, as)
	}

	if code, body := send(room, "", nil, "", alice); code != http.StatusBadRequest {
		t.Fatalf("empty message without pictures: got %d %s, want 400", code, body)
	}

	code, body := send(room, "", []string{pic}, "c1", alice)
	if code != http.StatusCreated {
		t.Fatalf("picture without text: got %d %s, want 201", code, body)
	}
	var msg Message
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("decoding message: %v", err)
	}
	if len(msg.Attachments) != 1 || msg.Attachments[0].ID.Hex() != pic || msg.Attachments[0].Width != 5 {
		t.Fatalf("attachments are %+v, want the 5x3 picture %s", msg.Attachments, pic)
	}
	if code, _ := getMedia(s, pic, bob); code != http.StatusOK {
		t.Fatalf("member reading a sent picture: got %d, want 200", code)
	}

	if code, _ := send(room, "", []string{pic}, "c1", alice); code != http.StatusOK {
		t.Fatalf("retried send: got %d, want 200 with the stored message", code)
	}

	for name, try := range map[string]func() (int, []byte){
		"into another channel": func() (int, []byte) { return send(notes, "", []string{pic}, "", alice) },
		"not yet sent, by someone else": func() (int, []byte) {
			return send(malloryRoom, "", []string{uploaded(t, s, alice)}, "", mallory)
		},
		"that does not exist": func() (int, []byte) { return send(room, "", []string{bson.NewObjectID().Hex()}, "", alice) },
		"that is someone's avatar": func() (int, []byte) {
			m, err := s.media.Save(ctx, alice.UserID, mediaKindAvatar, bytes.NewReader(testPNG(t, 2, 2)))
			if err != nil {
				t.Fatalf("saving avatar: %v", err)
			}
			return send(room, "", []string{m.ID.Hex()}, "", alice)
		},
	} {
		if code, body := try(); code != http.StatusNotFound {
			t.Fatalf("attaching a picture %s: got %d %s, want 404", name, code, body)
		}
	}
	if left, _ := messages.List(ctx, malloryRoom.ID, bson.ObjectID{}, 0); len(left) != 0 {
		t.Fatalf("mallory's room holds %d messages after refused sends, want 0", len(left))
	}

	many := make([]string, messageMaxAttachments+1)
	for i := range many {
		many[i] = bson.NewObjectID().Hex()
	}
	for name, ids := range map[string][]string{
		"too many":  many,
		"malformed": {"nope"},
		"repeated":  {pic, pic},
	} {
		if code, body := send(room, "x", ids, "", alice); code != http.StatusBadRequest {
			t.Fatalf("%s attachments: got %d %s, want 400", name, code, body)
		}
	}
}

func TestForwardCopiesThePictureRecordNotTheBytes(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, messages := newMessageTestStores(t, db)
	s := &server{channels: channels, messages: messages, media: newMediaStore(db), hub: NewHub()}

	alice, bob, carol := person("alice"), person("bob"), person("carol")
	room := createChannel(t, channels, "room", alice)
	other := createChannel(t, channels, "other", bob)
	if err := channels.AddMember(ctx, room.ID, bob); err != nil {
		t.Fatalf("adding bob: %v", err)
	}
	if err := channels.AddMember(ctx, other.ID, carol); err != nil {
		t.Fatalf("adding carol: %v", err)
	}

	pic := uploaded(t, s, alice)
	code, body := postJSON(t, s.handleSendMessage, "", map[string]any{
		"channel_id": room.ID.Hex(), "text": "look", "attachments": []string{pic},
	}, alice)
	if code != http.StatusCreated {
		t.Fatalf("sending: got %d %s", code, body)
	}
	var orig Message
	if err := json.Unmarshal(body, &orig); err != nil {
		t.Fatalf("decoding message: %v", err)
	}

	forward := func() (int, []byte) {
		return postJSON(t, s.handleForwardMessage, orig.ID.Hex(),
			map[string]string{"channel_id": other.ID.Hex(), "client_msg_id": "f1"}, bob)
	}
	code, body = forward()
	if code != http.StatusCreated {
		t.Fatalf("forwarding: got %d %s, want 201", code, body)
	}
	var copied Message
	if err := json.Unmarshal(body, &copied); err != nil {
		t.Fatalf("decoding forward: %v", err)
	}
	if len(copied.Attachments) != 1 || copied.Attachments[0].ID.Hex() == pic || copied.Attachments[0].Width != 5 {
		t.Fatalf("forwarded attachments are %+v, want one 5x3 picture under a new id", copied.Attachments)
	}
	copyID := copied.Attachments[0].ID.Hex()

	if code, _ := getMedia(s, copyID, carol); code != http.StatusOK {
		t.Fatalf("carol reading the forwarded picture: got %d, want 200", code)
	}
	if code, _ := getMedia(s, pic, carol); code != http.StatusNotFound {
		t.Fatalf("carol reading the original picture: got %d, want 404", code)
	}

	if code, _ := forward(); code != http.StatusOK {
		t.Fatalf("retried forward: got %d, want 200", code)
	}
	records, err := s.media.col.CountDocuments(ctx, bson.M{})
	if err != nil {
		t.Fatalf("counting media: %v", err)
	}
	files, err := db.Collection("fs.files").CountDocuments(ctx, bson.M{})
	if err != nil {
		t.Fatalf("counting files: %v", err)
	}
	if records != 2 || files != 1 {
		t.Fatalf("after a forward and its retry: %d media records and %d files, want 2 and 1", records, files)
	}
}
