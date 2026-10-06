package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/rclsilver/threavia/pkg/backend-sdk/state"
)

// ErrNoCredential is returned when a backend has neither a stored identity nor
// any way to obtain one.
var ErrNoCredential = errors.New("no backend credential and no way to register")

// Credential resolves the persistent credential this backend presents on every
// connection (THREAVIA_SPEC_V1.md section 8).
//
// A registered backend stores its identity locally, so registration material is
// not required at each reboot. Only a backend that has never registered reaches
// out to Core, with either a one-shot user token or the shared registration key.
func Credential(ctx context.Context, cfg Config, store state.Store, logger *slog.Logger) (string, error) {
	if identity, found, err := store.LoadIdentity(ctx); err != nil {
		return "", err
	} else if found && identity.Token != "" {
		logger.Info("using the stored backend identity",
			slog.String("backendInstanceId", identity.BackendInstanceID))
		return identity.Token, nil
	}

	// An explicitly configured credential wins: it is how an operator restores a
	// backend from a secret manager.
	if cfg.Client.Token != "" {
		if err := store.SaveIdentity(ctx, state.Identity{Token: cfg.Client.Token, Name: cfg.Client.InstanceName}); err != nil {
			return "", err
		}
		return cfg.Client.Token, nil
	}

	if cfg.Registration.Token == "" && cfg.Registration.SharedKey == "" {
		return "", fmt.Errorf("%w: set %sTOKEN, %sREGISTRATION_TOKEN or %sSHARED_KEY",
			ErrNoCredential, EnvPrefix, EnvPrefix, EnvPrefix)
	}

	result, err := register(ctx, cfg)
	if err != nil {
		return "", err
	}

	if err := store.SaveIdentity(ctx, state.Identity{
		BackendInstanceID: result.BackendInstanceID,
		Token:             result.Credential,
		Name:              cfg.Client.InstanceName,
	}); err != nil {
		return "", err
	}

	logger.Info("backend registered with core",
		slog.String("backendInstanceId", result.BackendInstanceID),
		slog.Bool("claimed", result.Claimed))

	if result.ClaimCode != "" {
		// The shared-key flow leaves the instance UNCLAIMED until a user enters
		// this code. It is displayed locally and never sent anywhere.
		logger.Warn("this backend is not claimed yet")
		fmt.Printf("\n  Threavia claim code: %s\n  Enter it once in your Threavia client to own this backend.\n\n", result.ClaimCode)
	}
	return result.Credential, nil
}

type registrationResult struct {
	BackendInstanceID string `json:"backendInstanceId"`
	Credential        string `json:"credential"`
	ClaimCode         string `json:"claimCode"`
	Claimed           bool   `json:"claimed"`
}

// register performs the one HTTP call a backend makes before it has any
// credential.
func register(ctx context.Context, cfg Config) (registrationResult, error) {
	capabilities := make([]string, 0, len(cfg.Client.Capabilities))
	for _, capability := range cfg.Client.Capabilities {
		capabilities = append(capabilities, capabilityName(capability.String()))
	}

	body, err := json.Marshal(map[string]any{
		"name":         cfg.Client.InstanceName,
		"capabilities": capabilities,
		"token":        cfg.Registration.Token,
		"sharedKey":    cfg.Registration.SharedKey,
	})
	if err != nil {
		return registrationResult{}, err
	}

	endpoint := strings.TrimSuffix(cfg.Registration.CoreAPI, "/") + "/api/v1/backends/register"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return registrationResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return registrationResult{}, fmt.Errorf("register with core at %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		var failure struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&failure)
		return registrationResult{}, fmt.Errorf("register with core: %s: %s", resp.Status, failure.Error.Message)
	}

	var result registrationResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return registrationResult{}, fmt.Errorf("decode the registration response: %w", err)
	}
	return result, nil
}

// capabilityName turns the protobuf enum name into the domain name Core stores.
func capabilityName(enumName string) string {
	return strings.TrimPrefix(enumName, "CAPABILITY_")
}
