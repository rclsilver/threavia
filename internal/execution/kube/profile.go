package kube

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var dnsName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func validName(s string) bool { return dnsName.MatchString(s) }

const labelController = "execution.threavia.io/controller"
const labelExecution = "execution.threavia.io/id"
const labelProfile = "execution.threavia.io/profile"

type Mount struct {
	Name    string `json:"name"`
	Claim   string `json:"claim"`
	Path    string `json:"path"`
	SubPath string `json:"subPath,omitempty"`
	// Roles are workspace, tools, provider, scratch, skills or credential.
	Role     string `json:"role"`
	ReadOnly bool   `json:"readOnly,omitempty"`
}
type Env struct{ Name, Value, SecretName, SecretKey string }
type Profile struct {
	Name             string            `json:"name"`
	Provider         string            `json:"provider"`
	Image            string            `json:"image"`
	WorkerBinary     string            `json:"workerBinary"`
	ProviderBinary   string            `json:"providerBinary"`
	WorkingDirectory string            `json:"workingDirectory"`
	Home             string            `json:"home"`
	ServiceAccount   string            `json:"serviceAccount"`
	UID              int64             `json:"uid"`
	GID              int64             `json:"gid"`
	Mounts           []Mount           `json:"mounts"`
	ProviderEnv      []Env             `json:"providerEnv,omitempty"`
	MonitorEnv       []Env             `json:"monitorEnv,omitempty"`
	NodeSelector     map[string]string `json:"nodeSelector,omitempty"`
	Tolerations      []map[string]any  `json:"tolerations,omitempty"`
	Resources        map[string]any    `json:"resources"`
	ImagePullSecrets []string          `json:"imagePullSecrets,omitempty"`
	SeccompProfile   string            `json:"seccompProfile,omitempty"`
	AppArmorProfile  string            `json:"appArmorProfile,omitempty"`
	// ProviderEgress is administrator-controlled NetworkPolicy egress. Monitor
	// workloads never inherit the provider's network or authentication exceptions.
	ProviderEgress []map[string]any `json:"providerEgress,omitempty"`
}
type Request struct {
	Scratch                                    bool
	Skills                                     bool
	ID, RunID, JobID, Kind, Directory, Command string
	Writable, Network                          bool
	ExpiresAt                                  time.Time
}
type Launch struct {
	Controller, Namespace, PublicURL, SessionID, Token, CASecretName string
	Profile                                                          Profile
	Request                                                          Request
}

