package externalmcp

import (
	"encoding/json"
	"fmt"
	"github.com/pelletier/go-toml/v2"
	"github.com/rclsilver/threavia/pkg/mcpconfig"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Import only previews declarations; native files and credentials are unchanged.
func Import(source string) (map[string]any, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot resolve backend home")
	}
	var path string
	var native map[string]map[string]any
	switch source {
	case "codex":
		root := os.Getenv("CODEX_HOME")
		if root == "" {
			root = filepath.Join(home, ".codex")
		}
		path = filepath.Join(root, "config.toml")
	case "claude":
		path = filepath.Join(home, ".claude.json")
	default:
		return nil, fmt.Errorf("import source must be codex or claude")
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() > 1<<20 {
		return nil, fmt.Errorf("native configuration unavailable or larger than 1 MiB")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read native configuration")
	}
	if source == "codex" {
		var v struct {
			Servers map[string]map[string]any `toml:"mcp_servers"`
		}
		err = toml.Unmarshal(raw, &v)
		native = v.Servers
	} else {
		var v struct {
			Servers map[string]map[string]any `json:"mcpServers"`
		}
		err = json.Unmarshal(raw, &v)
		native = v.Servers
	}
	if err != nil {
		return nil, fmt.Errorf("cannot parse native MCP declarations")
	}
	return ImportDeclarations(native), nil
}

var refPattern = regexp.MustCompile(`^\$\{(?:env:)?([A-Za-z_][A-Za-z0-9_]*)\}$`)

func ImportDeclarations(native map[string]map[string]any) map[string]any {
	out := []any{}
	names := make([]string, 0, len(native))
	for n := range native {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		n := native[name]
		v := mcpconfig.Server{Name: name, Transport: "stdio", Tools: []string{}, ReadTools: []string{}, Env: map[string]string{}, Headers: map[string]string{}}
		notes := []string{"Preview only. Select tools and enable explicitly before use."}
		v.Command, _ = n["command"].(string)
		if u, ok := n["url"].(string); ok && u != "" {
			v.Transport = "http"
			v.URL = u
		}
		// Arbitrary arguments can contain secrets: never return them from native files.
		if args, ok := n["args"]; ok && args != nil {
			notes = append(notes, "Arguments omitted to avoid importing inline secrets. Re-enter non-secret arguments.")
		}
		for _, key := range []string{"env", "headers", "http_headers"} {
			if values, ok := n[key].(map[string]any); ok {
				for k, val := range values {
					s, _ := val.(string)
					match := refPattern.FindStringSubmatch(s)
					if len(match) == 2 {
						if key == "env" {
							v.Env[k] = match[1]
						} else {
							v.Headers[k] = match[1]
						}
					} else {
						notes = append(notes, "Inline value omitted for "+k+"; configure a backend reference.")
					}
				}
			}
		}
		if refs, ok := n["env_http_headers"].(map[string]any); ok {
			for k, val := range refs {
				if ref, ok := val.(string); ok {
					v.Headers[k] = ref
				}
			}
		}
		if ref, ok := n["bearer_token_env_var"].(string); ok && ref != "" {
			notes = append(notes, "Configure an Authorization header environment reference including the Bearer prefix.")
		}
		if strings.ContainsAny(v.URL, "?#@") {
			v.URL = ""
			notes = append(notes, "URL omitted because it may contain credentials; enter a clean endpoint URL.")
		}
		out = append(out, map[string]any{"declaration": v, "notes": notes})
	}
	return map[string]any{"servers": out, "preview": true}
}
