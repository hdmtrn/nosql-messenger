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
