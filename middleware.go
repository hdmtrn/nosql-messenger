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

func requireAuth(sessions *sessionStore, next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := sessions.ByToken(r.Context(), tokenFromRequest(r))
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
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

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

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("underlying ResponseWriter is not a http.Hijacker")
	}
	r.status = http.StatusSwitchingProtocols
	return h.Hijack()
}
