package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

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

	m, err := s.media.Save(context.Background(), person("alice").UserID, mediaKindAvatar, nil, bytes.NewReader(testPNG(t, 4, 4)))
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

	m, err := s.media.Save(context.Background(), sess.UserID, mediaKindAttachment, nil, bytes.NewReader(testPNG(t, 5, 3)))
	if err != nil {
		t.Fatalf("saving media: %v", err)
	}
	return m.ID.Hex()
}

func channelAvatarCall(s *server, method, channelID string, body []byte, sess Session) (int, []byte) {
	r := httptest.NewRequest(method, "/channels/"+channelID+"/avatar", bytes.NewReader(body))
	r.SetPathValue("id", channelID)
	w := httptest.NewRecorder()
	if method == http.MethodDelete {
		s.handleDeleteChannelAvatar(w, r, sess)
	} else {
		s.handleSetChannelAvatar(w, r, sess)
	}
	return w.Code, w.Body.Bytes()
}

func TestOnlyTheOwnerChangesTheChannelAvatar(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, _ := newMessageTestStores(t, db)
	s := &server{channels: channels, media: newMediaStore(db)}

	alice, bob, mallory := person("alice"), person("bob"), person("mallory")
	room := createChannel(t, channels, "room", alice)
	if err := channels.AddMember(ctx, room.ID, bob); err != nil {
		t.Fatalf("adding bob: %v", err)
	}
	dm, err := channels.Direct(ctx, alice, &User{ID: bob.UserID, Username: bob.Username})
	if err != nil {
		t.Fatalf("opening direct channel: %v", err)
	}
	id := room.ID.Hex()

	for name, try := range map[string]struct {
		sess    Session
		channel string
		want    int
	}{
		"member":         {bob, id, http.StatusForbidden},
		"outsider":       {mallory, id, http.StatusNotFound},
		"direct channel": {alice, dm.ID.Hex(), http.StatusForbidden},
	} {
		for _, method := range []string{http.MethodPost, http.MethodDelete} {
			if code, body := channelAvatarCall(s, method, try.channel, testPNG(t, 2, 2), try.sess); code != try.want {
				t.Fatalf("%s by %s: got %d %s, want %d", method, name, code, body, try.want)
			}
		}
	}
	if n, _ := s.media.col.CountDocuments(ctx, bson.M{}); n != 0 {
		t.Fatalf("%d media documents stored by refused uploads, want 0", n)
	}

	code, body := channelAvatarCall(s, http.MethodPost, id, testPNG(t, 8, 8), alice)
	if code != http.StatusOK {
		t.Fatalf("owner setting avatar: got %d %s, want 200", code, body)
	}
	var got map[string]string
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding answer: %v", err)
	}
	stored, err := channels.ByID(ctx, room.ID)
	if err != nil || stored.AvatarID == nil || stored.AvatarID.Hex() != got["avatar_id"] {
		t.Fatalf("channel avatar is %v (err %v), want %s", stored.AvatarID, err, got["avatar_id"])
	}

	// A private channel's picture stays inside it, unlike a person's.
	if code, _ := getMedia(s, got["avatar_id"], bob); code != http.StatusOK {
		t.Fatalf("member reading the channel avatar: got %d, want 200", code)
	}
	if code, _ := getMedia(s, got["avatar_id"], mallory); code != http.StatusNotFound {
		t.Fatalf("outsider reading the channel avatar: got %d, want 404", code)
	}

	if code, body := channelAvatarCall(s, http.MethodDelete, id, nil, alice); code != http.StatusOK {
		t.Fatalf("owner removing avatar: got %d %s, want 200", code, body)
	}
	if stored, _ := channels.ByID(ctx, room.ID); stored.AvatarID != nil {
		t.Fatalf("channel avatar after removal is %s, want none", stored.AvatarID.Hex())
	}
	if n, _ := s.media.col.CountDocuments(ctx, bson.M{}); n != 0 {
		t.Fatalf("%d media documents left after removal, want 0", n)
	}
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
			m, err := s.media.Save(ctx, alice.UserID, mediaKindAvatar, nil, bytes.NewReader(testPNG(t, 2, 2)))
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

