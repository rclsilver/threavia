package kube

import "encoding/base64"

func WorkerSecretName(pod string) string { return pod + "-token" }

// WorkerSecret keeps the single-use attachment credential out of Pod specs
// visible to ordinary Kubernetes viewers. Its owning Pod also provides GC.
func (l Launch) WorkerSecret(uid string) map[string]any {
	name := PodName(l.Controller, l.Profile.Name, l.Request.ID)
	return map[string]any{"apiVersion": "v1", "kind": "Secret", "type": "Opaque", "metadata": map[string]any{
		"name": WorkerSecretName(name), "namespace": l.Namespace,
		"labels":          map[string]string{labelController: l.Controller, labelProfile: l.Profile.Name, labelExecution: l.Request.ID},
		"ownerReferences": []map[string]any{{"apiVersion": "v1", "kind": "Pod", "name": name, "uid": uid}},
	}, "data": map[string]string{"token": base64.StdEncoding.EncodeToString([]byte(l.Token))}}
}
