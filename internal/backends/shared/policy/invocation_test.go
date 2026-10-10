package policy

import (
	"os"
	"path/filepath"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

func TestInvocationRules(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name, tool, match string
		cap               backendv1.PermissionCapability
		input             map[string]any
	}{
		{"shell", "Bash", "go test *", backendv1.PermissionCapability_PERMISSION_CAPABILITY_SHELL, map[string]any{"command": "go test ./..."}},
		{"read", "Read", "secrets/**", backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_READ, map[string]any{"file_path": "secrets/key"}},
		{"write", "Edit", "src/*", backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_WRITE, map[string]any{"file_path": "src/main.go"}},
		{"network", "WebFetch", "*.example.com", backendv1.PermissionCapability_PERMISSION_CAPABILITY_NETWORK, map[string]any{"url": "https://api.example.com/v1"}},
		{"commit", "Bash", "", backendv1.PermissionCapability_PERMISSION_CAPABILITY_GIT_COMMIT, map[string]any{"command": `git -C "work tree" commit -m ok`}},
		{"push", "Bash", "", backendv1.PermissionCapability_PERMISSION_CAPABILITY_GIT_PUSH, map[string]any{"command": "git -C . push origin main"}},
		{"tool", "mcp__threavia__memory_write", "mcp__threavia__memory_*", backendv1.PermissionCapability_PERMISSION_CAPABILITY_TOOL, map[string]any{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, effect := range []backendv1.PermissionEffect{backendv1.PermissionEffect_PERMISSION_EFFECT_ALLOW, backendv1.PermissionEffect_PERMISSION_EFFECT_ASK, backendv1.PermissionEffect_PERMISSION_EFFECT_DENY} {
				p := Policy{Mode: backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE, AllowFilesystemWrite: true, AllowNetwork: true, AllowGitCommit: true, AllowGitPush: true,
					Rules: []Rule{{Effect: effect, Capability: tt.cap, Match: tt.match}}}
				want := map[backendv1.PermissionEffect]Verdict{backendv1.PermissionEffect_PERMISSION_EFFECT_ALLOW: Allow, backendv1.PermissionEffect_PERMISSION_EFFECT_ASK: Ask, backendv1.PermissionEffect_PERMISSION_EFFECT_DENY: Deny}[effect]
				if got := p.EvaluateInvocation(tt.tool, tt.input, root); got.Verdict != want {
					t.Fatalf("%s: %+v, want %v", effect, got, want)
				}
			}
		})
	}
}

func TestInvocationPrecedenceAndScope(t *testing.T) {
	p := Policy{Mode: backendv1.ExecutionMode_EXECUTION_MODE_GUARDED, AllowFilesystemWrite: true, Rules: []Rule{
		{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_ALLOW, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_SHELL, Match: "go test *"},
		{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_ASK, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_SHELL, Match: "go test -race *"},
		{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_DENY, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_SHELL, Match: "go test *secrets*"},
	}}
	for _, tt := range []struct {
		command string
		want    Verdict
	}{
		{"go test ./...", Allow}, {"go test -race ./...", Ask}, {"go test -race ./secrets/...", Deny},
		{"go test ./... && rm -rf src", Ask}, {"go test $(touch x)", Ask}, {"go test ./... > result", Ask},
		{`go test "a|b"`, Allow},
	} {
		if d := p.EvaluateInvocation("Bash", map[string]any{"command": tt.command}, t.TempDir()); d.Verdict != tt.want {
			t.Errorf("%q: %+v, want %v", tt.command, d, tt.want)
		}
	}
	p.AllowFilesystemWrite = false
	p.Rules = []Rule{{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_ALLOW, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_WRITE, Match: "*"}}
	if p.EvaluateInvocation("Edit", map[string]any{"file_path": "a.go"}, t.TempDir()).Verdict != Deny {
		t.Fatal("allow overrode the capability switch")
	}
}

func TestFileRulesResolvePathsAndShellAccess(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "secrets"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "secrets"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	p := Policy{Mode: backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS, AllowFilesystemWrite: true, Rules: []Rule{
		{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_DENY, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_READ, Match: "secrets/**"},
		{Effect: backendv1.PermissionEffect_PERMISSION_EFFECT_DENY, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_WRITE, Match: "locked/*"},
	}}
	for _, path := range []string{"secrets/key", "src/../secrets/key", filepath.Join(root, "secrets", "key"), "alias/key"} {
		if p.EvaluateInvocation("Read", map[string]any{"file_path": path}, root).Verdict != Deny {
			t.Errorf("read escaped through %q", path)
		}
	}
	for _, command := range []string{`cat "secrets/key"`, "cd secrets && cat key", "rg password .", "echo x>locked/result", "touch locked/result"} {
		if p.EvaluateInvocation("Bash", map[string]any{"command": command}, root).Verdict != Deny {
			t.Errorf("file rule missed %q", command)
		}
	}
	if p.EvaluateInvocation("Edit", map[string]any{"file_path": "src/main.go"}, root).Verdict != Allow {
		t.Fatal("targeted write denial blocked an unrelated file")
	}
	aliasRoot := filepath.Join(t.TempDir(), "project")
	if err := os.Symlink(root, aliasRoot); err != nil {
		t.Fatal(err)
	}
	if _, refused := p.RefusesReading(filepath.Join(root, "secrets", "key"), aliasRoot); !refused {
		t.Fatal("publication bypassed a relative refusal through a symlinked workspace root")
	}
}
