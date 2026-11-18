package control

import (
	"net/http"
	"strings"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

func (a *API) projects(w http.ResponseWriter, r *http.Request) {
	list, err := a.store.Projects(r.Context(), r.PathValue("id"))
	response(w, http.StatusOK, list, err)
}

func (a *API) createProject(w http.ResponseWriter, r *http.Request) {
	var input protocol.ProjectInput
	if !read(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	engine, known := protocol.FindEngine(input.Engine)
	if !idPattern.MatchString(r.PathValue("id")) || !slugPattern.MatchString(input.ID) ||
		!validText(input.Name, 120) || !known || !engine.Implemented {
		problem(w, http.StatusBadRequest, "请输入有效的项目名称、标识和已接入的游戏引擎")
		return
	}
	err := a.store.RequestProject(r.Context(), r.PathValue("id"), input)
	response(w, http.StatusAccepted, input, err)
}

func (a *API) projectCommits(w http.ResponseWriter, r *http.Request) {
	machine, project := r.PathValue("id"), r.PathValue("project")
	if !idPattern.MatchString(machine) || !slugPattern.MatchString(project) {
		problem(w, http.StatusBadRequest, "Invalid project")
		return
	}
	commits, err := a.store.ProjectCommits(r.Context(), machine, project)
	response(w, http.StatusOK, commits, err)
}
