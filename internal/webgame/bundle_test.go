package webgame

import (
	"archive/zip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func zipFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "game.zip")
	out, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(out)
	for name, content := range files {
		file, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestWebBundleServesUnchangedAssetsWithIsolation(t *testing.T) {
	b, err := Open(zipFixture(t, map[string]string{"index.html": "<canvas></canvas>", "index.wasm": "original wasm"}))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	request := httptest.NewRequest(http.MethodGet, "/index.wasm", nil)
	w := httptest.NewRecorder()
	b.ServeFile(w, request, "index.wasm")
	if w.Code != http.StatusOK || w.Body.String() != "original wasm" ||
		w.Header().Get("Content-Type") != "application/wasm" ||
		w.Header().Get("Cross-Origin-Opener-Policy") != "same-origin" ||
		w.Header().Get("Cross-Origin-Embedder-Policy") != "require-corp" {
		t.Fatalf("unexpected WASM response: %d, %v, %q", w.Code, w.Header(), w.Body.String())
	}
	request.Header.Set("Service-Worker", "script")
	w = httptest.NewRecorder()
	b.ServeFile(w, request, "index.html")
	if w.Code != http.StatusNotFound {
		t.Fatalf("service worker request returned %d", w.Code)
	}
}

func TestWebBundleRejectsPathsOutsideExport(t *testing.T) {
	b, err := Open(zipFixture(t, map[string]string{"index.html": "ok", "../private.js": "traversal"}))
	if err == nil {
		b.Close()
		t.Fatal("opened ZIP with traversal entry")
	}
}
