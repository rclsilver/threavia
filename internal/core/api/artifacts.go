package api

import (
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
)

// multipartMemory bounds what an upload keeps in memory; the rest spills to a
// temporary file, so a large Artifact never sizes the process.
const multipartMemory = 8 << 20

func (h *handler) registerArtifacts(mux *http.ServeMux) {
	h.handle(mux, "GET /api/v1/projects/{projectId}/artifacts", h.listArtifacts)
	h.handle(mux, "POST /api/v1/projects/{projectId}/artifacts", h.uploadArtifact)
	h.handle(mux, "GET /api/v1/artifacts/{artifactId}", h.getArtifact)
	h.handle(mux, "GET /api/v1/artifacts/{artifactId}/content", h.downloadArtifact)
	h.handle(mux, "DELETE /api/v1/artifacts/{artifactId}", h.deleteArtifact)
}

func (h *handler) listArtifacts(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	artifacts, err := h.svc.ListArtifacts(r.Context(), identity,
		domain.ProjectID(r.PathValue("projectId")), queryInt(r, "limit", 0))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeList(w, artifacts)
}

// uploadArtifact accepts either a multipart form, which is what a browser sends,
// or a raw body, which is what a script sends. Both carry the bytes straight to
// object storage: nothing buffers a whole Artifact in memory to re-encode it.
func (h *handler) uploadArtifact(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if limit := h.svc.MaxArtifactBytes(); limit > 0 {
		// Refused here as well as in storage, so an oversized body stops at the
		// edge instead of crossing the network twice.
		r.Body = http.MaxBytesReader(w, r.Body, limit+1)
	}

	scope := domain.Scope{
		SessionID: domain.SessionID(r.URL.Query().Get("sessionId")),
		JobID:     domain.JobID(r.URL.Query().Get("jobId")),
	}

	var (
		body     io.Reader = r.Body
		filename           = r.URL.Query().Get("filename")
		mimeType           = r.Header.Get("Content-Type")
	)

	if mediaType, _, err := mime.ParseMediaType(mimeType); err == nil && mediaType == "multipart/form-data" {
		part, closePart, err := h.multipartBody(w, r, &filename, &mimeType, &scope)
		if err != nil {
			return
		}
		defer closePart()
		body = part
	}

	artifact, err := h.svc.UploadArtifact(r.Context(), identity,
		domain.ProjectID(r.PathValue("projectId")), filename, mimeType, body, scope)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, artifact)
}

// multipartBody reads the upload out of a form, answering the client itself on a
// malformed request. It returns the open part, which the caller closes.
func (h *handler) multipartBody(w http.ResponseWriter, r *http.Request, filename, mimeType *string, scope *domain.Scope) (io.Reader, func(), error) {
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed upload: "+err.Error())
		return nil, nil, err
	}

	part, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "an upload must carry a file part named \"file\"")
		return nil, nil, err
	}

	if *filename == "" {
		*filename = header.Filename
	}
	*mimeType = partContentType(header)
	if value := r.FormValue("sessionId"); value != "" {
		scope.SessionID = domain.SessionID(value)
	}
	if value := r.FormValue("jobId"); value != "" {
		scope.JobID = domain.JobID(value)
	}
	return part, func() { _ = part.Close() }, nil
}

func (h *handler) getArtifact(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	artifact, err := h.svc.GetArtifact(r.Context(), identity, domain.ArtifactID(r.PathValue("artifactId")))
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, artifact)
}

// downloadArtifact streams the bytes.
//
// Everything is served as an attachment with a fixed content type on purpose:
// Artifacts are agent output, and letting the browser render one in the Threavia
// origin would turn a produced file into a script running as the user.
func (h *handler) downloadArtifact(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	artifact, body, err := h.svc.OpenArtifact(r.Context(), identity, domain.ArtifactID(r.PathValue("artifactId")))
	if err != nil {
		h.fail(w, err)
		return
	}
	defer func() { _ = body.Close() }()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.FormatInt(artifact.Size, 10))
	w.Header().Set("Content-Disposition",
		mime.FormatMediaType("attachment", map[string]string{"filename": artifact.Filename}))
	w.Header().Set("ETag", `"`+artifact.SHA256+`"`)

	if _, err := io.Copy(w, body); err != nil {
		// The status line is already sent, so there is nothing to tell the client:
		// the truncated length is the signal, and this is for the operator.
		h.logger.Warn("artifact download interrupted",
			slog.String("artifactId", string(artifact.ID)), slog.String("error", err.Error()))
	}
}

func (h *handler) deleteArtifact(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if err := h.svc.DeleteArtifact(r.Context(), identity, domain.ArtifactID(r.PathValue("artifactId"))); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// partContentType reports what the form part declared, falling back to the
// generic type rather than guessing from the filename.
func partContentType(header *multipart.FileHeader) string {
	if header == nil {
		return ""
	}
	if value := header.Header.Get("Content-Type"); value != "" {
		return value
	}
	return ""
}
