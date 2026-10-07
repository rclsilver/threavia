// Package mcp hosts the Model Context Protocol server through which Claude Code
// asks the user for things.
//
// This is the Claude-specific adaptation of two provider-independent Threavia
// concepts (THREAVIA_SPEC_V1.md sections 12 and 16): a permission request
// becomes a ValidationRequest, and a question becomes a UserInputRequest. Claude
// Code is pointed at it with --permission-prompt-tool and --mcp-config, so a
// prompt that would block an interactive terminal instead travels to Core and
// waits there, for as long as it takes.
//
// The server listens on the loopback interface only, and every Job gets its own
// unguessable endpoint path, so the only caller able to reach a Job's tools is
// the process started for it.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

// ServerName is the MCP server name Claude Code sees. Tool names it exposes are
// therefore mcp__threavia__<tool>.
const ServerName = "threavia"

// Tool names exposed to the agent.
const (
	// ToolApprovalPrompt is wired to --permission-prompt-tool: Claude Code calls
	// it instead of prompting a terminal.
	ToolApprovalPrompt = "approval_prompt"
	// ToolAskUser lets the agent ask for an answer, a choice or information.
	ToolAskUser = "ask_user"
)

// PermissionTool is the fully qualified name to pass to --permission-prompt-tool.
const PermissionTool = "mcp__" + ServerName + "__" + ToolApprovalPrompt

// Decision is what the user decided about a tool invocation.
type Decision struct {
	Approved bool
	// Reason is shown to the agent when the request is denied.
	Reason string
}

// CoreTool is one Core Tool, as Core declared it in the ProjectContext at Job
// start. The backend hardcodes none of this: it forwards what it was told.
type CoreTool struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// Asker is what the server needs from the backend: a way to turn a local tool
// call into a Threavia request and wait for the answer.
//
// The first two block until the user answers, the Job ends or the context is
// cancelled. Waiting indefinitely is the specified behaviour: validation and
// input requests never time out.
type Asker interface {
	// AskPermission raises a ValidationRequest for a tool invocation.
	AskPermission(ctx context.Context, jobID, toolName string, input map[string]any) (Decision, error)
	// AskUser raises a UserInputRequest.
	//
	// freeText is explicit rather than derived from the absence of choices: a
	// question may legitimately offer shortcuts and still accept an answer that
	// is none of them, and a request that offers a choice while asking for a
	// path contradicts itself.
	AskUser(ctx context.Context, jobID, prompt string, choices []string, freeText bool) (string, error)
	// CallCoreTool runs a Core Tool through Core and returns its result.
	CallCoreTool(ctx context.Context, jobID, name string, input map[string]any) (map[string]any, error)
}

// Server is the loopback MCP endpoint.
type Server struct {
	asker  Asker
	logger *slog.Logger

	mu       sync.RWMutex
	sessions map[string]*session // endpoint token -> job

	listener net.Listener
	http     *http.Server
}

// New builds the server. Install the Asker with SetAsker, then call Start.
func New(logger *slog.Logger) *Server {
	return &Server{
		logger:   logger,
		sessions: make(map[string]*session),
	}
}

// SetAsker installs what answers the tools. It must be called before Start:
// the adapter that answers needs the runner, which needs this server, so the
// three are wired in that order and only then does the endpoint accept calls.
func (s *Server) SetAsker(asker Asker) { s.asker = asker }

// Start binds the loopback listener and serves until Close.
func (s *Server) Start() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("bind the local tool endpoint: %w", err)
	}
	s.listener = listener

	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp/{token}", s.handle)
	s.http = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		if err := s.http.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("local tool endpoint stopped", slog.String("error", err.Error()))
		}
	}()
	return nil
}

// Close stops the server.
func (s *Server) Close(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
}

// session is one Job and what it may call.
type session struct {
	jobID     string
	coreTools []CoreTool
}

// Register gives a Job its own endpoint and returns the URL to configure Claude
// Code with. The token is the only thing standing between a local process and
// another Job's prompts and project knowledge, so it must be unguessable.
func (s *Server) Register(jobID, token string, coreTools []CoreTool) string {
	s.mu.Lock()
	s.sessions[token] = &session{jobID: jobID, coreTools: coreTools}
	s.mu.Unlock()
	return fmt.Sprintf("http://%s/mcp/%s", s.listener.Addr().String(), token)
}

// Unregister drops a Job endpoint once its process is gone.
func (s *Server) Unregister(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

func (s *Server) sessionFor(token string) (*session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[token]
	return sess, ok
}

// JSON-RPC envelopes.
type (
	request struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	response struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  any             `json:"result,omitempty"`
		Error   *rpcError       `json:"error,omitempty"`
	}
	rpcError struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
)

const (
	codeMethodNotFound = -32601
	codeInternalError  = -32603
)

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessionFor(r.PathValue("token"))
	if !ok {
		http.NotFound(w, r)
		return
	}

	var msg request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&msg); err != nil {
		http.Error(w, "malformed request", http.StatusBadRequest)
		return
	}

	// A notification carries no id and expects no reply.
	if len(msg.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	result, rpcErr := s.dispatch(r.Context(), sess, msg)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response{JSONRPC: "2.0", ID: msg.ID, Result: result, Error: rpcErr})
}

