package webgame

import (
	"archive/zip"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// Bundle is an immutable Godot export. Files are served directly from the ZIP.
type Bundle struct {
	archive *zip.ReadCloser
	files   map[string]*zip.File
}

func Open(filename string) (*Bundle, error) {
	archive, err := zip.OpenReader(filename)
	if err != nil {
		if archive != nil {
			_ = archive.Close()
		}
		return nil, err
	}
	b := &Bundle{archive: archive, files: make(map[string]*zip.File)}
	err = b.index()
	if err != nil {
		_ = archive.Close()
		return nil, err
	}
	return b, nil
}

func (b *Bundle) index() error {
	if len(b.archive.File) > 512 {
		return errors.New("too many files in the Web export")
	}
	var total uint64
	for _, file := range b.archive.File {
		name := strings.TrimSuffix(file.Name, "/")
		if name == "." || !fs.ValidPath(name) || strings.ContainsAny(name, "\\%") {
			return errors.New("invalid path in the Web export")
		}
		if file.FileInfo().IsDir() {
			continue
		}
		if !file.Mode().IsRegular() || b.files[name] != nil || file.UncompressedSize64 > 128<<20 {
			return errors.New("invalid file in the Web export")
		}
		total += file.UncompressedSize64
		if total > 384<<20 {
			return errors.New("Web export is too large")
		}
		b.files[name] = file
	}
	if file := b.files["index.html"]; file == nil || file.UncompressedSize64 == 0 {
		return errors.New("Web export has no index.html")
	}
	return nil
}

func (b *Bundle) Close() error { return b.archive.Close() }

func (b *Bundle) ServeFile(w http.ResponseWriter, r *http.Request, name string) {
	file := b.files[name]
	// Game workers are supported. Persistent service workers would outlive a preview.
	if file == nil || r.Header.Get("Service-Worker") != "" || r.Header.Get("Sec-Fetch-Dest") == "serviceworker" {
		http.NotFound(w, r)
		return
	}
	body, err := file.Open()
	if err != nil {
		http.Error(w, "Web export file is unavailable", http.StatusInternalServerError)
		return
	}
	defer body.Close()
	ext := strings.ToLower(path.Ext(name))
	contentType := mime.TypeByExtension(ext)
	switch ext {
	case ".wasm":
		contentType = "application/wasm"
	case ".js", ".mjs":
		contentType = "text/javascript; charset=utf-8"
	case ".pck":
		contentType = "application/octet-stream"
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Length", strconv.FormatUint(file.UncompressedSize64, 10))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Embedder-Policy", "require-corp")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self' 'unsafe-inline' 'wasm-unsafe-eval' blob:; "+
		"worker-src 'self' blob:; connect-src 'self' blob:; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; "+
		"media-src 'self' blob: data:; font-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = io.Copy(w, body)
}
