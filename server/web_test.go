package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// After a deploy a tab can ask for the bundle its page named, which the new
// image no longer has. Answering that with the page let the browser try to run
// HTML as a script and stay blank; it is a 404 now, and the page itself is
// revalidated on every load, so the next load names the new bundle.
func TestTheBundleIsCachedAndThePageIsNot(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{
		"index.html":           "<!doctype html><title>Messenger</title>",
		"assets/index-new1.js": "console.log('new')",
		"favicon.svg":          "<svg/>",
	} {
		full := filepath.Join(root, webRoot, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)

	for _, tc := range []struct {
		path        string
		code        int
		cache, body string
	}{
		{"/assets/index-new1.js", http.StatusOK, "public, max-age=31536000, immutable", "console.log"},
		{"/assets/index-old1.js", http.StatusNotFound, "", "404 page not found"},
		{"/favicon.svg", http.StatusOK, "no-cache", "<svg/>"},
		{"/", http.StatusOK, "no-cache", "<title>Messenger"},
		{"/invite/abc123", http.StatusOK, "no-cache", "<title>Messenger"},
		{"/c/6ac5/requests", http.StatusOK, "no-cache", "<title>Messenger"},
	} {
		w := httptest.NewRecorder()
		serveWeb(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if w.Code != tc.code {
			t.Errorf("%s: %d, want %d", tc.path, w.Code, tc.code)
		}
		if got := w.Header().Get("Cache-Control"); got != tc.cache {
			t.Errorf("%s: Cache-Control %q, want %q", tc.path, got, tc.cache)
		}
		if !strings.Contains(w.Body.String(), tc.body) {
			t.Errorf("%s: body %q, want it to contain %q", tc.path, w.Body.String(), tc.body)
		}
	}
}
