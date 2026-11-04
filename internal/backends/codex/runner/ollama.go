package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/ollama"
	contract "github.com/rclsilver/threavia/internal/backends/shared/runner"
)

// ollamaProviderID names the model provider Codex is pointed at. Codex reserves
// the built-in "ollama" id and refuses to let configuration move it off
// localhost, so a backend serving a remote Ollama declares its own.
const ollamaProviderID = "threavia-ollama"

// ollamaMinimumVersion is the first release that expands Codex's namespaced
// MCP tools and splits their calls back: below it no Threavia tool is callable.
const ollamaMinimumVersion = "0.32.7"

// codexEfforts are the reasoning efforts Codex's model catalog accepts. An
// Ollama level outside them would make Codex reject the whole catalog.
var codexEfforts = []string{"minimal", "low", "medium", "high", "xhigh"}

// ollamaInstructions are the base instructions of every Ollama model. Codex
// requires some; the Threavia rules arrive separately as developer
// instructions. Kept short: a local model's context is small and every token
// here is paid on each request.
const ollamaInstructions = `You are a coding agent running in Codex CLI, on the user's machine. You work in the user's repositories by running shell commands and reading their output, and you report back concisely.

How you work:
- Run commands with the shell tool; read files with cat, sed -n, rg or ls before changing them.
- Edit files from the shell (for example a heredoc, or sed for a precise substitution), then read back what changed.
- Keep going until the request is resolved; do not stop to ask what you can find out yourself.
- Prefer small, focused changes that match the surrounding code. Do not reformat unrelated code.
- Verify your work with the project's tests or build when they exist.
- When a command is refused, do not retry it in another form: explain what you needed and why.

When you answer, say what you did and what remains, in a few sentences. Quote file paths and commands exactly.`

// ollamaArgs point Codex at the Ollama server and turn off what Ollama cannot
// serve: OpenAI's hosted web search, and sub-agents, which Codex does not run
// reliably on Ollama.
func (c *Codex) ollamaArgs() []string {
	if c.options.Ollama == nil {
		return nil
	}
	quote := func(s string) string { raw, _ := json.Marshal(s); return string(raw) }
	return []string{
		"-c", "model_providers." + ollamaProviderID + `={name="Ollama",base_url=` + quote(c.options.Ollama.OpenAIURL()) + `,wire_api="responses"}`,
		"-c", "model_provider=" + quote(ollamaProviderID),
		"-c", "model_catalog_json=" + quote(c.options.CatalogPath),
		"-c", `web_search="disabled"`,
		"-c", "features.multi_agent=false",
	}
}

// ollamaAvailable checks the server answers, runs a release Codex can work
// with, and offers at least one model. It refreshes the model file Codex reads,
// so a server that was down at startup serves Jobs once it is back.
func (c *Codex) ollamaAvailable() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.ollamaCatalog(ctx); err != nil {
		return fmt.Errorf("%w: %v", contract.ErrProviderUnreachable, err)
	}
	return nil
}

// ollamaCatalog reads the server's models, writes the catalog Codex loads and
// returns the one Core offers to clients. Only models able to call tools are
// listed: Codex does nothing useful with the others.
func (c *Codex) ollamaCatalog(ctx context.Context) (*backendv1.ModelCatalog, error) {
	version, err := c.options.Ollama.Version(ctx)
	if err != nil {
		return nil, err
	}
	if olderVersion(version, ollamaMinimumVersion) {
		return nil, fmt.Errorf("ollama %s is too old: Codex's MCP tools need %s or later", version, ollamaMinimumVersion)
	}
	models, err := c.options.Ollama.Models(ctx)
	if err != nil {
		return nil, err
	}
	catalog := &backendv1.ModelCatalog{DefaultModel: c.options.Model, DefaultEffort: c.options.ReasoningEffort}
	var entries []map[string]any
	for _, m := range models {
		if !m.Agentic() {
			continue
		}
		levels := []any{}
		var efforts []string
		for _, level := range m.Levels {
			if slices.Contains(codexEfforts, level) {
				efforts = append(efforts, level)
				levels = append(levels, map[string]any{"effort": level, "description": "Thinking level " + level})
			}
		}
		window := m.ContextLength
		if limit := int64(c.options.ContextLength); limit > 0 && (window == 0 || limit < window) {
			window = limit
		}
		entry := map[string]any{
			"slug": m.Name, "display_name": m.Name,
			"shell_type": "default", "visibility": "list", "supported_in_api": true, "priority": 0,
			"supported_reasoning_levels": levels, "support_verbosity": false,
			"truncation_policy":            map[string]any{"mode": "bytes", "limit": 10000},
			"experimental_supported_tools": []any{}, "supports_parallel_tool_calls": false,
			"input_modalities": inputModalities(m), "base_instructions": ollamaInstructions,
		}
		if window > 0 {
			entry["context_window"] = window
		}
		option := &backendv1.ModelOption{Id: m.Name, Name: displayName(m), Efforts: efforts}
		if slices.Contains(efforts, m.DefaultLevel) {
			entry["default_reasoning_level"] = m.DefaultLevel
			option.DefaultEffort = m.DefaultLevel
		}
		entries = append(entries, entry)
		catalog.Models = append(catalog.Models, option)
	}
	if len(entries) == 0 {
		return nil, errors.New("ollama has no installed model that supports tool calling")
	}
	if !slices.ContainsFunc(catalog.Models, func(m *backendv1.ModelOption) bool { return m.Id == catalog.DefaultModel }) {
		catalog.DefaultModel = catalog.Models[0].Id
	}
	if err := writeCatalog(c.options.CatalogPath, map[string]any{"models": entries}); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.catalog = catalog
	c.mu.Unlock()
	return catalog, nil
}

// CurrentModelCatalog is the catalog last read from Ollama, so that a model
// pulled or removed while the backend runs reaches the clients. Nil for a
// Codex backend on OpenAI, whose catalog is read once at startup.
func (c *Codex) CurrentModelCatalog() *backendv1.ModelCatalog {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.catalog
}

func inputModalities(m ollama.Model) []string {
	if slices.Contains(m.Capabilities, "vision") {
		return []string{"text", "image"}
	}
	return []string{"text"}
}

func displayName(m ollama.Model) string {
	var details []string
	if m.ParameterSize != "" {
		details = append(details, m.ParameterSize)
	}
	if m.Quantization != "" {
		details = append(details, m.Quantization)
	}
	if len(details) == 0 {
		return m.Name
	}
	return m.Name + " (" + strings.Join(details, ", ") + ")"
}

// writeCatalog replaces the file atomically: a Job starting meanwhile reads
// either the previous catalog or the new one, never half of it.
func writeCatalog(path string, catalog any) error {
	if path == "" {
		return errors.New("no path for the Codex model catalog")
	}
	raw, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return err
	}
	if current, err := os.ReadFile(path); err == nil && string(current) == string(raw) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".models-*.json")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// olderVersion compares dotted release numbers; anything unparsable (a
// development build) is taken as recent enough.
func olderVersion(version, minimum string) bool {
	parse := func(s string) []int {
		s, _, _ = strings.Cut(strings.TrimPrefix(s, "v"), "-")
		var parts []int
		for _, field := range strings.Split(s, ".") {
			var n int
			if _, err := fmt.Sscanf(field, "%d", &n); err != nil {
				return nil
			}
			parts = append(parts, n)
		}
		return parts
	}
	have, want := parse(version), parse(minimum)
	if have == nil {
		return false
	}
	for i := range want {
		if i >= len(have) {
			return true
		}
		if have[i] != want[i] {
			return have[i] < want[i]
		}
	}
	return false
}
