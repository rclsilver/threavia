// Package mcpconfig defines portable MCP declarations. Credentials never travel
// in these records: references name values owned by the execution backend.
package mcpconfig

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)
var envPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type Server struct {
	ProjectIDs []string `json:"projectIds,omitempty"` // Read-only account registry metadata.
	Revision   int64    `json:"revision"`
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Transport  string   `json:"transport"`
	Command    string   `json:"command,omitempty"`
	Args       []string `json:"args,omitempty"`
	URL        string   `json:"url,omitempty"`
	// Env maps subprocess variable names to backend environment variable names.
	Env map[string]string `json:"env,omitempty"`
	// Headers maps HTTP header names to backend environment variable names.
	Headers       map[string]string `json:"headers,omitempty"`
	OAuth         bool              `json:"oauth"`
	OAuthClientID string            `json:"oauthClientId,omitempty"`
	Tools         []string          `json:"tools"`
	ReadTools     []string          `json:"readTools"`
	Resources     bool              `json:"resources"`
	Prompts       bool              `json:"prompts"`
	Enabled       bool              `json:"enabled"`
}

type Binding struct {
	ServerID string `json:"serverId"`
	Enabled  bool   `json:"enabled"`
	Required bool   `json:"required"`
}

type Effective struct {
	Server
	Required bool `json:"required"`
}

func (s Server) Validate() error {
	if !namePattern.MatchString(s.Name) || s.Name == "threavia" {
		return fmt.Errorf("name must start with a lowercase letter, contain only letters, digits and underscores, and differ from threavia")
	}
	if s.Transport != "stdio" && s.Transport != "http" {
		return fmt.Errorf("transport must be stdio or http (Streamable HTTP)")
	}
	if s.Transport == "stdio" {
		if s.Command == "" || s.URL != "" || s.OAuth || len(s.Headers) > 0 {
			return fmt.Errorf("stdio requires a command, without URL, HTTP headers or OAuth")
		}
		switch filepath.Base(s.Command) {
		case "npx":
			if !slices.Contains(s.Args, "--no-install") && !slices.Contains(s.Args, "--offline") {
				return fmt.Errorf("npx requires --no-install or --offline; install the server separately")
			}
		case "uvx":
			if !slices.Contains(s.Args, "--offline") {
				return fmt.Errorf("uvx requires --offline; install the server separately")
			}
		}
	} else {
		u, err := url.Parse(s.URL)
		if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) {
			return fmt.Errorf("HTTP requires an HTTPS URL (HTTP allowed on loopback), without userinfo, query or fragment")
		}
		if s.Command != "" || len(s.Args) > 0 || len(s.Env) > 0 {
			return fmt.Errorf("HTTP cannot contain a command, arguments or subprocess environment")
		}
	}
	if len(s.Tools) > 256 || len(s.ReadTools) > 256 || len(s.Args) > 128 {
		return fmt.Errorf("too many tools or arguments")
	}
	selected := map[string]bool{}
	for _, n := range s.Tools {
		if n == "" || len(n) > 128 || strings.ContainsAny(n, "*?\n\r") || selected[n] {
			return fmt.Errorf("tools must be unique exact names, not patterns")
		}
		selected[n] = true
	}
	for _, n := range s.ReadTools {
		if !selected[n] {
			return fmt.Errorf("readTools must be selected tools")
		}
	}
	for k, v := range s.Env {
		if !envPattern.MatchString(k) || !envPattern.MatchString(v) {
			return fmt.Errorf("environment must contain variable-name references, not values")
		}
	}
	for k, v := range s.Headers {
		if !regexp.MustCompile(`^[A-Za-z0-9-]+$`).MatchString(k) || !envPattern.MatchString(v) {
			return fmt.Errorf("headers must contain backend environment references")
		}
		if strings.EqualFold(k, "Host") {
			return fmt.Errorf("Host header cannot be overridden")
		}
	}
	return nil
}
