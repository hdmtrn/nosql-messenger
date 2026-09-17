package main

import (
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const mediaTransferTimeout = 2 * time.Minute

func extendDeadlines(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	deadline := time.Now().Add(mediaTransferTimeout)
	err := errors.Join(rc.SetReadDeadline(deadline), rc.SetWriteDeadline(deadline))
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		log.Printf("extending deadlines: %v", err)
	}
}

func (s *server) saveUpload(w http.ResponseWriter, r *http.Request, owner bson.ObjectID, kind string, channel *bson.ObjectID, limit int64) (Media, bool) {
	extendDeadlines(w)
	body := http.MaxBytesReader(w, r.Body, limit)

	m, err := s.media.Save(r.Context(), owner, kind, channel, body)
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "file is too large")
		return Media{}, false
	case errors.Is(err, errImageTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "image is larger than 10000 px a side or 40 megapixels")
		return Media{}, false
	case errors.Is(err, errUnsupportedMedia):
		writeError(w, http.StatusUnsupportedMediaType, "only JPEG, PNG and GIF images are accepted")
		return Media{}, false
	case err != nil:
		log.Printf("saving media: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return Media{}, false
	}
	return m, true
}

func (s *server) handleUploadMedia(w http.ResponseWriter, r *http.Request, sess Session) {
	m, ok := s.saveUpload(w, r, sess.UserID, mediaKindAttachment, nil, mediaMaxBytes)
	if !ok {
		return
	}
	writeJSON(w, http.StatusCreated, m.Attachment())
}

func (s *server) canReadMedia(r *http.Request, m Media, sess Session) (bool, error) {
	switch {
	case m.Kind == mediaKindAvatar && m.ChannelID == nil:
		return true, nil
	case m.ChannelID == nil:
		return m.OwnerID == sess.UserID, nil
	default:
		return s.channels.IsMember(r.Context(), *m.ChannelID, sess.UserID)
	}
}

func (s *server) handleGetMedia(w http.ResponseWriter, r *http.Request, sess Session) {
	id, err := bson.ObjectIDFromHex(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed media id")
		return
	}

	m, err := s.media.ByID(r.Context(), id)
	if errors.Is(err, errMediaNotFound) {
		writeError(w, http.StatusNotFound, "media not found")
		return
	}
	if err != nil {
		log.Printf("loading media: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	allowed, err := s.canReadMedia(r, m, sess)
	if err != nil {
		log.Printf("checking media access: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !allowed {
		writeError(w, http.StatusNotFound, "media not found")
		return
	}

	ds, err := s.media.Open(r.Context(), m)
	if err != nil {
		log.Printf("opening media %s: %v", m.ID.Hex(), err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer ds.Close()

	extendDeadlines(w)
	h := w.Header()
	h.Set("Content-Type", m.ContentType)
	h.Set("Content-Length", strconv.FormatInt(ds.GetFile().Length, 10))
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	if _, err := io.Copy(w, ds); err != nil {
		log.Printf("streaming media %s: %v", m.ID.Hex(), err)
	}
}