func (p Profile) Validate() error {
	if !validName(p.Name) || p.Provider != "codex" && p.Provider != "claude" {
		return errors.New("profile name and provider are invalid")
	}
	if p.Image == "" || strings.ContainsAny(p.Image, " \n\r") || !filepath.IsAbs(p.WorkerBinary) || p.ProviderBinary == "" {
		return errors.New("profile requires an image and fixed worker/provider executables")
	}
	if !cleanAbsolute(p.Home) || !cleanAbsolute(p.WorkingDirectory) || p.UID <= 0 || p.GID <= 0 || !validName(p.ServiceAccount) {
		return errors.New("profile requires a non-root identity, home, working directory and service account")
	}
	if len(p.Mounts) == 0 || len(p.Mounts) > 32 {
		return errors.New("profile must declare 1–32 volume mounts")
	}
	names := map[string]bool{}
	paths := map[string]bool{}
	workspace := false
	for _, m := range p.Mounts {
		if m.Name == "temporary" || m.Name == "private-home" || m.Name == "executor-ca" {
			return errors.New("profile mount name is reserved")
		}
		if !validName(m.Name) || names[m.Name] || !validName(m.Claim) || !cleanAbsolute(m.Path) || paths[m.Path] || !safeSubPath(m.SubPath) {
			return fmt.Errorf("invalid or duplicate profile mount %q", m.Name)
		}
		names[m.Name] = true
		paths[m.Path] = true
		switch m.Role {
		case "workspace":
			workspace = workspace || within(m.Path, p.WorkingDirectory)
		case "tools", "credential":
			if !m.ReadOnly {
				return fmt.Errorf("%s mounts must be read-only", m.Role)
			}
		case "provider", "scratch", "skills":
		default:
			return fmt.Errorf("unsupported mount role %q", m.Role)
		}
		for _, forbidden := range []string{"/", "/proc", "/sys", "/dev", "/tmp", "/var/tmp", "/var/run/threavia-execution", "/var/run/secrets/kubernetes.io/serviceaccount"} {
			if m.Path == forbidden || forbidden != "/" && within(forbidden, m.Path) {
				return fmt.Errorf("mount overlays protected path %s", m.Path)
			}
		}
	}
	if !workspace {
		return errors.New("working directory must belong to a workspace volume")
	}
	if len(p.Resources) == 0 {
		return errors.New("execution resource requests and limits are required")
	}
	for _, items := range [][]Env{p.ProviderEnv, p.MonitorEnv} {
		seen := map[string]bool{}
		for _, e := range items {
			if e.Name == "" || strings.ContainsAny(e.Name, "=\x00") || strings.HasPrefix(e.Name, "THREAVIA_") || strings.HasPrefix(e.Name, "KUBERNETES_") || e.Name == "HOME" || seen[e.Name] {
				return fmt.Errorf("reserved or invalid profile environment %q", e.Name)
			}
			if (e.SecretName == "") != (e.SecretKey == "") || e.SecretName != "" && e.Value != "" {
				return fmt.Errorf("invalid secret environment %q", e.Name)
			}
			seen[e.Name] = true
		}
	}
	return nil
}
func cleanAbsolute(s string) bool {
	return filepath.IsAbs(s) && filepath.Clean(s) == s && !strings.ContainsRune(s, 0)
}
func safeSubPath(s string) bool {
	return s == "" || !filepath.IsAbs(s) && filepath.Clean(s) == s && s != "." && s != ".." && !strings.HasPrefix(s, "../") && !strings.ContainsRune(s, 0)
}
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel)
}
func (r Request) Validate(p Profile, now time.Time) error {
	if _, err := uuid.Parse(r.ID); err != nil {
		return errors.New("execution ID must be a UUID")
	}
	if !cleanAbsolute(r.Directory) || !r.ExpiresAt.After(now) || r.ExpiresAt.After(now.Add(7*24*time.Hour)) {
		return errors.New("execution needs an approved absolute directory and a deadline within seven days")
	}
	workspace := false
	for _, m := range p.Mounts {
		workspace = workspace || m.Role == "workspace" && within(m.Path, r.Directory)
	}
	if !workspace {
		return errors.New("execution directory is outside the profile workspace")
	}
	switch r.Kind {
	case "job":
		if _, err := uuid.Parse(r.RunID); err != nil {
			return errors.New("invalid run ID")
		}
		if _, err := uuid.Parse(r.JobID); err != nil {
			return errors.New("invalid job ID")
		}
		if r.Command != "" {
			return errors.New("job command is selected by the worker image")
		}
	case "probe":
		if r.RunID != "" || r.JobID != "" || r.Command != "" || r.Writable || !r.Network || r.Skills || r.Scratch {
			return errors.New("invalid provider probe")
		}
	case "monitor":
		if r.Skills || r.Scratch {
			return errors.New("monitors cannot mount Job private data")
		}
		if _, err := uuid.Parse(r.RunID); err != nil {
			return errors.New("invalid monitor run ID")
		}
		if _, err := uuid.Parse(r.JobID); err != nil {
			return errors.New("invalid monitor origin job ID")
		}
		if len(r.Command) == 0 || len(r.Command) > 16384 {
			return errors.New("monitor command must contain 1–16384 bytes")
		}
		if r.ExpiresAt.After(now.Add(24 * time.Hour)) {
			return errors.New("monitor exceeds the 24-hour duration limit")
		}
	default:
		return errors.New("unsupported execution kind")
	}
	return nil
}
func PodName(controller, profile, id string) string {
	h := sha256.Sum256([]byte(controller + "\x00" + profile + "\x00" + id))
	return "threavia-exec-" + hex.EncodeToString(h[:16])
}
