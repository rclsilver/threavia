package kube

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func testProfile() Profile {
	return Profile{Name: "codex", Provider: "codex", Image: "example.invalid/worker:v1", WorkerBinary: "/worker", ProviderBinary: "codex", WorkingDirectory: "/workspace", Home: "/home/agent", ServiceAccount: "execution-worker", UID: 1001, GID: 1001, Mounts: []Mount{{Name: "work", Claim: "workspaces", Path: "/workspace", Role: "workspace"}, {Name: "provider", Claim: "native-state", Path: "/home/agent", Role: "provider"}, {Name: "skills", Claim: "backend-state", Path: "/skills", SubPath: "skills/jobs", Role: "skills", ReadOnly: true}}, ProviderEnv: []Env{{Name: "OPENAI_API_KEY", SecretName: "provider-auth", SecretKey: "key"}}, Resources: map[string]any{"requests": map[string]string{"cpu": "100m", "memory": "128Mi"}, "limits": map[string]string{"cpu": "1", "memory": "1Gi"}}}
}
func launch(kind string) Launch {
	now := time.Now().UTC()
	id := uuid.NewString()
	return Launch{Controller: "test", Namespace: "execution", PublicURL: "https://executor.execution.svc:8443", CASecretName: "executor-ca", SessionID: uuid.NewString(), Token: strings.Repeat("x", 48), Profile: testProfile(), Request: Request{ID: id, JobID: id, RunID: uuid.NewString(), Kind: kind, Directory: "/workspace/project", ExpiresAt: now.Add(time.Hour)}}
}
func TestMonitorDoesNotMountProviderCredentialsOrBackendState(t *testing.T) {
	l := launch("monitor")
	l.Request.Command = "echo ready"
	l.Request.Writable = true
	pod, err := l.Pod(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(pod)
	encoded := string(raw)
	if strings.Contains(encoded, l.Token) {
		t.Fatal("worker credential exposed in readable Pod manifest")
	}
	for _, forbidden := range []string{"OPENAI_API_KEY", "provider-auth", "native-state", "backend-state", "hostPath", "privileged", "registrationToken"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("monitor contains %s", forbidden)
		}
	}
	spec := pod["spec"].(map[string]any)
	if spec["automountServiceAccountToken"] != false || spec["hostPID"] != false || spec["restartPolicy"] != "Never" {
		t.Fatal("unsafe process identity")
	}
	container := spec["containers"].([]map[string]any)[0]
	mounts := container["volumeMounts"].([]map[string]any)
	rootRO, cwdRW := false, false
	for _, m := range mounts {
		if m["mountPath"] == "/workspace" {
			rootRO = m["readOnly"] == true
		}
		if m["mountPath"] == "/workspace/project" {
			cwdRW = m["readOnly"] == false && m["subPath"] == "project"
		}
	}
	if !rootRO || !cwdRW {
		t.Fatalf("monitor mount boundary not preserved: %v", mounts)
	}
}

func TestWorkerCredentialIsScopedToOwningPodSecret(t *testing.T) {
	l := launch("job")
	secret := l.WorkerSecret("pod-uid")
	metadata := secret["metadata"].(map[string]any)
	owner := metadata["ownerReferences"].([]map[string]any)[0]
	if owner["uid"] != "pod-uid" || owner["name"] != PodName(l.Controller, l.Profile.Name, l.Request.ID) {
		t.Fatal("worker credential is not bound to its Pod")
	}
}

