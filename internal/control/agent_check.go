package control

import (
	"errors"
	"net/http"

	"github.com/PhongFeng/Agentrib/internal/store"
)

func (a *API) agentChecks(w http.ResponseWriter, r *http.Request) {
	machine := r.PathValue("id")
	if !idPattern.MatchString(machine) {
		problem(w, http.StatusBadRequest, "执行机器无效")
		return
	}
	runs, err := a.store.AgentChecks(r.Context(), machine)
	response(w, http.StatusOK, runs, err)
}

func (a *API) checkAgent(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Executor string `json:"executor"`
	}
	if !read(w, r, &input) {
		return
	}
	machine := r.PathValue("id")
	if !idPattern.MatchString(machine) {
		problem(w, http.StatusBadRequest, "执行机器无效")
		return
	}
	run, err := a.store.CheckAgent(r.Context(), machine, input.Executor)
	if errors.Is(err, store.ErrAgentCheckUnavailable) {
		problem(w, http.StatusConflict, err.Error())
		return
	}
	response(w, http.StatusAccepted, run, err)
}
