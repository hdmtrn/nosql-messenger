package main

import (
	"bufio"
	"errors"
	"log"
	"net"
	"net/http"
	"time"
)

type authedHandler func(w http.ResponseWriter, r *http.Request, sess Session)

func (s *server) requireAuth(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.sessions.ByToken(r.Context(), tokenFromRequest(r))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		next(w, r, sess)
	}
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := newResponseRecorder(w)

		next.ServeHTTP(rec, r)

		note := ""
		if r.Pattern == "" {
			note = "  (no route)"
		} else if r.Pattern != r.Method+" "+r.URL.Path {
			note = "  pattern=" + r.Pattern
		}
		log.Printf("%s %s %d %v%s",
			r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Microsecond), note)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status   int
	hijacker http.Hijacker
	flusher  http.Flusher
}

func newResponseRecorder(w http.ResponseWriter) *responseRecorder {
	hijacker, _ := w.(http.Hijacker)
	flusher, _ := w.(http.Flusher)
	return &responseRecorder{
		ResponseWriter: w,
		status:         http.StatusOK,
		hijacker:       hijacker,
		flusher:        flusher,
	}
}

func (r *responseRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if r.hijacker == nil {
		return nil, nil, errors.New("wrapped ResponseWriter does not support Hijack")
	}
	r.status = http.StatusSwitchingProtocols
	return r.hijacker.Hijack()
}

func (r *responseRecorder) Flush() {
	if r.flusher != nil {
		r.flusher.Flush()
	}
}
