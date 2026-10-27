// Package kube contains the executor's namespaced Kubernetes API client and
// administrator-defined pod profiles. Agent input is never a Pod manifest.
package kube

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type APIConfig struct{ URL, CAFile, TokenFile, Namespace string }
type Client struct {
	base      string
	namespace string
	tokenFile string
	http      *http.Client
}
type APIError struct {
	Status int
	Reason string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Kubernetes API returned %d (%s)", e.Status, e.Reason)
}
func IsNotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}
func IsConflict(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusConflict
}
func NewClient(c APIConfig) (*Client, error) {
	u, err := url.Parse(c.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("Kubernetes API must be an HTTPS origin")
	}
	if !validName(c.Namespace) || c.TokenFile == "" || c.CAFile == "" {
		return nil, errors.New("Kubernetes namespace, CA file and token file are required")
	}
	ca, err := os.ReadFile(c.CAFile)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("Kubernetes CA file contains no certificate")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	transport.ResponseHeaderTimeout = 15 * time.Second
	return &Client{base: strings.TrimRight(c.URL, "/"), namespace: c.Namespace, tokenFile: c.TokenFile, http: &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Kubernetes API redirects are refused") }}}, nil
}
func (c *Client) path(group, resource, name string) string {
	prefix := "/api/v1"
	if group != "" {
		prefix = "/apis/" + group
	}
	result := prefix + "/namespaces/" + url.PathEscape(c.namespace) + "/" + resource
	if name != "" {
		result += "/" + url.PathEscape(name)
	}
	return result
}
func (c *Client) do(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	token, err := os.ReadFile(c.tokenFile)
	if err != nil {
		return fmt.Errorf("read Kubernetes service account token: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	request.Header.Set("Content-Type", "application/json")
	if method == http.MethodPatch {
		request.Header.Set("Content-Type", "application/json-patch+json")
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("Kubernetes API request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Status.message can quote the submitted manifest, including scoped worker
		// credentials. Retain the reason/code only, never the body or request headers.
		var status struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&status)
		if len(status.Reason) > 64 {
			status.Reason = "RequestFailed"
		}
		return &APIError{Status: response.StatusCode, Reason: status.Reason}
	}
	if output != nil {
		return json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(output)
	}
	_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 8<<20))
	return err
}
func (c *Client) CreatePod(ctx context.Context, pod map[string]any) (Pod, error) {
	var result Pod
	err := c.do(ctx, http.MethodPost, c.path("", "pods", ""), pod, &result)
	return result, err
}
func (c *Client) Pod(ctx context.Context, name string) (Pod, error) {
	var result Pod
	err := c.do(ctx, http.MethodGet, c.path("", "pods", name), nil, &result)
	return result, err
}
func (c *Client) DeletePod(ctx context.Context, name, uid string) error {
	return c.do(ctx, http.MethodDelete, c.path("", "pods", name), map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "gracePeriodSeconds": 10, "preconditions": map[string]string{"uid": uid}}, nil)
}
func (c *Client) ListPods(ctx context.Context, selector string) ([]Pod, error) {
	var result struct {
		Items []Pod `json:"items"`
	}
	err := c.do(ctx, http.MethodGet, c.path("", "pods", "")+"?labelSelector="+url.QueryEscape(selector), nil, &result)
	return result.Items, err
}
func (c *Client) CreatePolicy(ctx context.Context, policy map[string]any) error {
	return c.do(ctx, http.MethodPost, c.path("networking.k8s.io/v1", "networkpolicies", ""), policy, nil)
}
func (c *Client) DeletePolicy(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, c.path("networking.k8s.io/v1", "networkpolicies", name), map[string]any{"apiVersion": "v1", "kind": "DeleteOptions"}, nil)
}
func (c *Client) CreateSecret(ctx context.Context, secret map[string]any) error {
	return c.do(ctx, http.MethodPost, c.path("", "secrets", ""), secret, nil)
}
func (c *Client) DeleteSecret(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, c.path("", "secrets", name), map[string]any{"apiVersion": "v1", "kind": "DeleteOptions"}, nil)
}

type Pod struct {
	Metadata struct {
		Name, UID, ResourceVersion string
		Labels                     map[string]string
		DeletionTimestamp          *time.Time
		Finalizers                 []string
	} `json:"metadata"`
	Spec   struct{ NodeName string } `json:"spec"`
	Status struct {
		Phase             string
		Reason            string
		ContainerStatuses []ContainerStatus `json:"containerStatuses"`
	} `json:"status"`
}

func (p Pod) Terminated() (bool, int) {
	for _, container := range p.Status.ContainerStatuses {
		if container.Name == "worker" && container.State.Terminated != nil {
			return true, container.State.Terminated.ExitCode
		}
	}
	// A control-plane phase such as Failed/NodeLost is not evidence that an
	// unreachable node stopped an admitted process. Require its runtime status.
	return false, -1
}

type Termination struct {
	ExitCode        int
	Reason, Message string
	FinishedAt      time.Time
}
type ContainerStatus struct {
	Name  string
	State struct {
		Terminated *Termination `json:"terminated"`
	} `json:"state"`
}

const ExitFinalizer = "execution.threavia.io/confirmed-exit"

func (c *Client) ReleasePod(ctx context.Context, p Pod) error {
	finalizers := []string{}
	found := false
	for _, f := range p.Metadata.Finalizers {
		if f == ExitFinalizer {
			found = true
		} else {
			finalizers = append(finalizers, f)
		}
	}
	if !found {
		return nil
	}
	patch := []map[string]any{{"op": "test", "path": "/metadata/uid", "value": p.Metadata.UID}, {"op": "test", "path": "/metadata/resourceVersion", "value": p.Metadata.ResourceVersion}, {"op": "replace", "path": "/metadata/finalizers", "value": finalizers}}
	return c.do(ctx, http.MethodPatch, c.path("", "pods", p.Metadata.Name), patch, nil)
}
