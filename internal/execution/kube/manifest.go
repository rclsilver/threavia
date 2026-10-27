package kube

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"strconv"
	"time"
)

func (l Launch) Pod(now time.Time) (map[string]any, error) {
	p, r := l.Profile, l.Request
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if err := r.Validate(p, now); err != nil {
		return nil, err
	}
	u, err := url.Parse(l.PublicURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("executor public endpoint must use HTTPS")
	}
	if !validName(l.Controller) || !validName(l.Namespace) || !validName(l.CASecretName) || l.SessionID == "" || len(l.Token) < 32 {
		return nil, errors.New("execution launch identity is incomplete")
	}
	labels := map[string]string{"app.kubernetes.io/name": "threavia-execution", labelController: l.Controller, labelProfile: p.Name, labelExecution: r.ID}
	env := []map[string]any{
		{"name": "HOME", "value": p.Home},
		{"name": "THREAVIA_EXECUTION_ENDPOINT", "value": l.PublicURL},
		{"name": "THREAVIA_EXECUTION_CA", "value": "/var/run/threavia-execution/ca.crt"},
		{"name": "THREAVIA_EXECUTION_SESSION", "value": l.SessionID},
		{"name": "THREAVIA_EXECUTION_TOKEN", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": WorkerSecretName(PodName(l.Controller, p.Name, r.ID)), "key": "token"}}},
		{"name": "THREAVIA_EXECUTION_PROVIDER", "value": p.Provider},
		{"name": "THREAVIA_EXECUTION_BINARY", "value": p.ProviderBinary},
		{"name": "THREAVIA_EXECUTION_KIND", "value": r.Kind},
		{"name": "THREAVIA_EXECUTION_NETWORK", "value": strconv.FormatBool(r.Network)},
		{"name": "THREAVIA_EXECUTION_ID", "value": r.ID},
		{"name": "THREAVIA_EXECUTION_RUN_ID", "value": r.RunID},
		{"name": "THREAVIA_EXECUTION_JOB_ID", "value": r.JobID},
		{"name": "THREAVIA_EXECUTION_DEADLINE", "value": r.ExpiresAt.UTC().Format(time.RFC3339Nano)},
		{"name": "THREAVIA_EXECUTION_POD_UID", "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": "metadata.uid"}}},
	}
	extra := p.ProviderEnv
	if r.Kind == "monitor" {
		extra = p.MonitorEnv
	}
	readable := []string{}
	for _, m := range p.Mounts {
		if m.Role == "workspace" || m.Role == "tools" {
			readable = append(readable, m.Path)
		}
	}
	readableJSON, _ := json.Marshal(readable)
	env = append(env, map[string]any{"name": "THREAVIA_EXECUTION_READABLE", "value": string(readableJSON)})
	for _, e := range extra {
		value := map[string]any{"name": e.Name}
		if e.SecretName != "" {
			value["valueFrom"] = map[string]any{"secretKeyRef": map[string]any{"name": e.SecretName, "key": e.SecretKey}}
		} else {
			value["value"] = e.Value
		}
		env = append(env, value)
	}
	volumes := []map[string]any{{"name": "temporary", "emptyDir": map[string]any{"sizeLimit": "1Gi"}}}
	mounts := []map[string]any{{"name": "temporary", "mountPath": "/tmp"}, {"name": "temporary", "mountPath": "/var/tmp"}}
	// Publish only the trust root. The controller's TLS private key and backend
	// control token are never mounted into execution pods.
	volumes = append(volumes, map[string]any{"name": "executor-ca", "secret": map[string]any{"secretName": l.CASecretName, "items": []map[string]string{{"key": "ca.crt", "path": "ca.crt"}}}})
	mounts = append(mounts, map[string]any{"name": "executor-ca", "mountPath": "/var/run/threavia-execution", "readOnly": true})
	homeMounted := false
	claimVolumes := map[string]string{}
	for _, m := range p.Mounts {
		if r.Kind == "monitor" && (m.Role == "provider" || m.Role == "scratch" || m.Role == "skills" || m.Role == "credential") {
			continue
		}
		// Only this Job's assembled skill directory may cross from the control
		// backend volume. Its sibling SQLite identity and credentials never mount.
		sub := m.SubPath
		target := m.Path
		if m.Role == "skills" {
			if r.Kind != "job" || !r.Skills {
				continue
			}
			sub = filepath.Join(sub, r.JobID)
			target = filepath.Join(target, r.JobID)
		}
		if m.Role == "scratch" {
			if r.Kind != "job" || !r.Scratch {
				continue
			}
			sub = filepath.Join(sub, r.RunID)
			target = filepath.Join(target, r.RunID)
			env = append(env, map[string]any{"name": "THREAVIA_EXECUTION_SCRATCH", "value": m.Path})
		}
		volumeName := claimVolumes[m.Claim]
		if volumeName == "" {
			volumeName = m.Name
			claimVolumes[m.Claim] = volumeName
			volumes = append(volumes, map[string]any{"name": volumeName, "persistentVolumeClaim": map[string]any{"claimName": m.Claim}})
		}
		ro := m.ReadOnly || m.Role == "tools" || m.Role == "skills" || m.Role == "credential" || m.Role == "workspace" && (!r.Writable || r.Kind != "job")
		if r.Kind == "monitor" && m.Role == "workspace" && r.Directory == m.Path && r.Writable && !m.ReadOnly {
			ro = false
		}
		mount := map[string]any{"name": volumeName, "mountPath": target, "readOnly": ro}
		if sub != "" {
			mount["subPath"] = sub
		}
		mounts = append(mounts, mount)
		if m.Path == p.Home {
			homeMounted = true
		}
		if r.Kind == "monitor" && m.Role == "workspace" && r.Writable && !m.ReadOnly && r.Directory != m.Path && within(m.Path, r.Directory) {
			rel, err := filepath.Rel(m.Path, r.Directory)
			if err != nil {
				return nil, err
			}
			mounts = append(mounts, map[string]any{"name": volumeName, "mountPath": r.Directory, "subPath": filepath.Join(m.SubPath, rel), "readOnly": false})
		}
	}
	if !homeMounted {
		volumes = append(volumes, map[string]any{"name": "private-home", "emptyDir": map[string]any{"sizeLimit": "256Mi"}})
		mounts = append(mounts, map[string]any{"name": "private-home", "mountPath": p.Home})
	}
	security := map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "runAsNonRoot": true, "runAsUser": p.UID, "runAsGroup": p.GID, "capabilities": map[string]any{"drop": []string{"ALL"}}, "seccompProfile": map[string]any{"type": "RuntimeDefault"}}
	// Monitors use the runtime's own isolation and do not need provider-specific
	// namespace/mount exceptions. Named native profiles apply to provider pods.
	if r.Kind != "monitor" {
		if p.SeccompProfile != "" {
			security["seccompProfile"] = map[string]any{"type": "Localhost", "localhostProfile": p.SeccompProfile}
		}
		if p.AppArmorProfile != "" {
			security["appArmorProfile"] = map[string]any{"type": "Localhost", "localhostProfile": p.AppArmorProfile}
		}
	}
	seconds := int64(math.Ceil(r.ExpiresAt.Sub(now).Seconds()))
	if seconds < 1 {
		return nil, errors.New("execution deadline expired")
	}
	spec := map[string]any{
		"restartPolicy": "Never", "terminationGracePeriodSeconds": 10, "activeDeadlineSeconds": seconds,
		"automountServiceAccountToken": false, "serviceAccountName": p.ServiceAccount, "enableServiceLinks": false,
		"hostNetwork": false, "hostPID": false, "hostIPC": false,
		"securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": p.UID, "runAsGroup": p.GID, "fsGroup": p.GID, "seccompProfile": map[string]any{"type": "RuntimeDefault"}},
		"containers":      []map[string]any{{"name": "worker", "image": p.Image, "imagePullPolicy": "IfNotPresent", "command": []string{p.WorkerBinary, "--kubernetes-worker"}, "workingDir": r.Directory, "env": env, "securityContext": security, "resources": p.Resources, "volumeMounts": mounts}}, "volumes": volumes,
	}
	if len(p.NodeSelector) > 0 {
		spec["nodeSelector"] = p.NodeSelector
	}
	if len(p.Tolerations) > 0 {
		spec["tolerations"] = p.Tolerations
	}
	if len(p.ImagePullSecrets) > 0 {
		secrets := []map[string]string{}
		for _, name := range p.ImagePullSecrets {
			if !validName(name) {
				return nil, fmt.Errorf("invalid image pull secret name")
			}
			secrets = append(secrets, map[string]string{"name": name})
		}
		spec["imagePullSecrets"] = secrets
	}
	return map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": PodName(l.Controller, p.Name, r.ID), "namespace": l.Namespace, "labels": labels, "finalizers": []string{ExitFinalizer}}, "spec": spec}, nil
}

