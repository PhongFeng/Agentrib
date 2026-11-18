package control

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PhongFeng/Agentrib/internal/protocol"
	"github.com/jackc/pgx/v5"
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

const (
	downloadCookie        = "agentrib_download"
	downloadGrantDuration = 10 * time.Minute
)

func (a *API) downloadSignature(path, expires string) string {
	mac := hmac.New(sha256.New, a.operator[:])
	_, _ = fmt.Fprintf(mac, "artifact download\n%s\n%s", path, expires)
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *API) downloadGranted(r *http.Request, now time.Time) bool {
	cookie, err := r.Cookie(downloadCookie)
	if err != nil {
		return false
	}
	expires, signature, ok := strings.Cut(cookie.Value, ".")
	if !ok {
		return false
	}
	until, err := strconv.ParseInt(expires, 10, 64)
	return err == nil && now.Unix() < until &&
		hmac.Equal([]byte(signature), []byte(a.downloadSignature(r.URL.Path, expires)))
}

func (a *API) authorizeDownload(w http.ResponseWriter, r *http.Request) {
	if _, err := a.runArtifact(r); err != nil {
		response(w, 0, nil, err)
		return
	}
	expires := time.Now().Add(downloadGrantDuration)
	stamp := strconv.FormatInt(expires.Unix(), 10)
	http.SetCookie(w, &http.Cookie{
		Name: downloadCookie, Value: stamp + "." + a.downloadSignature(r.URL.Path, stamp),
		Path: r.URL.Path, MaxAge: int(downloadGrantDuration.Seconds()), Expires: expires,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) runArtifact(r *http.Request) (protocol.Artifact, error) {
	id, name := r.PathValue("id"), r.PathValue("name")
	if !idPattern.MatchString(id) || !validArtifact(name) {
		return protocol.Artifact{}, pgx.ErrNoRows
	}
	run, err := a.store.Run(r.Context(), id)
	if err != nil {
		return protocol.Artifact{}, err
	}
	if run.Result != nil {
		for _, artifact := range run.Result.Artifacts {
			if artifact.Name == name && a.artifactExists(id, artifact) {
				return artifact, nil
			}
		}
	}
	return protocol.Artifact{}, pgx.ErrNoRows
}

func (a *API) download(w http.ResponseWriter, r *http.Request) {
	if !a.isOperator(r) && !a.downloadGranted(r, time.Now()) {
		problem(w, http.StatusUnauthorized, "Operator token or download permission required")
		return
	}
	artifact, err := a.runArtifact(r)
	if err != nil {
		response(w, 0, nil, err)
		return
	}
	file, err := os.Open(filepath.Join(a.artifacts, r.PathValue("id"), artifact.Digest))
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
	// Give slow downloads more time than the deadline for ordinary API responses.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Minute))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": artifact.Name}))
	http.ServeContent(w, r, artifact.Name, info.ModTime(), file)
}
