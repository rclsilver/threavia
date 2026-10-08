package policy_test

import (
	"encoding/json"
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

// TestOnlyAutonomousAnswersForTheUser pins what separates the modes at the
// gate, now that the provider decides what never reaches it.
//
// Anything arriving here is something the provider did not already settle, so
// the only question left is who answers. AUTONOMOUS answers; the other two put
// it to the person. What GUARDED lets through without asking at all is the
// provider's read-only set, and that is pinned on the translation instead —
// see TestModesAreTranslatedNotReimplemented.
func TestOnlyAutonomousAnswersForTheUser(t *testing.T) {
	t.Parallel()

	cases := map[backendv1.ExecutionMode]policy.Verdict{
		backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE: policy.Ask,
		backendv1.ExecutionMode_EXECUTION_MODE_GUARDED:     policy.Ask,
		backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS:  policy.Allow,
	}
	for mode, want := range cases {
		p := permissive(mode)
		if d := p.Evaluate("Bash", bash("./deploy.sh")); d.Verdict != want {
			t.Errorf("a command under %s = %v, want %v", mode, d.Verdict, want)
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

// TestEverySwitchBecomesAProviderRefusal pins the translation half of the
// contract: a capability the policy withholds has to arrive at the provider as
// a refusal, not as something the gate will catch later.
func TestEverySwitchBecomesAProviderRefusal(t *testing.T) {
	t.Parallel()

	p := permissive(backendv1.ExecutionMode_EXECUTION_MODE_GUARDED)
	p.AllowGitPush = false
	p.AllowGitCommit = false
	p.AllowNetwork = false
	p.AllowFilesystemWrite = false

	native := p.Native()
	for _, want := range []string{
		"Bash(git push *)", "Bash(git commit *)", "Bash(curl *)",
		"WebFetch", "WebSearch", "Write", "Edit", "NotebookEdit",
	} {
		if !contains(native.Deny, want) {
			t.Errorf("deny is missing %q, got %v", want, native.Deny)
		}
	}

	// And a policy that withholds nothing refuses nothing.
	if open := permissive(backendv1.ExecutionMode_EXECUTION_MODE_GUARDED).Native(); len(open.Deny) != 0 {
		t.Errorf("a policy that forbids nothing produced %v", open.Deny)
	}
}

// TestModesAreTranslatedNotReimplemented pins what is left of the modes once
// the provider does the classifying.
//
// GUARDED and AUTONOMOUS ask the provider for nothing special: its built-in
// read-only set is exactly what GUARDED means, and it is better at deciding
// than a list maintained here ever was. INTERACTIVE is the one that has to say
// something, because that set runs without asking in every mode and
// INTERACTIVE is the mode that asks about reading too.
func TestModesAreTranslatedNotReimplemented(t *testing.T) {
	t.Parallel()

	interactive := permissive(backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE).Native()
	for _, want := range []string{"Bash", "Read", "Glob", "Grep"} {
		if !contains(interactive.Ask, want) {
			t.Errorf("INTERACTIVE does not ask about %q, got %v", want, interactive.Ask)
		}
	}

	for _, mode := range []backendv1.ExecutionMode{
		backendv1.ExecutionMode_EXECUTION_MODE_GUARDED,
		backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS,
	} {
		if native := permissive(mode).Native(); len(native.Ask) != 0 {
			t.Errorf("%v asks the provider for %v, want nothing", mode, native.Ask)
		}
	}
}

// TestEveryCapabilityHasATranslation pins that the vocabulary Core promises is
// one this backend can actually render. A capability with no translation is a
// rule a user wrote and nobody applied.
func TestEveryCapabilityHasATranslation(t *testing.T) {
	t.Parallel()

	capabilities := []backendv1.PermissionCapability{
		backendv1.PermissionCapability_PERMISSION_CAPABILITY_SHELL,
		backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_READ,
		backendv1.PermissionCapability_PERMISSION_CAPABILITY_FILE_WRITE,
		backendv1.PermissionCapability_PERMISSION_CAPABILITY_NETWORK,
		backendv1.PermissionCapability_PERMISSION_CAPABILITY_GIT_COMMIT,
		backendv1.PermissionCapability_PERMISSION_CAPABILITY_GIT_PUSH,
		backendv1.PermissionCapability_PERMISSION_CAPABILITY_TOOL,
	}
	for _, capability := range capabilities {
		p := permissive(backendv1.ExecutionMode_EXECUTION_MODE_GUARDED)
		p.Rules = []policy.Rule{{
			Effect:     backendv1.PermissionEffect_PERMISSION_EFFECT_DENY,
			Capability: capability,
			Match:      "something",
		}}
		if native := p.Native(); len(native.Deny) == 0 {
			t.Errorf("%v renders to nothing", capability)
		}
	}
}

// TestRulesLandInTheRightList pins the three effects, and the shapes a reader
// of the provider settings would expect to see.
func TestRulesLandInTheRightList(t *testing.T) {
	t.Parallel()

	p := permissive(backendv1.ExecutionMode_EXECUTION_MODE_GUARDED)
	p.Rules = []policy.Rule{
		{
			Effect:     backendv1.PermissionEffect_PERMISSION_EFFECT_DENY,
			Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_SHELL,
			Match:      "kubectl delete *",
			Note:       "shared cluster",
		},
		{
			Effect:     backendv1.PermissionEffect_PERMISSION_EFFECT_ALLOW,
			Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_SHELL,
			Match:      "npm run *",
		},
		{
			Effect:     backendv1.PermissionEffect_PERMISSION_EFFECT_ASK,
			Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_TOOL,
			Match:      "mcp__threavia__task_create",
		},
		{
			Effect:     backendv1.PermissionEffect_PERMISSION_EFFECT_DENY,
			Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_NETWORK,
			Match:      "example.com",
		},
	}

	native := p.Native()
	if !contains(native.Deny, "Bash(kubectl delete *)") {
		t.Errorf("deny = %v", native.Deny)
	}
	if !contains(native.Allow, "Bash(npm run *)") {
		t.Errorf("allow = %v", native.Allow)
	}
	if !contains(native.Ask, "mcp__threavia__task_create") {
		t.Errorf("ask = %v", native.Ask)
	}
	if !contains(native.Deny, "WebFetch(domain:example.com)") {
		t.Errorf("deny = %v", native.Deny)
	}
}

// TestSettingsAreWhatTheProviderReads pins the shape of the document, since it
// is passed on a command line and a wrong key would fail silently.
func TestSettingsAreWhatTheProviderReads(t *testing.T) {
	t.Parallel()

	p := permissive(backendv1.ExecutionMode_EXECUTION_MODE_GUARDED)
	p.AllowGitPush = false

	rendered, err := p.Native().Settings()
	if err != nil {
		t.Fatalf("rendering the settings: %v", err)
	}

	var document struct {
		Permissions struct {
			Allow []string `json:"allow"`
			Ask   []string `json:"ask"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(rendered), &document); err != nil {
		t.Fatalf("the settings are not the document the provider reads: %v", err)
	}
	if !contains(document.Permissions.Deny, "Bash(git push *)") {
		t.Fatalf("deny = %v", document.Permissions.Deny)
	}
}

// TestARefusalIsEnforcedTwice pins the half that stays here.
//
// The provider's own documentation says a `Bash(git push *)` rule stops
// `git push origin main` and not `git -C . push origin main`. Reading the
// arguments is what catches the second, and a refusal is the one verdict worth
// paying for twice.
func TestARefusalIsEnforcedTwice(t *testing.T) {
	t.Parallel()

	p := permissive(backendv1.ExecutionMode_EXECUTION_MODE_GUARDED)
	p.Rules = []policy.Rule{{
		Effect:     backendv1.PermissionEffect_PERMISSION_EFFECT_DENY,
		Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_SHELL,
		Match:      "kubectl delete *",
		Note:       "shared cluster",
	}}

	refused := p.Evaluate("Bash", bash("kubectl delete pod x"))
	if refused.Verdict != policy.Deny {
		t.Fatalf("a denied command = %v, want Deny", refused.Verdict)
	}
	if !strings.Contains(refused.Reason, "shared cluster") {
		t.Errorf("the refusal does not say why: %q", refused.Reason)
	}

	// Behind a harmless prefix, and inside a pipeline, it is the same command.
	if d := p.Evaluate("Bash", bash("echo go && kubectl delete pod x")); d.Verdict != policy.Deny {
		t.Errorf("a denied command behind a prefix = %v, want Deny", d.Verdict)
	}
	// And a command that merely contains the words is not that command.
	if d := p.Evaluate("Bash", bash(`grep -n "kubectl delete" runbook.md`)); d.Verdict == policy.Deny {
		t.Error("a search for the words is not the command")
	}
}

// TestAForbiddenCallCannotHideBehindQuotes pins that the refusals read the same
// command the shell will run: a push is a push wherever it sits in a pipeline,
// and a quoted one is text.
func TestAForbiddenCallCannotHideBehindQuotes(t *testing.T) {
	t.Parallel()

	guarded := permissive(backendv1.ExecutionMode_EXECUTION_MODE_GUARDED)
	guarded.AllowGitPush = false

	if d := guarded.Evaluate("Bash", bash(`git status && git push origin master`)); d.Verdict != policy.Deny {
		t.Errorf("a push behind a status = %v, want Deny", d.Verdict)
	}
	// Not refused. Whether it then runs without asking is the provider's
	// answer, not this package's: it is a read, and reads never reach here.
	if d := guarded.Evaluate("Bash", bash(`grep -n "git push" docs/release.md`)); d.Verdict == policy.Deny {
		t.Errorf("a search for the words = %v, want anything but Deny", d.Verdict)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestAnUnapprovableHabitIsRefusedNotAsked pins the one prefix that costs a
// person for nothing.
//
// `export PATH=…:$PATH && grep foo src` cannot be approved by any rule: the
// provider refuses to decide a command whose value it cannot resolve before
// running it, and that verdict stands over an explicit allow — verified against
// the CLI, which answers "a variable in this command can't be checked before it
// runs". So the agent is told, in time to retry, instead of a human being woken
// up for a search.
func TestAnUnapprovableHabitIsRefusedNotAsked(t *testing.T) {
	t.Parallel()

	guarded := permissive(backendv1.ExecutionMode_EXECUTION_MODE_GUARDED)
	for _, cmd := range []string{
		`export PATH=/run/current-system/sw/bin:$PATH && grep -n foo internal`,
		`PATH=/usr/bin:$PATH make build`,
		`export IFS=,; read a b`,
		`cd /srv && export LD_PRELOAD=/tmp/x.so && ./run`,
	} {
		decision := guarded.Evaluate("Bash", bash(cmd))
		if decision.Verdict != policy.Deny {
			t.Errorf("%q = %v, want Deny", cmd, decision.Verdict)
		}
		if !strings.Contains(decision.Reason, "without the") {
			t.Errorf("%q was refused without telling the agent what to do: %q", cmd, decision.Reason)
		}
	}

	// An ordinary assignment is not one of these, and neither is a mention.
	for _, cmd := range []string{
		`GOFLAGS=-mod=mod go vet ./...`,
		`grep -n "export PATH=" docs/install.md`,
		`echo "home is $HOME"`,
	} {
		if d := guarded.Evaluate("Bash", bash(cmd)); d.Verdict == policy.Deny {
			t.Errorf("%q = Deny, want it left alone", cmd)
		}
	}

	// AUTONOMOUS never asks anyone, so the prefix costs nothing there and is
	// not worth refusing over.
	auto := permissive(backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS)
	if d := auto.Evaluate("Bash", bash(`export PATH=/usr/bin:$PATH && ls`)); d.Verdict != policy.Allow {
		t.Errorf("autonomous = %v, want Allow", d.Verdict)
	}
}

// TestTheTreesNextDoorAreReadable pins what stops a Job asking about the
// repository beside the one it works in.
//
// The provider asks about any file outside the directory a Job runs in, which
// is right by default and wrong for a machine whose projects sit side by side:
// half the approvals in a working session were reads of the repository next
// door. The backend's discovery roots are exactly the trees a Job is about, so
// they are named as readable.
func TestTheTreesNextDoorAreReadable(t *testing.T) {
	t.Parallel()

	native := permissive(backendv1.ExecutionMode_EXECUTION_MODE_GUARDED).Native()
	native.AdditionalDirectories = []string{"/home/thomas/Documents/Work"}

	rendered, err := native.Settings()
	if err != nil {
		t.Fatalf("rendering the settings: %v", err)
	}

	var document struct {
		Permissions struct {
			AdditionalDirectories []string `json:"additionalDirectories"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(rendered), &document); err != nil {
		t.Fatalf("the settings are not the document the provider reads: %v", err)
	}
	if !contains(document.Permissions.AdditionalDirectories, "/home/thomas/Documents/Work") {
		t.Fatalf("additionalDirectories = %v", document.Permissions.AdditionalDirectories)
	}

	// A backend with no discovery roots names none, rather than an empty list
	// the provider would have to interpret.
	bare := permissive(backendv1.ExecutionMode_EXECUTION_MODE_GUARDED).Native()
	if rendered, err := bare.Settings(); err != nil {
		t.Fatal(err)
	} else if strings.Contains(rendered, "additionalDirectories") {
		t.Errorf("settings = %s, want no additionalDirectories", rendered)
	}
}

// TestAPathDoesNotWalkAroundARule pins the hole that made the rules decorative.
//
// The agent reaches for an absolute path on its own — not to evade anything,
// but because it is unsure the program is on PATH — and the provider's own
// documentation lists `/usr/bin/curl` among what a `Bash(curl *)` rule does not
// stop. A refusal a prefix walks around is not a refusal.
func TestAPathDoesNotWalkAroundARule(t *testing.T) {
	t.Parallel()

	guarded := permissive(backendv1.ExecutionMode_EXECUTION_MODE_GUARDED)
	guarded.Rules = []policy.Rule{{
		Effect:     backendv1.PermissionEffect_PERMISSION_EFFECT_DENY,
		Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_SHELL,
		Match:      "kubectl delete *",
		Note:       "shared cluster",
	}}

	for _, cmd := range []string{
		`kubectl delete pod x`,
		`/run/current-system/sw/bin/kubectl delete pod x`,
		`/usr/bin/kubectl delete pod x`,
		`echo go && /run/current-system/sw/bin/kubectl delete pod x`,
		`KUBECONFIG=/tmp/k /run/current-system/sw/bin/kubectl delete pod x`,
	} {
		if d := guarded.Evaluate("Bash", bash(cmd)); d.Verdict != policy.Deny {
			t.Errorf("%q = %v, want Deny", cmd, d.Verdict)
		}
	}

	// What the rule is not about stays untouched, path or no path.
	for _, cmd := range []string{
		`kubectl get pods`,
		`/run/current-system/sw/bin/kubectl get pods`,
		`grep -n "kubectl delete" runbook.md`,
	} {
		if d := guarded.Evaluate("Bash", bash(cmd)); d.Verdict == policy.Deny {
			t.Errorf("%q = Deny, want it left alone", cmd)
		}
	}

	// And the capability switches read a path the same way.
	offline := permissive(backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS)
	offline.AllowNetwork = false
	if d := offline.Evaluate("Bash", bash(`/usr/bin/curl https://example.com`)); d.Verdict != policy.Deny {
		t.Errorf("a curl behind a path = %v, want Deny", d.Verdict)
	}
}
