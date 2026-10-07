package api

import (
	"net/http"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
)

func (h *handler) registerKnowledge(mux *http.ServeMux) {
	h.handle(mux, "GET /api/v1/projects/{projectId}/tasks", h.listTasks)
	h.handle(mux, "POST /api/v1/projects/{projectId}/tasks", h.createTask)
	h.handle(mux, "GET /api/v1/projects/{projectId}/tasks/ready", h.readyTasks)
	h.handle(mux, "PATCH /api/v1/tasks/{taskId}", h.updateTask)

	h.handle(mux, "GET /api/v1/projects/{projectId}/decisions", h.listDecisions)
	h.handle(mux, "POST /api/v1/projects/{projectId}/decisions", h.createDecision)

	h.handle(mux, "GET /api/v1/projects/{projectId}/search", h.searchProject)
}

func (h *handler) listTasks(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	tasks, err := h.svc.ListTasks(r.Context(), identity,
		domain.ProjectID(r.PathValue("projectId")), queryBool(r, "includeDone"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeList(w, tasks)
}

func (h *handler) createTask(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[struct {
		Title       string   `json:"title"`
		Description string   `json:"description"`
		DependsOn   []string `json:"dependsOn"`
	}](w, r)
	if !ok {
		return
	}

	dependsOn := make([]domain.TaskID, 0, len(body.DependsOn))
	for _, id := range body.DependsOn {
		dependsOn = append(dependsOn, domain.TaskID(id))
	}

	task, err := h.svc.CreateTask(r.Context(), identity,
		domain.ProjectID(r.PathValue("projectId")), body.Title, body.Description, dependsOn)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, task)
}

// readyTasks lists what can be started now: TODO with every dependency done.
// Blocked is derived from the graph, never stored.
func (h *handler) readyTasks(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	tasks, err := h.svc.ReadyTasks(r.Context(), identity,
		domain.ProjectID(r.PathValue("projectId")), queryInt(r, "limit", 0))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeList(w, tasks)
}

func (h *handler) updateTask(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Status      string `json:"status"`
	}](w, r)
	if !ok {
		return
	}

	task, err := h.svc.UpdateTask(r.Context(), identity, domain.TaskID(r.PathValue("taskId")),
		body.Title, body.Description, domain.TaskStatus(body.Status))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (h *handler) listDecisions(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	decisions, err := h.svc.ListDecisions(r.Context(), identity,
		domain.ProjectID(r.PathValue("projectId")), queryBool(r, "includeSuperseded"))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeList(w, decisions)
}

func (h *handler) createDecision(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	body, ok := decode[struct {
		Title      string  `json:"title"`
		Content    string  `json:"content"`
		Importance string  `json:"importance"`
		Supersedes *string `json:"supersedes"`
	}](w, r)
	if !ok {
		return
	}

	var supersedes *domain.DecisionID
	if body.Supersedes != nil && *body.Supersedes != "" {
		id := domain.DecisionID(*body.Supersedes)
		supersedes = &id
	}

	decision, err := h.svc.CreateDecision(r.Context(), identity,
		domain.ProjectID(r.PathValue("projectId")), body.Title, body.Content,
		domain.DecisionImportance(body.Importance), supersedes)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, decision)
}

// searchProject searches the whole project memory at once: the explicit
// structured layer and the episodic one (spec section 15).
func (h *handler) searchProject(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	projectID := domain.ProjectID(r.PathValue("projectId"))
	query := r.URL.Query().Get("q")
	limit := queryInt(r, "limit", 0)

	tasks, err := h.svc.SearchTasks(r.Context(), identity, projectID, query, limit)
	if err != nil {
		h.fail(w, err)
		return
	}
	decisions, err := h.svc.SearchDecisions(r.Context(), identity, projectID, query, limit)
	if err != nil {
		h.fail(w, err)
		return
	}
	history, err := h.svc.SearchHistory(r.Context(), identity, projectID, query, limit)
	if err != nil {
		h.fail(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tasks":     tasks,
		"decisions": decisions,
		"history":   history,
	})
}