func TestNodeLostPhaseDoesNotConfirmRuntimeExit(t *testing.T) {
	var pod Pod
	pod.Status.Phase = "Failed"
	pod.Status.Reason = "NodeLost"
	if ended, _ := pod.Terminated(); ended {
		t.Fatal("control-plane phase became evidence of physical exit")
	}
	pod.Status.ContainerStatuses = []ContainerStatus{{Name: "worker"}}
	pod.Status.ContainerStatuses[0].State.Terminated = &Termination{ExitCode: 137, FinishedAt: time.Now()}
	if ended, _ := pod.Terminated(); !ended {
		t.Fatal("runtime exit was not recognized")
	}
}
func TestJobMountsOnlyItsSkillsAndKeepsProviderIdentitySeparate(t *testing.T) {
	l := launch("job")
	l.Request.Skills = true
	l.Request.Writable = false
	pod, err := l.Pod(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	mounts := pod["spec"].(map[string]any)["containers"].([]map[string]any)[0]["volumeMounts"].([]map[string]any)
	found := false
	for _, m := range mounts {
		if m["name"] == "skills" {
			found = true
			if m["subPath"] != "skills/jobs/"+l.Request.JobID || m["readOnly"] != true {
				t.Fatal(m)
			}
		}
		if m["name"] == "work" && m["readOnly"] != true {
			t.Fatal("read-only job received writable workspace")
		}
	}
	if !found {
		t.Fatal("job skills were not mounted")
	}
}
func TestRejectsUnscopedOrProtectedVolumesAndEnvironment(t *testing.T) {
	cases := []func(*Profile){func(p *Profile) { p.Mounts[0].Path = "/" }, func(p *Profile) { p.Mounts[0].Path = "/proc/subdir" }, func(p *Profile) { p.Mounts[0].SubPath = "../backend" }, func(p *Profile) {
		p.ProviderEnv = append(p.ProviderEnv, Env{Name: "THREAVIA_BACKEND_TOKEN", Value: "no"})
	}, func(p *Profile) { p.UID = 0 }}
	for i, change := range cases {
		p := testProfile()
		change(&p)
		if p.Validate() == nil {
			t.Errorf("unsafe profile %d accepted", i)
		}
	}
	l := launch("monitor")
	l.Request.Command = "true"
	l.Request.Directory = "/workspace-other/private"
	if _, err := l.Pod(time.Now()); err == nil {
		t.Fatal("workspace prefix traversal accepted")
	}
}
func TestMonitorCannotInheritProviderNetworkExceptions(t *testing.T) {
	l := launch("monitor")
	l.Profile.ProviderEgress = []map[string]any{{"providerOnly": true}}
	policy, err := l.NetworkPolicy(ControlNetwork{Namespace: "execution", Selector: map[string]string{"app": "executor"}, Port: 8443})
	if err != nil {
		t.Fatal(err)
	}
	egress := policy["spec"].(map[string]any)["egress"].([]map[string]any)
	if len(egress) != 2 {
		t.Fatal("provider egress leaked into monitor")
	}
	l.Request.Network = true
	policy, err = l.NetworkPolicy(ControlNetwork{Namespace: "execution", Selector: map[string]string{"app": "executor"}, Port: 8443})
	if err != nil {
		t.Fatal(err)
	}
	if len(policy["spec"].(map[string]any)["egress"].([]map[string]any)) != 3 {
		t.Fatal("approved monitor network not enabled")
	}
}

func TestSharedClaimUsesOneVolumeWithSeparateSubpaths(t *testing.T) {
	l := launch("job")
	l.Request.Skills = true
	l.Request.Scratch = true
	l.Profile.Mounts = append(l.Profile.Mounts, Mount{Name: "scratch", Claim: "backend-state", Path: "/scratch", SubPath: "scratch", Role: "scratch"})
	pod, err := l.Pod(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	spec := pod["spec"].(map[string]any)
	claims := 0
	for _, volume := range spec["volumes"].([]map[string]any) {
		if claim, ok := volume["persistentVolumeClaim"].(map[string]any); ok && claim["claimName"] == "backend-state" {
			claims++
		}
	}
	if claims != 1 {
		t.Fatalf("shared claim produced %d volume identities", claims)
	}
	ro, rw := false, false
	for _, mount := range spec["containers"].([]map[string]any)[0]["volumeMounts"].([]map[string]any) {
		if mount["name"] == "skills" && mount["subPath"] == "skills/jobs/"+l.Request.JobID {
			ro = mount["readOnly"] == true
		}
		if mount["name"] == "skills" && mount["subPath"] == "scratch/"+l.Request.RunID {
			rw = mount["readOnly"] == false
		}
	}
	if !ro || !rw {
		t.Fatal("shared subpath permissions were merged")
	}
}
