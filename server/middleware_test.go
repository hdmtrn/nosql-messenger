package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBrowserDefences(t *testing.T) {
	reached := false
	h := withBrowserDefences(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))
	send := func(method, site string) *httptest.ResponseRecorder {
		reached = false
		r := httptest.NewRequest(method, "/auth/login", nil)
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// A login posted by a page on another site, as a hidden form would.
	if w := send(http.MethodPost, "cross-site"); w.Code != http.StatusForbidden || reached {
		t.Fatalf("cross-site POST: got %d, handler reached %v; want 403 and not reached", w.Code, reached)
	}
	if w := send(http.MethodPost, "same-origin"); w.Code != http.StatusOK || !reached {
		t.Fatalf("same-origin POST: got %d, handler reached %v; want it through", w.Code, reached)
	}
	// No header at all is a client that is not a browser, such as the tests.
	if w := send(http.MethodPost, ""); !reached {
		t.Fatalf("POST without Sec-Fetch-Site: got %d, want it through", w.Code)
	}
	// Opening a link from another site is a GET and changes nothing.
	w := send(http.MethodGet, "cross-site")
	if !reached {
		t.Fatalf("cross-site GET: got %d, want it through", w.Code)
	}

	for _, name := range []string{"Strict-Transport-Security", "Content-Security-Policy", "Referrer-Policy", "X-Content-Type-Options"} {
		if w.Header().Get(name) == "" {
			t.Errorf("%s is not set", name)
		}
	}
}

func TestLoggedPathDropsInviteCodes(t *testing.T) {
	for path, want := range map[string]string{
		"/invite/Xy7-secret":  "/invite/{code}",
		"/invites/Xy7-secret": "/invites/{code}",
		"/channels/42/invite": "/channels/42/invite",
		"/messages":           "/messages",
	} {
		if got := loggedPath(path); got != want {
			t.Errorf("loggedPath(%q) = %q, want %q", path, got, want)
		}
	}
}
