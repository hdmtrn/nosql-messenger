package main

import (
	"bufio"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
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

// contentSecurityPolicy lets the page load only its own scripts, styles and
// connections. blob: is for the previews of pictures picked to send; inline
// styles are for the <style> of the privacy and terms pages. 'self' covers the
// socket too: CSP Level 3 matches ws: and wss: on the page's own host.
const contentSecurityPolicy = "default-src 'self'; img-src 'self' blob:; " +
	"style-src 'self' 'unsafe-inline'; object-src 'none'; base-uri 'none'; " +
	"form-action 'self'; frame-ancestors 'none'"

// withBrowserDefences sets the headers that tell a browser what the site may do,
// and refuses a state-changing request a browser sends from another site.
// SameSite keeps the session cookie off such a request, but not the cookie it
// sets: a foreign form could log a visitor in to an account of its choosing.
func withBrowserDefences(next http.Handler) http.Handler {
	csrf := http.NewCrossOriginProtection()
	// In the same JSON as every other refusal, which the client reads.
	csrf.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusForbidden, "cross-site request refused")
	}))
	guarded := csrf.Handler(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// Browsers ignore it over plain http, so it is harmless locally. No
		// includeSubDomains: other hosts of the domain may have no TLS.
		h.Set("Strict-Transport-Security", "max-age=31536000")
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		guarded.ServeHTTP(w, r)
	})
}

// secretPathPrefixes start the paths that end in an invite code, which is all
// it takes to join the channel: the page an invite link opens, and the call
// that follows it.
var secretPathPrefixes = []string{"/invite/", "/invites/"}

// loggedPath is the path as the log may keep it, with an invite code dropped.
func loggedPath(path string) string {
	for _, prefix := range secretPathPrefixes {
		if strings.HasPrefix(path, prefix) {
			return prefix + "{code}"
		}
	}
	return path
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
			r.Method, loggedPath(r.URL.Path), rec.status, time.Since(start).Round(time.Microsecond), note)
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
