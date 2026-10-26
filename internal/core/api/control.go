package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
)

func (h *handler) registerControl(mux *http.ServeMux) {
	h.handle(mux, "GET /api/v1/me/control-accesses", func(w http.ResponseWriter, r *http.Request, i auth.Identity) {
		items, err := h.svc.ListControlAccesses(r.Context(), i)
		if err != nil {
			h.fail(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeList(w, items)
	})
	h.handle(mux, "POST /api/v1/me/control-accesses", func(w http.ResponseWriter, r *http.Request, i auth.Identity) {
		in, ok := decode[struct {
			Label     string     `json:"label"`
			Scopes    []string   `json:"scopes"`
			ExpiresAt *time.Time `json:"expiresAt"`
		}](w, r)
		if !ok {
			return
		}
		a, token, err := h.svc.CreateControlAccess(r.Context(), i, domain.ControlAccess{Label: in.Label, Scopes: in.Scopes, ExpiresAt: in.ExpiresAt})
		if err != nil {
			h.fail(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, map[string]any{"access": a, "token": token})
	})
	h.handle(mux, "DELETE /api/v1/control-accesses/{accessId}", func(w http.ResponseWriter, r *http.Request, i auth.Identity) {
		if err := h.svc.RevokeControlAccess(r.Context(), i, r.PathValue("accessId")); err != nil {
			h.fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	catalog := sync.OnceValues(h.controlCatalog)
	protocol := sdk.NewStreamableHTTPHandler(func(r *http.Request) *sdk.Server {
		a := r.Context().Value(controlAccessKey{}).(domain.ControlAccess)
		m := sdk.NewServer(&sdk.Implementation{Name: "threavia-control", Version: h.version}, nil)
		definitions, err := catalog()
		if err != nil {
			h.logger.Error("cannot build control tool catalog", "error", err)
			return nil
		}
		for _, def := range definitions {
			if !slices.Contains(a.Scopes, def.scope) {
				continue
			}
			sdk.AddTool(m, &sdk.Tool{Name: def.name, Description: def.description, InputSchema: def.schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: def.scope == "read"}}, func(ctx context.Context, call *sdk.CallToolRequest, in map[string]any) (*sdk.CallToolResult, any, error) {
				raw, _ := json.Marshal(in)
				return h.callControl(ctx, a, def, raw), nil, nil
			})
		}
		return m
	}, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	for _, method := range []string{"POST", "GET", "DELETE"} {
		pattern := method + " /mcp"
		h.routes = append(h.routes, pattern)
		mux.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if refuseCrossSite(w, r) {
				return
			}
			a, err := h.svc.AuthenticateControl(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || err != nil {
				w.Header().Set("WWW-Authenticate", `Bearer realm="threavia-control"`)
				writeError(w, http.StatusUnauthorized, "unauthenticated", "a valid MCP control access token is required")
				return
			}
			if r.Method != "POST" {
				w.Header().Set("Allow", "POST")
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
			w.Header().Set("Cache-Control", "no-store")
			ctx := context.WithValue(r.Context(), controlAccessKey{}, a)
			ctx = domain.WithClient(domain.WithChannel(domain.WithActor(ctx, "mcp:"+a.ID), "mcp"), domain.Client{ID: a.ID, Name: a.Label})
			protocol.ServeHTTP(w, r.WithContext(ctx))
		}))
	}
}

type controlAccessKey struct{}
type controlDefinition struct {
	name, pattern, scope, description string
	schema                            map[string]any
	parameters                        []map[string]any
}

func (h *handler) callControl(ctx context.Context, a domain.ControlAccess, d controlDefinition, raw json.RawMessage) *sdk.CallToolResult {
	var in map[string]any
	if err := json.Unmarshal(raw, &in); err != nil {
		return controlError("invalid", "arguments must be an object")
	}
	// Recheck the live grant on each invocation, independently of discovery.
	// It also bounds the effects of a request racing with revocation.
	current, err := h.svc.ControlAccess(ctx, a.OwnerID, a.ID)
	if err != nil || !slices.Contains(current.Scopes, d.scope) {
		return controlError("forbidden", "this control access is expired, revoked or lacks the required scope")
	}
	i := auth.Identity{UserID: a.OwnerID, Subject: "mcp:" + a.ID, Name: a.Label}
	var result any
	if d.pattern == "instance_info" {
		result = map[string]any{"name": "threavia-control", "version": h.version, "accessId": a.ID, "label": a.Label, "scopes": current.Scopes}
	}
	if d.pattern == "session_wait" {
		result, err = h.svc.ControlSessionWait(ctx, i, in)
	}
	if d.pattern == "session_read" {
		result, err = h.svc.ControlSessionRead(ctx, i, in)
	}
	if d.pattern == "session_answer" {
		result, err = h.svc.ControlSessionAnswer(ctx, i, in)
	}
	if d.pattern == "session_resolve_validation" {
		result, err = h.svc.ControlResolveValidation(ctx, i, in)
	}
	if strings.Contains(d.pattern, " /api/") {
		result, err = h.controlHTTP(ctx, i, a, d, in)
	}
	if auditErr := h.svc.RecordControlCall(ctx, a, d.name, err == nil); auditErr != nil {
		h.logger.Error("cannot record control call", "tool", d.name, "error", auditErr)
	}
	if err != nil {
		return controlError("operation_failed", err.Error())
	}
	return controlResult(result)
}

func controlError(code, message string) *sdk.CallToolResult {
	r := controlResult(map[string]any{"error": map[string]string{"code": code, "message": message}})
	r.IsError = true
	return r
}
func controlResult(value any) *sdk.CallToolResult {
	if value == nil {
		value = map[string]any{"ok": true}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "cannot encode result"}}}
	}
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(raw)}}, StructuredContent: value}
}

