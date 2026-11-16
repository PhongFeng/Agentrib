package control

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/PhongFeng/Agentrib/internal/protocol"
	"github.com/PhongFeng/Agentrib/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	idPattern     = regexp.MustCompile(`^[a-f0-9]{32}$`)
	slugPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)
	commitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
	shaPattern    = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type API struct {
	store    *store.Store
	operator [32]byte
}

func New(db *store.Store, operatorToken string) http.Handler {
	a := &API{store: db, operator: sha256.Sum256([]byte(operatorToken))}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /api/v1/machines", a.asOperator(a.machines))
	mux.HandleFunc("POST /api/v1/machines", a.asOperator(a.enroll))
	mux.HandleFunc("GET /api/v1/runs", a.asOperator(a.runs))
	mux.HandleFunc("POST /api/v1/runs", a.asOperator(a.createRun))
	mux.HandleFunc("GET /api/v1/runs/{id}", a.asOperator(a.run))
	mux.HandleFunc("POST /api/v1/runs/{id}/cancel", a.asOperator(a.cancel))
	mux.HandleFunc("GET /api/v1/workflows", a.asOperator(a.workflows))
	mux.HandleFunc("GET /api/v1/workflows/template", a.asOperator(a.template))
	mux.HandleFunc("POST /api/v1/workflows", a.asOperator(a.saveWorkflow))
	mux.HandleFunc("GET /api/v1/executions", a.asOperator(a.executions))
	mux.HandleFunc("POST /api/v1/executions", a.asOperator(a.startExecution))
	mux.HandleFunc("GET /api/v1/executions/{id}", a.asOperator(a.execution))
	mux.HandleFunc("POST /api/v1/executions/{id}/approve/{step}", a.asOperator(a.approve))
	mux.HandleFunc("POST /api/v1/runner/heartbeat", a.asRunner(a.heartbeat))
	mux.HandleFunc("POST /api/v1/runner/claim", a.asRunner(a.claim))
	mux.HandleFunc("POST /api/v1/runner/runs/{id}/start", a.asRunner(a.start))
	mux.HandleFunc("POST /api/v1/runner/runs/{id}/report", a.asRunner(a.report))
	return mux
}

func send(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func problem(w http.ResponseWriter, code int, message string) {
	send(w, code, map[string]string{"error": message})
}

func response(w http.ResponseWriter, status int, value any, err error) {
	var pgErr *pgconn.PgError
	switch {
	case err == nil:
		send(w, status, value)
	case errors.Is(err, store.ErrConflict):
		problem(w, http.StatusConflict, "Run state or lease changed")
	case errors.Is(err, pgx.ErrNoRows), errors.As(err, &pgErr) && pgErr.Code == "23503":
		problem(w, http.StatusNotFound, "Record not found")
	default:
		slog.Error("request failed", "error", err)
		problem(w, http.StatusInternalServerError, "Server error")
	}
}

func read(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
		problem(w, http.StatusBadRequest, "Invalid JSON request")
		return false
	}
	return true
}

func bearer(r *http.Request) string {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return token
}

func (a *API) asOperator(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		digest := sha256.Sum256([]byte(bearer(r)))
		if subtle.ConstantTimeCompare(digest[:], a.operator[:]) != 1 {
			problem(w, http.StatusUnauthorized, "Operator token required")
			return
		}
		next(w, r)
	}
}

func (a *API) asRunner(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearer(r)
		if !shaPattern.MatchString(token) {
			problem(w, http.StatusUnauthorized, "Runner token required")
			return
		}
		id, err := a.store.AuthRunner(r.Context(), token)
		if errors.Is(err, pgx.ErrNoRows) {
			problem(w, http.StatusUnauthorized, "Runner token required")
			return
		}
		if err != nil {
			response(w, 0, nil, err)
			return
		}
		next(w, r, id)
	}
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	if err := a.store.Ping(r.Context()); err != nil {
		problem(w, http.StatusServiceUnavailable, "Database unavailable")
		return
	}
	send(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) machines(w http.ResponseWriter, r *http.Request) {
	list, err := a.store.Machines(r.Context())
	response(w, http.StatusOK, list, err)
}

func (a *API) enroll(w http.ResponseWriter, r *http.Request) {
	var input struct{ Name string }
	if !read(w, r, &input) {
		return
	}
	name := strings.TrimSpace(input.Name)
	if !validText(name, 120) {
		problem(w, http.StatusBadRequest, "Machine name is required")
		return
	}
	machine, err := a.store.Enroll(r.Context(), name)
	response(w, http.StatusCreated, machine, err)
}

func (a *API) createRun(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID   string        `json:"id"`
		Spec protocol.Spec `json:"spec"`
	}
	if !read(w, r, &input) {
		return
	}
	if !idPattern.MatchString(input.ID) || !validSpec(input.Spec) {
		problem(w, http.StatusBadRequest, "Invalid run specification")
		return
	}
	run, err := a.store.CreateRun(r.Context(), input.ID, input.Spec)
	response(w, http.StatusCreated, run, err)
}

