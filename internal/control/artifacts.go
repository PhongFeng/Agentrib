package control

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

const maxArtifact = 96 << 20

var artifactPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,179}$`)

func validArtifact(name string) bool { return artifactPattern.MatchString(name) }

func (a *API) artifactExists(id string, artifact protocol.Artifact) bool {
	if !idPattern.MatchString(id) {
		return false
	}
	info, err := os.Stat(filepath.Join(a.artifacts, id, artifact.Digest))
	return err == nil && info.Mode().IsRegular() && info.Size() == artifact.Bytes
}

func (a *API) upload(w http.ResponseWriter, r *http.Request, machine string) {
	id, name := r.PathValue("id"), r.PathValue("name")
	lease, expected := r.Header.Get("X-Agentrib-Lease"), r.Header.Get("X-Agentrib-Sha256")
	if !idPattern.MatchString(id) || !validArtifact(name) || !idPattern.MatchString(lease) || !shaPattern.MatchString(expected) ||
		r.ContentLength <= 0 || r.ContentLength > maxArtifact {
		problem(w, http.StatusBadRequest, "Invalid artifact metadata or size")
		return
	}
	allowed, err := a.store.ArtifactAllowed(r.Context(), machine, id, lease)
	if err != nil {
		response(w, 0, nil, err)
		return
	}
	if !allowed {
		problem(w, http.StatusConflict, "Run state or lease changed")
		return
	}
	dir := filepath.Join(a.artifacts, id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		response(w, 0, nil, err)
		return
	}
	temp, err := os.CreateTemp(dir, ".upload-")
	if err != nil {
		response(w, 0, nil, err)
		return
	}
	defer os.Remove(temp.Name())
	digest := sha256.New()
	size, err := io.Copy(io.MultiWriter(temp, digest), http.MaxBytesReader(w, r.Body, maxArtifact))
	closeErr := temp.Close()
	if err != nil || size != r.ContentLength || expected != hex.EncodeToString(digest.Sum(nil)) {
		problem(w, http.StatusBadRequest, "Artifact size or SHA-256 does not match")
		return
	}
	if closeErr != nil {
		response(w, 0, nil, closeErr)
		return
	}
	if err := os.Rename(temp.Name(), filepath.Join(dir, expected)); err != nil {
		response(w, 0, nil, err)
		return
	}
	send(w, http.StatusCreated, protocol.Artifact{Name: name, Digest: expected, Bytes: size})
}

func (a *API) download(w http.ResponseWriter, r *http.Request) {
	id, name := r.PathValue("id"), r.PathValue("name")
	run, err := a.store.Run(r.Context(), id)
	if err != nil {
		response(w, 0, nil, err)
		return
	}
	if run.Result != nil {
		for _, artifact := range run.Result.Artifacts {
			if artifact.Name != name || !a.artifactExists(id, artifact) {
				continue
			}
			file, err := os.Open(filepath.Join(a.artifacts, id, artifact.Digest))
			if err != nil {
				response(w, 0, nil, err)
				return
			}
			defer file.Close()
			info, err := file.Stat()
			if err != nil {
				response(w, 0, nil, err)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
			http.ServeContent(w, r, name, info.ModTime(), file)
			return
		}
	}
	problem(w, http.StatusNotFound, "Artifact not found")
}