// controlHTTP adapts a fixed, allowlisted operation to the existing API handler.
// There is no network loopback, credential impersonation or arbitrary route tool.
// Request shapes come from OpenAPI, so both clients exercise the same validation.
func (h *handler) controlHTTP(ctx context.Context, i auth.Identity, a domain.ControlAccess, d controlDefinition, in map[string]any) (any, error) {
	method, path, _ := strings.Cut(d.pattern, " ")
	body := make(map[string]any, len(in))
	for k, v := range in {
		body[k] = v
	}
	query := url.Values{}
	paths := map[string]string{}
	for _, p := range d.parameters {
		name, _ := p["name"].(string)
		location, _ := p["in"].(string)
		v, exists := in[name]
		if !exists {
			continue
		}
		delete(body, name)
		if location == "path" {
			id, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("invalid %s", name)
			}
			if _, err := uuid.Parse(id); err != nil {
				return nil, fmt.Errorf("invalid %s", name)
			}
			paths[name] = id
			path = strings.ReplaceAll(path, "{"+name+"}", id)
		} else if location == "query" {
			query.Set(name, fmt.Sprint(v))
		}
	}
	key, _ := body["idempotencyKey"].(string)
	delete(body, "idempotencyKey")
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, method, path+"?"+query.Encode(), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Idempotency-Key", fmt.Sprintf("control:%x", sha256.Sum256([]byte(a.ID+"\x00"+d.name+"\x00"+key))))
	}
	for k, v := range paths {
		r.SetPathValue(k, v)
	}
	fn := h.endpoints[d.pattern]
	if fn == nil {
		return nil, fmt.Errorf("control operation unavailable")
	}
	w := &controlResponse{header: make(http.Header), status: http.StatusOK}
	fn(w, r, i)
	if w.status >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", w.status, strings.TrimSpace(w.body.String()))
	}
	if w.body.Len() == 0 {
		return map[string]any{"ok": true}, nil
	}
	var out any
	err = json.Unmarshal(w.body.Bytes(), &out)
	return out, err
}

type controlResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *controlResponse) Header() http.Header            { return w.header }
func (w *controlResponse) WriteHeader(status int)         { w.status = status }
func (w *controlResponse) Write(data []byte) (int, error) { return w.body.Write(data) }