func validSpec(spec protocol.Spec) bool {
	return idPattern.MatchString(spec.MachineID) && slugPattern.MatchString(spec.Project) && validExecutor(spec.Executor) &&
		validText(spec.Title, 200) && len(spec.Prompt) <= 16000 && !strings.ContainsRune(spec.Prompt, 0) &&
		(spec.Executor == "godot" || validText(spec.Prompt, 16000)) && (spec.Commit == "" || commitPattern.MatchString(spec.Commit))
}

func validText(s string, max int) bool {
	return len(s) <= max && strings.TrimSpace(s) != "" && !strings.ContainsRune(s, 0)
}

func validExecutor(key string) bool {
	return key == "codex" || key == "qoder-cn" || key == "godot"
}

func (a *API) runs(w http.ResponseWriter, r *http.Request) {
	runs, err := a.store.Runs(r.Context(), "")
	response(w, http.StatusOK, runs, err)
}

func (a *API) run(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	after := 0
	if cursor := r.URL.Query().Get("after"); cursor != "" {
		var err error
		after, err = strconv.Atoi(cursor)
		if err != nil || after < 0 || after > 10000 {
			problem(w, http.StatusBadRequest, "Invalid event cursor")
			return
		}
	}
	run, err := a.store.Run(r.Context(), id)
	if err != nil {
		response(w, 0, nil, err)
		return
	}
	events, err := a.store.Events(r.Context(), id, after)
	response(w, http.StatusOK, map[string]any{"run": run, "events": events}, err)
}

func (a *API) cancel(w http.ResponseWriter, r *http.Request) {
	run, err := a.store.Cancel(r.Context(), r.PathValue("id"))
	response(w, http.StatusOK, run, err)
}

func (a *API) heartbeat(w http.ResponseWriter, r *http.Request, id string) {
	var h protocol.Heartbeat
	if !read(w, r, &h) {
		return
	}
	if len(h.Executors) > 3 || len(h.Projects) > 64 {
		problem(w, http.StatusBadRequest, "Heartbeat too large")
		return
	}
	for _, key := range h.Executors {
		if !validExecutor(key) {
			problem(w, http.StatusBadRequest, "Unknown executor")
			return
		}
	}
	for _, key := range h.Projects {
		if !slugPattern.MatchString(key) {
			problem(w, http.StatusBadRequest, "Invalid project key")
			return
		}
	}
	if h.Executors == nil {
		h.Executors = []string{}
	}
	if h.Projects == nil {
		h.Projects = []string{}
	}
	response(w, http.StatusOK, map[string]string{"status": "ok"}, a.store.Heartbeat(r.Context(), id, h))
}

func (a *API) claim(w http.ResponseWriter, r *http.Request, id string) {
	run, err := a.store.Claim(r.Context(), id)
	response(w, http.StatusOK, run, err)
}

func (a *API) start(w http.ResponseWriter, r *http.Request, machine string) {
	var input struct{ Lease string }
	if !read(w, r, &input) {
		return
	}
	if !idPattern.MatchString(input.Lease) {
		problem(w, http.StatusBadRequest, "Invalid lease")
		return
	}
	response(w, http.StatusOK, protocol.RunReply{Status: "running"}, a.store.Start(r.Context(), machine, r.PathValue("id"), input.Lease))
}

func (a *API) report(w http.ResponseWriter, r *http.Request, machine string) {
	var report protocol.Report
	if !read(w, r, &report) {
		return
	}
	if !idPattern.MatchString(report.Lease) || len(report.Events) > 64 || !validResult(report.Result) {
		problem(w, http.StatusBadRequest, "Invalid run report")
		return
	}
	for _, event := range report.Events {
		if event.Seq < 1 || event.Seq > 10000 || !validText(event.Text, 6000) || !validText(event.Kind, 32) {
			problem(w, http.StatusBadRequest, "Invalid event")
			return
		}
	}
	reply, err := a.store.Report(r.Context(), machine, r.PathValue("id"), report)
	response(w, http.StatusOK, reply, err)
}

func validResult(result *protocol.Result) bool {
	if result == nil {
		return true
	}
	if result.Status != "succeeded" && result.Status != "failed" && result.Status != "canceled" {
		return false
	}
	if !validText(result.Detail, 3000) || result.Commit != "" && !commitPattern.MatchString(result.Commit) || len(result.Artifacts) > 12 {
		return false
	}
	for _, artifact := range result.Artifacts {
		if !validText(artifact.Name, 180) || strings.ContainsAny(artifact.Name, `/\`) || !shaPattern.MatchString(artifact.Digest) || artifact.Bytes < 1 {
			return false
		}
	}
	return true
}
