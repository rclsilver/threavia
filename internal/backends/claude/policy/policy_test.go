package policy_test

import (
	"strings"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/claude/policy"
)

func bash(cmd string) map[string]any { return map[string]any{"command": cmd} }

func permissive(mode backendv1.ExecutionMode) policy.Policy {
	return policy.Policy{
		Mode: mode, AllowFilesystemWrite: true, AllowGitCommit: true,
		AllowGitPush: true, AllowNetwork: true,
	}
}

// TestForbiddenCapabilitiesAreRefusedNotAsked pins the sentence section 17 is
// built around: a policy forbidding a push rejects the push, rather than asking
// the model not to.
func TestForbiddenCapabilitiesAreRefusedNotAsked(t *testing.T) {
	t.Parallel()

	p := permissive(backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS)
	p.AllowGitPush = false

	cases := []string{
		"git push",
		"git push origin master",
		"git -C /srv/app push",
		"make build && git push",
		"echo ok; git push --force",
	}
	for _, cmd := range cases {
		decision := p.Evaluate("Bash", bash(cmd))
		if decision.Verdict != policy.Deny {
			t.Errorf("%q = %v, want Deny", cmd, decision.Verdict)
		}
		if decision.Reason == "" {
			t.Errorf("%q was refused without telling the agent why", cmd)
		}
	}

	// A command that merely mentions the word is not a push.
	if d := p.Evaluate("Bash", bash("echo 'remember to push later'")); d.Verdict == policy.Deny {
		t.Error("a mention of pushing is not a push")
	}
	// And the capability it still has works.
	if d := p.Evaluate("Bash", bash("git commit -m ok")); d.Verdict != policy.Allow {
		t.Errorf("committing is allowed by this policy, got %v", d.Verdict)
	}
}

// TestAutonomousIsStillBounded pins that AUTONOMOUS is not permission to do
// anything: it stops asking, it does not stop refusing.
func TestAutonomousIsStillBounded(t *testing.T) {
	t.Parallel()

	p := policy.Policy{Mode: backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS}

	for tool, input := range map[string]map[string]any{
		"Write":    {"file_path": "/tmp/x"},
		"WebFetch": {"url": "https://example.com"},
	} {
		if d := p.Evaluate(tool, input); d.Verdict != policy.Deny {
			t.Errorf("%s under a policy allowing nothing = %v, want Deny", tool, d.Verdict)
		}
	}

	// What it does permit proceeds without asking.
	permissiveAuto := permissive(backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS)
	if d := permissiveAuto.Evaluate("Write", map[string]any{}); d.Verdict != policy.Allow {
		t.Errorf("autonomous write = %v, want Allow", d.Verdict)
	}
}

// TestModesDifferOnReading pins what separates the three modes: who gets asked
// about an action that only observes.
func TestModesDifferOnReading(t *testing.T) {
	t.Parallel()

	cases := map[backendv1.ExecutionMode]policy.Verdict{
		backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE: policy.Ask,
		backendv1.ExecutionMode_EXECUTION_MODE_GUARDED:     policy.Allow,
		backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS:  policy.Allow,
	}
	for mode, want := range cases {
		p := permissive(mode)
		if d := p.Evaluate("Read", map[string]any{}); d.Verdict != want {
			t.Errorf("reading under %s = %v, want %v", mode, d.Verdict, want)
		}
		if d := p.Evaluate("Bash", bash("git status")); d.Verdict != want {
			t.Errorf("git status under %s = %v, want %v", mode, d.Verdict, want)
		}
	}

	// Writing still goes to the user under GUARDED: that is the point of the
	// mode.
	guarded := permissive(backendv1.ExecutionMode_EXECUTION_MODE_GUARDED)
	if d := guarded.Evaluate("Write", map[string]any{}); d.Verdict != policy.Ask {
		t.Errorf("guarded write = %v, want Ask", d.Verdict)
	}
	if d := guarded.Evaluate("Bash", bash("rm -rf build")); d.Verdict != policy.Ask {
		t.Errorf("guarded destructive command = %v, want Ask", d.Verdict)
	}
}

// TestUnknownThingsAreTreatedAsMutating pins the direction the gate errs in: a
// capability nobody classified is not waved through.
func TestUnknownThingsAreTreatedAsMutating(t *testing.T) {
	t.Parallel()

	guarded := permissive(backendv1.ExecutionMode_EXECUTION_MODE_GUARDED)
	if d := guarded.Evaluate("SomeToolAddedLater", map[string]any{}); d.Verdict != policy.Ask {
		t.Errorf("an unclassified tool = %v, want Ask", d.Verdict)
	}
	if d := guarded.Evaluate("Bash", bash("./deploy.sh")); d.Verdict != policy.Ask {
		t.Errorf("an unrecognised command = %v, want Ask", d.Verdict)
	}
}

