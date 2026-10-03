package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// textBody is a valid JSON body of exactly n bytes: a message whose text
// fills whatever the braces leave.
func textBody(n int) string {
	const frame = `{"text":""}`
	return `{"text":"` + strings.Repeat("a", n-len(frame)) + `"}`
}

type textRequest struct {
	Text string `json:"text"`
}

func TestDecodeJSONBySize(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want int // 0 when the body decodes
	}{
		{"small", `{"text":"hi"}`, 0},
		{"exactly the cap", textBody(jsonBodyMax), 0},
		{"one byte over", textBody(jsonBodyMax + 1), http.StatusRequestEntityTooLarge},
		{"a megabyte", textBody(1 << 20), http.StatusRequestEntityTooLarge},
		{"malformed", `{"text":`, http.StatusBadRequest},
		{"empty", ``, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			var v textRequest
			ok := decodeJSON(rec, req, &v)

			if tc.want == 0 {
				if !ok {
					t.Fatalf("refused with %d: %s", rec.Code, rec.Body)
				}
				if want := len(tc.body) - len(`{"text":""}`); len(v.Text) != want {
					t.Fatalf("decoded %d characters of text, want %d", len(v.Text), want)
				}
				return
			}
			if ok || rec.Code != tc.want {
				t.Fatalf("decoded=%v with %d, want %d", ok, rec.Code, tc.want)
			}
		})
	}
}

// streamedBody is a client that keeps sending and sends no Content-Length,
// which is what chunked transfer looks like to a handler. It counts what was
// taken from it, and ends after size bytes only so that a broken cap fails the
// test instead of exhausting its memory.
type streamedBody struct{ size, read int }

func (b *streamedBody) Read(p []byte) (int, error) {
	const prefix = `{"text":"`
	if b.read >= b.size {
		return 0, io.EOF
	}
	p = p[:min(len(p), b.size-b.read)]
	for i := range p {
		if b.read < len(prefix) {
			p[i] = prefix[b.read]
		} else {
			p[i] = 'a'
		}
		b.read++
	}
	return len(p), nil
}

// With no header to check the cap has to hold while reading, and reading has
// to stop at it: whatever the client keeps sending stays in the socket.
func TestDecodeJSONStopsReadingAStreamedBody(t *testing.T) {
	body := &streamedBody{size: 16 << 20}
	req := httptest.NewRequest(http.MethodPost, "/", body)
	if req.ContentLength != -1 {
		t.Fatalf("Content-Length %d, want none", req.ContentLength)
	}

	rec := httptest.NewRecorder()
	var v textRequest
	if decodeJSON(rec, req, &v) || rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("a streamed body got %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
	if body.read > jsonBodyMax+1 {
		t.Fatalf("read %d bytes of a streamed body, the cap is %d", body.read, jsonBodyMax)
	}
}

// Every handler that takes a JSON body refuses one over the cap. The body is
// also left unterminated, so a handler that decoded it without the cap would
// answer 400 rather than go on to the stores, which these tests leave out.
func TestJSONHandlersRefuseABodyOverTheCap(t *testing.T) {
	s := &server{}
	a := &auth{}
	var sess Session
	authed := func(h authedHandler) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { h(w, r, sess) }
	}

	body := `{"text":"` + strings.Repeat("a", jsonBodyMax)
	for _, tc := range []struct {
		name string
		h    http.HandlerFunc
	}{
		{"register", a.handleRegister},
		{"login", a.handleLogin},
		{"create channel", authed(s.handleCreateChannel)},
		{"open direct", authed(s.handleOpenDirect)},
		{"friend request", authed(s.handleSendFriendRequest)},
		{"send message", authed(s.handleSendMessage)},
		{"forward message", authed(s.handleForwardMessage)},
		{"update profile", authed(s.handleUpdateProfile)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.h(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("got %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
			}
		})
	}
}

// The table above knows the handlers that exist today. A new one that decodes
// r.Body itself would have no cap, and no behavioural test would know it is
// there, so this one reads the sources. It catches the usual copy of the old
// line, not every way of reading a body.
func TestNoHandlerDecodesTheBodyItself(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), "json.NewDecoder(r.Body)") {
			t.Errorf("%s decodes r.Body directly; use decodeJSON, which caps the body", f)
		}
	}
}