type ControlNetwork struct {
	Namespace string
	Selector  map[string]string
	Port      int32
}

func (l Launch) NetworkPolicy(control ControlNetwork) (map[string]any, error) {
	if !validName(control.Namespace) || len(control.Selector) == 0 || control.Port < 1 || control.Port > 65535 {
		return nil, errors.New("executor network destination must be explicit")
	}
	egress := []map[string]any{
		{"to": []map[string]any{{"namespaceSelector": map[string]any{"matchLabels": map[string]string{"kubernetes.io/metadata.name": control.Namespace}}, "podSelector": map[string]any{"matchLabels": control.Selector}}}, "ports": []map[string]any{{"protocol": "TCP", "port": control.Port}}},
		{"to": []map[string]any{{"namespaceSelector": map[string]any{"matchLabels": map[string]string{"kubernetes.io/metadata.name": "kube-system"}}, "podSelector": map[string]any{"matchLabels": map[string]string{"k8s-app": "kube-dns"}}}}, "ports": []map[string]any{{"protocol": "UDP", "port": 53}, {"protocol": "TCP", "port": 53}}},
	}
	if l.Request.Kind != "monitor" {
		egress = append(egress, l.Profile.ProviderEgress...)
	}
	if l.Request.Network && l.Request.Kind != "probe" {
		egress = append(egress, map[string]any{})
	}
	return map[string]any{"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy", "metadata": map[string]any{"name": PodName(l.Controller, l.Profile.Name, l.Request.ID), "namespace": l.Namespace}, "spec": map[string]any{"podSelector": map[string]any{"matchLabels": map[string]string{labelController: l.Controller, labelProfile: l.Profile.Name, labelExecution: l.Request.ID}}, "policyTypes": []string{"Ingress", "Egress"}, "ingress": []any{}, "egress": egress}}, nil
}
