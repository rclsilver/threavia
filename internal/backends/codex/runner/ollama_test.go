package runner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rclsilver/threavia/internal/backends/ollama"
	contract "github.com/rclsilver/threavia/internal/backends/shared/runner"
)

func fakeOllama(t *testing.T, version string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			_, _ = w.Write([]byte(`{"version":"` + version + `"}`))
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"model":"qwen3-coder:30b","details":{"parameter_size":"30.5B","quantization_level":"Q4_K_M"}},{"model":"nomic-embed-text:latest"},{"model":"gpt-oss:20b"}]}`))
		case "/api/show":
			var body struct{ Model string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			switch body.Model {
			case "qwen3-coder:30b":
				_, _ = w.Write([]byte(`{"capabilities":["completion","tools"],"model_info":{"qwen3moe.context_length":262144}}`))
			case "gpt-oss:20b":
				// "max" is not an effort Codex knows: it must not reach its catalog.
				_, _ = w.Write([]byte(`{"capabilities":["completion","tools","thinking","vision"],"model_info":{"gptoss.context_length":16384},"thinking":{"values":["low","medium","high","max"],"default":"medium"}}`))
			default:
				_, _ = w.Write([]byte(`{"capabilities":["embedding"]}`))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func ollamaCodex(t *testing.T, url string) *Codex {
	t.Helper()
	server, err := ollama.New(url)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	catalog := filepath.Join(t.TempDir(), "models.json")
	return New(binary, nil, Options{Model: "qwen3-coder:30b", Scratch: t.TempDir(), Ollama: server, CatalogPath: catalog, ContextLength: 32768}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestOllamaCatalogListsToolModelsForCoreAndCodex(t *testing.T) {
	c := ollamaCodex(t, fakeOllama(t, "0.40.14").URL)
	catalog, err := c.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if catalog.DefaultModel != "qwen3-coder:30b" || len(catalog.Models) != 2 {
		t.Fatalf("catalog = %v", catalog)
	}
	oss, coder := catalog.Models[0], catalog.Models[1]
	if oss.Id != "gpt-oss:20b" || !slices.Equal(oss.Efforts, []string{"low", "medium", "high"}) || oss.DefaultEffort != "medium" {
		t.Errorf("gpt-oss option = %v", oss)
	}
	if coder.Name != "qwen3-coder:30b (30.5B, Q4_K_M)" || len(coder.Efforts) != 0 {
		t.Errorf("qwen3-coder option = %v", coder)
	}

	raw, err := os.ReadFile(c.options.CatalogPath)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Models) != 2 {
		t.Fatalf("Codex catalog = %s", raw)
	}
	for _, entry := range file.Models {
		// Codex refuses a catalog missing any of these.
		for _, key := range []string{"slug", "display_name", "supported_reasoning_levels", "shell_type", "visibility", "supported_in_api", "priority", "support_verbosity", "truncation_policy", "experimental_supported_tools"} {
			if _, ok := entry[key]; !ok {
				t.Errorf("%s: missing %s", entry["slug"], key)
			}
		}
		if text, _ := entry["base_instructions"].(string); text == "" {
			t.Errorf("%s: empty base instructions", entry["slug"])
		}
		if _, ok := entry["supports_search_tool"]; ok {
			t.Errorf("%s: tool search enabled, which Ollama cannot serve", entry["slug"])
		}
	}
	// The window is the server's, never more than the model's own.
	if window := file.Models[0]["context_window"]; window != float64(16384) {
		t.Errorf("gpt-oss window = %v", window)
	}
	if window := file.Models[1]["context_window"]; window != float64(32768) {
		t.Errorf("qwen3-coder window = %v", window)
	}
	if levels := file.Models[0]["supported_reasoning_levels"].([]any); len(levels) != 3 {
		t.Errorf("gpt-oss levels = %v", levels)
	}
	if modalities := file.Models[0]["input_modalities"].([]any); len(modalities) != 2 {
		t.Errorf("gpt-oss modalities = %v", modalities)
	}
}

func TestOllamaAvailabilityNeedsAServerRecentEnough(t *testing.T) {
	recent := ollamaCodex(t, fakeOllama(t, "0.40.14").URL)
	if recent.CurrentModelCatalog() != nil {
		t.Fatal("a catalog before the server was read")
	}
	if err := recent.Available(); err != nil {
		t.Fatalf("a recent server is unavailable: %v", err)
	}
	// Checking availability is what refreshes the catalog the clients see.
	if catalog := recent.CurrentModelCatalog(); catalog == nil || len(catalog.Models) != 2 {
		t.Fatalf("catalog after Available = %v", catalog)
	}
	if err := ollamaCodex(t, fakeOllama(t, "0.32.6").URL).Available(); !errors.Is(err, contract.ErrProviderUnreachable) || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("an old server = %v", err)
	}
	down := fakeOllama(t, "0.40.14")
	c := ollamaCodex(t, down.URL)
	down.Close()
	if err := c.Available(); !errors.Is(err, contract.ErrProviderUnreachable) {
		t.Fatalf("a stopped server = %v", err)
	}
}

func TestOllamaPointsCodexAtTheServerAndDropsWhatItCannotServe(t *testing.T) {
	c := ollamaCodex(t, "http://gpu.lan:11434")
	args := strings.Join(c.appServer(context.Background(), "-c", "features.hooks=true").Args, " ")
	for _, want := range []string{
		`model_providers.threavia-ollama={name="Ollama",base_url="http://gpu.lan:11434/v1",wire_api="responses"}`,
		`model_provider="threavia-ollama"`,
		`model_catalog_json="` + c.options.CatalogPath + `"`,
		`web_search="disabled"`,
		"features.multi_agent=false",
		"features.hooks=true",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("app-server args lack %s: %s", want, args)
		}
	}
	tools := c.webTools(&execution{}, "job")
	if len(tools) != 1 || tools[0].Name != "web_fetch" {
		t.Errorf("web tools = %v", tools)
	}
	if status := c.Quotas(context.Background()); status.Availability != "UNSUPPORTED" {
		t.Errorf("quotas = %+v", status)
	}
	if authenticated, known := c.Authenticated(context.Background()); !authenticated || !known {
		t.Error("Ollama reads as needing a login")
	}
	if text := instructions(contract.StartParams{}, nil, "", promptOptions{NoWebSearch: true}); strings.Contains(text, "web_search") {
		t.Error("the prompt still offers web search")
	}
}

func TestCodexWithoutOllamaKeepsOpenAI(t *testing.T) {
	c, _, _ := provider(t, "unused")
	if args := strings.Join(c.appServer(context.Background()).Args, " "); strings.Contains(args, "model_provider") {
		t.Errorf("a Codex backend was pointed elsewhere: %s", args)
	}
	if tools := c.webTools(&execution{}, "job"); len(tools) != 2 {
		t.Errorf("web tools = %d", len(tools))
	}
}

func TestOlderVersion(t *testing.T) {
	for _, tc := range []struct {
		version string
		older   bool
	}{{"0.32.6", true}, {"0.32.7", false}, {"0.40.14", false}, {"v0.33.0-rc1", false}, {"0.32", true}, {"1.0.0", false}, {"dev", false}} {
		if got := olderVersion(tc.version, ollamaMinimumVersion); got != tc.older {
			t.Errorf("olderVersion(%q) = %v", tc.version, got)
		}
	}
}