// TestAbsentPolicyIsTheRestrainedOne pins that a Job arriving without a policy
// gets the careful behaviour, never the permissive one.
func TestAbsentPolicyIsTheRestrainedOne(t *testing.T) {
	t.Parallel()

	p := policy.From(nil)
	if p.Mode != backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE {
		t.Fatalf("mode = %v, want INTERACTIVE", p.Mode)
	}
	for _, tool := range []string{"Write", "WebFetch"} {
		if d := p.Evaluate(tool, map[string]any{}); d.Verdict != policy.Deny {
			t.Errorf("%s with no policy = %v, want Deny", tool, d.Verdict)
		}
	}
	if d := p.Evaluate("Read", map[string]any{}); d.Verdict != policy.Ask {
		t.Errorf("reading with no policy = %v, want Ask", d.Verdict)
	}
}

// TestNetworkCommandsFollowTheNetworkFlag pins that the flag covers the shell
// too, not only the dedicated tools.
func TestNetworkCommandsFollowTheNetworkFlag(t *testing.T) {
	t.Parallel()

	p := permissive(backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS)
	p.AllowNetwork = false

	for _, cmd := range []string{"curl https://example.com", "wget http://x", "git clone https://x", "ssh host"} {
		if d := p.Evaluate("Bash", bash(cmd)); d.Verdict != policy.Deny {
			t.Errorf("%q = %v, want Deny", cmd, d.Verdict)
		}
	}
	if d := p.Evaluate("Bash", bash("ls -la")); d.Verdict == policy.Deny {
		t.Error("a local command is not network access")
	}
}

// TestGuardedLetsReadingCommandsThrough pins the commands an agent runs to look
// around. GUARDED asked for every one of them when they began with an export or
// went through a pipe, which left the mode indistinguishable from INTERACTIVE.
func TestGuardedLetsReadingCommandsThrough(t *testing.T) {
	t.Parallel()

	guarded := permissive(backendv1.ExecutionMode_EXECUTION_MODE_GUARDED)
	for _, cmd := range []string{
		"export PATH=/run/current-system/sw/bin:$PATH && git grep -n foo -- internal",
		"cd /srv/app && git log --oneline -5",
		"GOFLAGS=-mod=mod go vet ./...",
		"grep -rn TODO internal 2>/dev/null | sort | uniq -c | head",
		"go test ./... 2>&1 | tail -3 && false || true; ls",
		"git branch",
		"git branch -a",
		"git remote -v",
		"env",
		"go env GOPATH",
		"jq .version package.json",
	} {
		// The go test line is not one: it builds and runs code.
		want := policy.Allow
		if strings.HasPrefix(cmd, "go test") {
			want = policy.Ask
		}
		if d := guarded.Evaluate("Bash", bash(cmd)); d.Verdict != want {
			t.Errorf("%q = %v, want %v", cmd, d.Verdict, want)
		}
	}
}

// TestReadingCommandsThatWriteAreStillAsked pins the other half: a command that
// starts like a read but writes, or runs something else, is not one.
func TestReadingCommandsThatWriteAreStillAsked(t *testing.T) {
	t.Parallel()

	guarded := permissive(backendv1.ExecutionMode_EXECUTION_MODE_GUARDED)
	for _, cmd := range []string{
		"cat a > b",
		"echo secret >> ~/.bashrc",
		"echo $(rm -rf build)",
		"echo `rm -rf build`",
		"env rm -rf build",
		"export X=1 && rm -rf build",
		"find . -name '*.tmp' -delete",
		"find . -exec rm {} ;",
		"rg --pre ./run.sh foo",
		"git branch -D feature",
		"git remote add origin x",
		"git -c core.pager='rm -rf build' log",
		"git diff --output=patch",
		"go env -w GOFLAGS=x",
		"sort -o out.txt in.txt",
		"export X=1",
		"cd /tmp",
		"$CMD status",
	} {
		if d := guarded.Evaluate("Bash", bash(cmd)); d.Verdict != policy.Ask {
			t.Errorf("%q = %v, want Ask", cmd, d.Verdict)
		}
	}
}
