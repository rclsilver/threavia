package api

import (
	"net/http"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
)

func (h *handler) registerProjects(mux *http.ServeMux) {
	h.handle(mux, "GET /api/v1/projects", h.listProjects)
	h.handle(mux, "POST /api/v1/projects", h.createProject)
	h.handle(mux, "GET /api/v1/projects/{projectId}", h.getProject)
	h.handle(mux, "POST /api/v1/projects/{projectId}/archive", h.archiveProject)
	h.handle(mux, "POST /api/v1/projects/{projectId}/restore", h.restoreProject)
	h.handle(mux, "DELETE /api/v1/projects/{projectId}", h.deleteProject)

	h.handle(mux, "GET /api/v1/projects/{projectId}/sessions", h.listSessions)
	h.handle(mux, "GET /api/v1/projects/{projectId}/directories", h.listDirectories)
	h.handle(mux, "POST /api/v1/projects/{projectId}/directories", h.createDirectory)
	h.handle(mux, "GET /api/v1/directories/{directoryId}/bindings", h.listBindings)
	h.handle(mux, "POST /api/v1/directories/{directoryId}/bindings", h.bindDirectory)
}

func (h *handler) listProjects(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	projects, err := h.svc.ListProjects(r.Context(), identity, queryBool(r, "includeArchived"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeList(w, projects)
}

func (h *handler) createProject(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}](w, r)
	if !ok {
		return
	}

	project, err := h.svc.CreateProject(r.Context(), identity, body.Name, body.Description)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, project)
}

func (h *handler) getProject(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	project, err := h.svc.GetProject(r.Context(), identity, domain.ProjectID(r.PathValue("projectId")))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (h *handler) archiveProject(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	h.setProjectStatus(w, r, identity, domain.ProjectArchived)
}

func (h *handler) restoreProject(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	h.setProjectStatus(w, r, identity, domain.ProjectActive)
}

func (h *handler) setProjectStatus(w http.ResponseWriter, r *http.Request, identity auth.Identity, status domain.ProjectStatus) {
	project, err := h.svc.SetProjectStatus(r.Context(), identity, domain.ProjectID(r.PathValue("projectId")), status)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, project)
}

// deleteProject is the explicit permanent deletion of specification section 21,
// separate from archiving. BackendInstances are user-owned and survive it.
func (h *handler) deleteProject(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if err := h.svc.DeleteProject(r.Context(), identity, domain.ProjectID(r.PathValue("projectId"))); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) listDirectories(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	dirs, err := h.svc.ListKnownDirectories(r.Context(), identity, domain.ProjectID(r.PathValue("projectId")))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeList(w, dirs)
}

func (h *handler) createDirectory(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[struct {
		Name        string  `json:"name"`
		Description string  `json:"description"`
		GitRemote   *string `json:"gitRemote"`
	}](w, r)
	if !ok {
		return
	}

	dir, err := h.svc.CreateKnownDirectory(r.Context(), identity,
		domain.ProjectID(r.PathValue("projectId")), body.Name, body.Description, body.GitRemote)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, dir)
}

func (h *handler) listBindings(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	bindings, err := h.svc.ListBindings(r.Context(), identity, domain.KnownDirectoryID(r.PathValue("directoryId")))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeList(w, bindings)
}

// bindDirectory records where a logical directory lives on one backend. The
// path is backend truth and Core never validates it: it has no access to the
// backend filesystem.
func (h *handler) bindDirectory(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[struct {
		BackendInstanceID string `json:"backendInstanceId"`
		Path              string `json:"path"`
	}](w, r)
	if !ok {
		return
	}

	binding, err := h.svc.BindKnownDirectory(r.Context(), identity,
		domain.KnownDirectoryID(r.PathValue("directoryId")),
		domain.BackendInstanceID(body.BackendInstanceID), body.Path)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, binding)
}