func TestConcurrentUploadsKeepTheirBytes(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	media := newMediaStore(db)

	pictures := make([][]byte, 8)
	for i := range pictures {
		img := image.NewRGBA(image.Rect(0, 0, 600, 600))
		if _, err := rand.Read(img.Pix); err != nil {
			t.Fatalf("filling picture: %v", err)
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			t.Fatalf("encoding picture: %v", err)
		}
		pictures[i] = buf.Bytes()
	}

	saved := make([]Media, len(pictures))
	errs := make([]error, len(pictures))
	var wg sync.WaitGroup
	for i, p := range pictures {
		wg.Add(1)
		go func() {
			defer wg.Done()
			saved[i], errs[i] = media.Save(ctx, person("alice").UserID, mediaKindAttachment, nil, bytes.NewReader(p))
		}()
	}
	wg.Wait()

	for i := range saved {
		if errs[i] != nil {
			t.Fatalf("saving picture %d: %v", i, errs[i])
		}
	}

	// Reads share one bucket, so they run at once too, twice per picture.
	got := make([][]byte, 2*len(saved))
	readErrs := make([]error, len(got))
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ds, err := media.Open(ctx, saved[i/2])
			if err != nil {
				readErrs[i] = err
				return
			}
			defer ds.Close()
			got[i], readErrs[i] = io.ReadAll(ds)
		}()
	}
	wg.Wait()

	for i := range got {
		if readErrs[i] != nil {
			t.Fatalf("reading picture %d: %v", i/2, readErrs[i])
		}
		if !bytes.Equal(got[i], pictures[i/2]) {
			t.Fatalf("picture %d came back different: %d bytes, sent %d", i/2, len(got[i]), len(pictures[i/2]))
		}
	}
}

// pngHeader is the start of a PNG claiming the given size: the signature and the
// IHDR chunk are all DecodeConfig reads, so no pixels need to exist.
func pngHeader(w, h uint32) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 6 // 8-bit RGBA

	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	_ = binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
	chunk := append([]byte("IHDR"), ihdr...)
	b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return b.Bytes()
}

func TestHugeImagesAreRefusedByTheirHeader(t *testing.T) {
	db := testDB(t)
	s := &server{media: newMediaStore(db)}
	alice := person("alice")

	for name, size := range map[string][2]uint32{
		"a side over the limit": {mediaMaxSide + 1, 1},
		"too many pixels":       {6400, 6400},
	} {
		code, body := uploadMedia(s, pngHeader(size[0], size[1]), alice)
		if code != http.StatusRequestEntityTooLarge {
			t.Fatalf("%s (%dx%d): got %d %s, want 413", name, size[0], size[1], code, body)
		}
	}
	if code, body := uploadMedia(s, pngHeader(mediaMaxSide, mediaMaxPixels/mediaMaxSide), alice); code != http.StatusCreated {
		t.Fatalf("an image exactly at the limit: got %d %s, want 201", code, body)
	}

	files, err := db.Collection("fs.files").CountDocuments(context.Background(), bson.M{})
	if err != nil {
		t.Fatalf("counting files: %v", err)
	}
	if files != 1 {
		t.Fatalf("%d files stored, want only the one at the limit", files)
	}
}

