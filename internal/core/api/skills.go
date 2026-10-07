package api

import (
	"io"
	"net/http"
	"strings"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
)

func (h *handler) registerSkills(mux *http.ServeMux) {
	h.handle(mux, "GET /api/v1/projects/{projectId}/skills", h.listSkills)
	h.handle(mux, "POST /api/v1/projects/{projectId}/skills", h.installSkill)
	h.handle(mux, "DELETE /api/v1/skills/{skillId}", h.uninstallSkill)
	h.handle(mux, "GET /api/v1/backends/{backendId}/skills", h.listBackendSkills)
}

func (h *handler) listSkills(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	installed, err := h.svc.ListSkills(r.Context(), identity, domain.ProjectID(r.PathValue("projectId")))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeList(w, installed)
}

// installSkill acquires a Skill. A JSON body names a git or archive source; a
// raw or multipart body carries an uploaded archive.
func (h *handler) installSkill(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	projectID := domain.ProjectID(r.PathValue("projectId"))

	if isUpload(r) {
		h.installUploadedSkill(w, r, identity, projectID)
		return
	}

	body, ok := decode[struct {
		Name   string `json:"name"`
		Source struct {
			Type     string `json:"type"`
			URL      string `json:"url"`
			Path     string `json:"path"`
			Revision string `json:"revision"`
		} `json:"source"`
	}](w, r)
	if !ok {
		return
	}

	skill, err := h.svc.InstallSkill(r.Context(), identity, projectID, domain.SkillSource{
		Type:     domain.SkillSourceType(strings.ToUpper(body.Source.Type)),
		URL:      body.Source.URL,
		Path:     body.Source.Path,
		Revision: body.Source.Revision,
	}, body.Name, nil)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, skill)
}

// installUploadedSkill acquires a Skill from an archive the user sent.
func (h *handler) installUploadedSkill(w http.ResponseWriter, r *http.Request, identity auth.Identity, projectID domain.ProjectID) {
	if limit := h.svc.MaxArtifactBytes(); limit > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, limit+1)
	}

	var (
		archive  io.Reader = r.Body
		filename           = r.URL.Query().Get("filename")
		name               = r.URL.Query().Get("name")
		path               = r.URL.Query().Get("path")
	)

	if isMultipart(r) {
		if err := r.ParseMultipartForm(multipartMemory); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "malformed upload: "+err.Error())
			return
		}
		part, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request",
				"an uploaded skill must carry a file part named \"file\"")
			return
		}
		defer func() { _ = part.Close() }()

		archive = part
		if filename == "" {
			filename = header.Filename
		}
		if value := r.FormValue("name"); value != "" {
			name = value
		}
		if value := r.FormValue("path"); value != "" {
			path = value
		}
	}

	// The filename only selects the archive format; it never shapes anything
	// stored.
	skill, err := h.svc.InstallSkill(r.Context(), identity, projectID, domain.SkillSource{
		Type: domain.SkillSourceUpload,
		URL:  filename,
		Path: path,
	}, name, archive)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, skill)
}

func (h *handler) uninstallSkill(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if err := h.svc.UninstallSkill(r.Context(), identity, domain.SkillID(r.PathValue("skillId"))); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listBackendSkills returns the Skills that exist only on one backend. Core
// knows their metadata and never their content, which is what lets a handoff
// report that another backend cannot run one (spec section 18).
func (h *handler) listBackendSkills(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	local, err := h.svc.BackendSkills(r.Context(), identity,
		domain.BackendInstanceID(r.PathValue("backendId")))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeList(w, local)
}

// isUpload reports whether the request carries an archive rather than a JSON
// description of where to fetch one.
func isUpload(r *http.Request) bool {
	contentType := r.Header.Get("Content-Type")
	return contentType != "" && !strings.HasPrefix(contentType, "application/json")
}

func isMultipart(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data")
}
