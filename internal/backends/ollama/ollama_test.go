package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// FakeServer answers like an Ollama 0.40 with a coding model, an embedding
// model and a model with named thinking levels.
func FakeServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			_, _ = w.Write([]byte(`{"version":"0.40.14"}`))
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[
				{"name":"qwen3-coder:30b","model":"qwen3-coder:30b","details":{"family":"qwen3moe","parameter_size":"30.5B","quantization_level":"Q4_K_M"}},
				{"name":"nomic-embed-text:latest","model":"nomic-embed-text:latest","details":{"family":"nomic-bert"}},
				{"name":"gpt-oss:20b","model":"gpt-oss:20b","details":{"parameter_size":"20.9B","quantization_level":"MXFP4"}}]}`))
		case "/api/show":
			var body struct{ Model string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			switch body.Model {
			case "qwen3-coder:30b":
				_, _ = w.Write([]byte(`{"capabilities":["completion","tools"],"model_info":{"qwen3moe.context_length":262144}}`))
			case "gpt-oss:20b":
				_, _ = w.Write([]byte(`{"capabilities":["completion","tools","thinking"],"model_info":{"gptoss.context_length":131072},"thinking":{"values":["low","medium","high"],"default":"medium"}}`))
			default:
				_, _ = w.Write([]byte(`{"capabilities":["embedding"],"thinking":{"values":[false]}}`))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestNewNormalizesTheServerURL(t *testing.T) {
	for raw, want := range map[string]string{
		"":                           "http://localhost:11434",
		"http://gpu.lan:11434/":      "http://gpu.lan:11434",
		"https://ollama.example/v1":  "https://ollama.example",
		"http://proxy.lan/ollama/v1": "http://proxy.lan/ollama",
	} {
		c, err := New(raw)
		if err != nil {
			t.Fatalf("New(%q): %v", raw, err)
		}
		if c.URL() != want || c.OpenAIURL() != want+"/v1" {
			t.Errorf("New(%q) = %s, %s", raw, c.URL(), c.OpenAIURL())
		}
	}
	for _, raw := range []string{"localhost:11434", "ftp://host", "http://user:secret@host", "http://host/?q=1"} {
		if _, err := New(raw); err == nil {
			t.Errorf("New(%q) accepted an invalid URL", raw)
		}
	}
}

func TestModelsReportCapabilitiesContextAndThinkingLevels(t *testing.T) {
	c, err := New(FakeServer(t).URL)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := c.Version(context.Background()); err != nil || v != "0.40.14" {
		t.Fatalf("Version = %q, %v", v, err)
	}
	models, err := c.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 {
		t.Fatalf("models = %+v", models)
	}
	oss, embed, coder := models[0], models[1], models[2]
	if oss.Name != "gpt-oss:20b" || !oss.Agentic() || !oss.Thinking() || oss.ContextLength != 131072 ||
		!slices.Equal(oss.Levels, []string{"low", "medium", "high"}) || oss.DefaultLevel != "medium" {
		t.Errorf("gpt-oss = %+v", oss)
	}
	if embed.Agentic() || len(embed.Levels) != 0 {
		t.Errorf("an embedding model reads as agentic: %+v", embed)
	}
	if !coder.Agentic() || coder.Thinking() || coder.ContextLength != 262144 || coder.ParameterSize != "30.5B" {
		t.Errorf("qwen3-coder = %+v", coder)
	}
}

func TestAnUnreachableServerIsReported(t *testing.T) {
	server := FakeServer(t)
	c, _ := New(server.URL)
	server.Close()
	if _, err := c.Version(context.Background()); err == nil {
		t.Fatal("Version succeeded against a closed server")
	}
}