func TestOnlyUnsentPicturesExpire(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, _ := newMessageTestStores(t, db)
	media := newMediaStore(db)
	if err := media.ensureIndexes(ctx); err != nil {
		t.Fatalf("media indexes: %v", err)
	}

	alice := person("alice")
	room := createChannel(t, channels, "room", alice)
	save := func(kind string) Media {
		t.Helper()
		m, err := media.Save(ctx, alice.UserID, kind, nil, bytes.NewReader(testPNG(t, 2, 2)))
		if err != nil {
			t.Fatalf("saving %s: %v", kind, err)
		}
		return m
	}

	unsent, sent, avatar := save(mediaKindAttachment), save(mediaKindAttachment), save(mediaKindAvatar)
	if unsent.ExpiresAt == nil || avatar.ExpiresAt != nil {
		t.Fatalf("expiry: attachment %v, avatar %v; want a time and none", unsent.ExpiresAt, avatar.ExpiresAt)
	}
	if _, err := media.Attach(ctx, []bson.ObjectID{sent.ID}, alice.UserID, room.ID); err != nil {
		t.Fatalf("attaching: %v", err)
	}
	copies, err := media.CopyTo(ctx, []Attachment{sent.Attachment()}, alice.UserID, room.ID)
	if err != nil {
		t.Fatalf("copying: %v", err)
	}

	if n, err := media.DeleteExpired(ctx, time.Now()); err != nil || n != 0 {
		t.Fatalf("sweeping before the deadline: deleted %d, err %v; want 0", n, err)
	}
	n, err := media.DeleteExpired(ctx, time.Now().Add(mediaUnsentTTL+time.Minute))
	if err != nil || n != 1 {
		t.Fatalf("sweeping after the deadline: deleted %d, err %v; want 1", n, err)
	}

	if _, err := media.ByID(ctx, unsent.ID); !errors.Is(err, errMediaNotFound) {
		t.Fatalf("unsent picture after the sweep: %v, want not found", err)
	}
	for name, id := range map[string]bson.ObjectID{"sent": sent.ID, "forwarded": copies[0].ID, "avatar": avatar.ID} {
		if _, err := media.ByID(ctx, id); err != nil {
			t.Fatalf("%s picture after the sweep: %v", name, err)
		}
	}
	files, err := db.Collection("fs.files").CountDocuments(ctx, bson.M{"_id": unsent.FileID})
	if err != nil {
		t.Fatalf("counting files: %v", err)
	}
	if files != 0 {
		t.Fatal("the bytes of the unsent picture are still stored")
	}
}

func TestDiscardKeepsFilesThatForwardedCopiesStillUse(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	channels, messages := newMessageTestStores(t, db)
	invites, media := newInviteStore(db), newMediaStore(db)
	if err := media.ensureIndexes(ctx); err != nil {
		t.Fatalf("media indexes: %v", err)
	}

	alice := person("alice")
	doomed := createChannel(t, channels, "doomed", alice)
	kept := createChannel(t, channels, "kept", alice)
	save := func(kind string, channel *bson.ObjectID) Media {
		t.Helper()
		m, err := media.Save(ctx, alice.UserID, kind, channel, bytes.NewReader(testPNG(t, 2, 2)))
		if err != nil {
			t.Fatalf("saving %s: %v", kind, err)
		}
		return m
	}

	forwarded, alone := save(mediaKindAttachment, nil), save(mediaKindAttachment, nil)
	if _, err := media.Attach(ctx, []bson.ObjectID{forwarded.ID, alone.ID}, alice.UserID, doomed.ID); err != nil {
		t.Fatalf("attaching: %v", err)
	}
	copies, err := media.CopyTo(ctx, []Attachment{forwarded.Attachment()}, alice.UserID, kept.ID)
	if err != nil {
		t.Fatalf("forwarding: %v", err)
	}
	avatar := save(mediaKindAvatar, &doomed.ID)

	if err := channels.Leave(ctx, messages, invites, media, doomed.ID, alice.UserID); err != nil {
		t.Fatalf("leaving: %v", err)
	}

	fileExists := func(id bson.ObjectID) bool {
		t.Helper()
		n, err := db.Collection("fs.files").CountDocuments(ctx, bson.M{"_id": id})
		if err != nil {
			t.Fatalf("counting files: %v", err)
		}
		return n > 0
	}
	if n, _ := media.col.CountDocuments(ctx, bson.M{"channel_id": doomed.ID}); n != 0 {
		t.Fatalf("%d media records outlived their channel", n)
	}
	if fileExists(alone.FileID) || fileExists(avatar.FileID) {
		t.Fatalf("files used only by the discarded channel are still stored")
	}
	if !fileExists(forwarded.FileID) {
		t.Fatalf("the file behind a forwarded copy was deleted with the original's channel")
	}

	s := &server{channels: channels, media: media}
	if code, _ := getMedia(s, copies[0].ID.Hex(), alice); code != http.StatusOK {
		t.Fatalf("reading the forwarded copy: got %d, want 200", code)
	}
}