func (s *Server) dispatch(ctx context.Context, sess *session, msg request) (any, *rpcError) {
	switch msg.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(msg.Params, &params)
		// Echoing the client protocol version keeps this server compatible with
		// whatever revision Claude Code speaks.
		return map[string]any{
			"protocolVersion": params.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": ServerName, "version": "1"},
		}, nil

	case "tools/list":
		return map[string]any{"tools": toolDefinitions(sess.coreTools)}, nil

	case "tools/call":
		return s.call(ctx, sess, msg.Params)

	default:
		return nil, &rpcError{Code: codeMethodNotFound, Message: "unknown method " + msg.Method}
	}
}

func (s *Server) call(ctx context.Context, sess *session, raw json.RawMessage) (any, *rpcError) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &rpcError{Code: codeInternalError, Message: "malformed tool call"}
	}

	switch params.Name {
	case ToolApprovalPrompt:
		return s.approvalPrompt(ctx, sess.jobID, params.Arguments)
	case ToolAskUser:
		return s.askUser(ctx, sess.jobID, params.Arguments)
	}

	// Anything else must be one of the Core Tools this Job was told about.
	// Checking against that list, rather than forwarding whatever was asked,
	// keeps the endpoint from becoming a general proxy into Core.
	for _, tool := range sess.coreTools {
		if tool.Name == params.Name {
			return s.coreTool(ctx, sess.jobID, params.Name, params.Arguments)
		}
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: "unknown tool " + params.Name}
}

// approvalPrompt turns a Claude Code permission prompt into a Threavia
// ValidationRequest and answers in the shape Claude Code expects.
func (s *Server) approvalPrompt(ctx context.Context, jobID string, args map[string]any) (any, *rpcError) {
	toolName, _ := args["tool_name"].(string)
	input, _ := args["input"].(map[string]any)
	if input == nil {
		input = map[string]any{}
	}

	s.logger.Info("permission requested",
		slog.String("jobId", jobID), slog.String("tool", toolName))

	decision, err := s.asker.AskPermission(ctx, jobID, toolName, input)
	if err != nil {
		// Failing closed is the only safe default: an unanswered permission
		// request must never become an approval.
		s.logger.Warn("permission request failed, denying",
			slog.String("jobId", jobID), slog.String("error", err.Error()))
		return toolText(map[string]any{
			"behavior": "deny",
			"message":  "Threavia could not obtain a decision: " + err.Error(),
		})
	}

	if !decision.Approved {
		message := decision.Reason
		if message == "" {
			message = "The user declined this action."
		}
		return toolText(map[string]any{"behavior": "deny", "message": message})
	}
	return toolText(map[string]any{"behavior": "allow", "updatedInput": input})
}

// askUser turns an agent question into a Threavia UserInputRequest.
func (s *Server) askUser(ctx context.Context, jobID string, args map[string]any) (any, *rpcError) {
	prompt, _ := args["prompt"].(string)
	if prompt == "" {
		return nil, &rpcError{Code: codeInternalError, Message: "a prompt is required"}
	}

	var choices []string
	if raw, ok := args["choices"].([]any); ok {
		for _, choice := range raw {
			if text, ok := choice.(string); ok {
				choices = append(choices, text)
			}
		}
	}

	s.logger.Info("user input requested", slog.String("jobId", jobID))
	// The tool describes `choices` as a closed list, so offering one is the
	// agent saying it wants nothing else.
	answer, err := s.asker.AskUser(ctx, jobID, prompt, choices, len(choices) == 0)
	if err != nil {
		return nil, &rpcError{Code: codeInternalError, Message: err.Error()}
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": answer}}}, nil
}

// toolText wraps a payload as the single text content block a tool returns.
func toolText(payload map[string]any) (any, *rpcError) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, &rpcError{Code: codeInternalError, Message: err.Error()}
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(encoded)}}}, nil
}

// coreTool forwards a Core Tool call to Core and renders its result.
//
// The round trip can be slow and can fail; an explicit error reaches the agent
// rather than an empty result it would read as "nothing found".
func (s *Server) coreTool(ctx context.Context, jobID, name string, args map[string]any) (any, *rpcError) {
	if args == nil {
		args = map[string]any{}
	}
	s.logger.Info("core tool called",
		slog.String("jobId", jobID), slog.String("tool", name))

	result, err := s.asker.CallCoreTool(ctx, jobID, name, args)
	if err != nil {
		return nil, &rpcError{Code: codeInternalError, Message: err.Error()}
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, &rpcError{Code: codeInternalError, Message: err.Error()}
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(encoded)}}}, nil
}

// toolDefinitions lists what this Job may call: the two Threavia-specific tools,
// plus the Core Tools Core declared for it.
func toolDefinitions(coreTools []CoreTool) []any {
	definitions := []any{
		map[string]any{
			"name":        ToolApprovalPrompt,
			"description": "Ask the Threavia user to approve or deny a tool invocation.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tool_name": map[string]any{"type": "string", "description": "The tool being invoked."},
					"input":     map[string]any{"type": "object", "description": "The tool input."},
				},
				"required": []string{"tool_name", "input"},
			},
		},
		map[string]any{
			"name": ToolAskUser,
			"description": "Ask the Threavia user a question and wait for their answer. " +
				"Use it whenever you need information, a choice or a decision from the user. " +
				"It may take a long time to come back, which is expected.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{"type": "string", "description": "The question to ask."},
					"choices": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Optional closed list of acceptable answers.",
					},
				},
				"required": []string{"prompt"},
			},
		},
	}

	for _, tool := range coreTools {
		schema := tool.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		definitions = append(definitions, map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": schema,
		})
	}
	return definitions
}

// Config renders the --mcp-config value pointing Claude Code at a Job endpoint.
func Config(endpoint string) (string, error) {
	encoded, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			ServerName: map[string]any{"type": "http", "url": endpoint},
		},
	})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
