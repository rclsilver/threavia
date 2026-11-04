// Package ollama reads what an Ollama server offers: whether it answers, and
// which of its models can drive an agent. It never runs inference: Codex CLI
// talks to the model, this package only describes the server to Core.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
)

// DefaultURL is where a local Ollama listens unless told otherwise.
const DefaultURL = "http://localhost:11434"

// Client reads one Ollama server.
type Client struct {
	base *url.URL
	http *http.Client
}

// New validates the server URL. Only the scheme, host and an optional path
// prefix are kept: the API paths are added here.
func New(raw string) (*Client, error) {
	if strings.TrimSpace(raw) == "" {
		raw = DefaultURL
	}
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("ollama URL %q: expected http(s)://host[:port]", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, fmt.Errorf("ollama URL %q: no credentials, query or fragment", raw)
	}
	// The OpenAI-compatible prefix belongs to Codex's configuration, not here.
	u.Path = strings.TrimSuffix(u.Path, "/v1")
	return &Client{base: u, http: &http.Client{Timeout: 30 * time.Second}}, nil
}

// URL is the server root, without the OpenAI-compatible /v1 prefix.
func (c *Client) URL() string { return c.base.String() }

// OpenAIURL is the OpenAI-compatible endpoint Codex sends its requests to.
func (c *Client) OpenAIURL() string { return c.base.JoinPath("v1").String() }

// Version answers whether the server is up, and which release it runs.
func (c *Client) Version(ctx context.Context) (string, error) {
	var v struct {
		Version string `json:"version"`
	}
	if err := c.call(ctx, http.MethodGet, "api/version", nil, &v); err != nil {
		return "", err
	}
	if v.Version == "" {
		return "", errors.New("ollama reported no version")
	}
	return v.Version, nil
}

// Model is one installed model and what the server says it can do.
type Model struct {
	Name          string
	Family        string
	ParameterSize string
	Quantization  string
	Capabilities  []string
	ContextLength int64
	// Levels are the named thinking levels the model accepts (low, medium,
	// high…), and DefaultLevel the one it uses when none is asked for. Empty
	// for a model whose thinking is only on or off.
	Levels       []string
	DefaultLevel string
}

// Agentic reports whether the model can call tools: Codex does nothing useful
// with a model that cannot.
func (m Model) Agentic() bool { return slices.Contains(m.Capabilities, "tools") }

// Thinking reports whether the model reasons before answering.
func (m Model) Thinking() bool { return slices.Contains(m.Capabilities, "thinking") }

// Models lists the installed models with their capabilities. A model whose
// details cannot be read is kept without capabilities rather than hidden, so
// the caller decides what an unknown model is worth.
func (c *Client) Models(ctx context.Context) ([]Model, error) {
	var tags struct {
		Models []struct {
			Name    string `json:"name"`
			Model   string `json:"model"`
			Details struct {
				Family            string `json:"family"`
				ParameterSize     string `json:"parameter_size"`
				QuantizationLevel string `json:"quantization_level"`
			} `json:"details"`
		} `json:"models"`
	}
	if err := c.call(ctx, http.MethodGet, "api/tags", nil, &tags); err != nil {
		return nil, err
	}
	models := make([]Model, 0, len(tags.Models))
	for _, tag := range tags.Models {
		name := tag.Model
		if name == "" {
			name = tag.Name
		}
		if name == "" {
			continue
		}
		m := Model{Name: name, Family: tag.Details.Family, ParameterSize: tag.Details.ParameterSize, Quantization: tag.Details.QuantizationLevel}
		var show struct {
			Capabilities []string       `json:"capabilities"`
			ModelInfo    map[string]any `json:"model_info"`
			// Values are booleans (on/off) or model-defined level names.
			Thinking struct {
				Values  []any `json:"values"`
				Default any   `json:"default"`
			} `json:"thinking"`
		}
		if err := c.call(ctx, http.MethodPost, "api/show", map[string]any{"model": name}, &show); err == nil {
			m.Capabilities = show.Capabilities
			for key, value := range show.ModelInfo {
				if n, ok := value.(float64); ok && strings.HasSuffix(key, ".context_length") {
					m.ContextLength = int64(n)
				}
			}
			for _, value := range show.Thinking.Values {
				if level, ok := value.(string); ok && level != "" {
					m.Levels = append(m.Levels, level)
				}
			}
			if level, ok := show.Thinking.Default.(string); ok && slices.Contains(m.Levels, level) {
				m.DefaultLevel = level
			}
		} else if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		models = append(models, m)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })
	return models, nil
}

func (c *Client) call(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.JoinPath(path).String(), reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("ollama at %s is unreachable: %w", c.base.Host, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(detail, &e) == nil && e.Error != "" {
			return fmt.Errorf("ollama %s: %s", path, e.Error)
		}
		return fmt.Errorf("ollama %s: HTTP %d", path, res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(out)
}
