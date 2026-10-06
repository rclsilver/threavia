// Package client is the reusable Go SDK a BackendInstance uses to talk to
// Threavia Core.
//
// The backend always initiates the outbound connection and keeps one long-lived
// bidirectional control stream open (THREAVIA_SPEC_V1.md section 9). Core never
// dials a backend, so a laptop behind NAT is a first-class BackendInstance.
package client

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

// ProtocolVersion is the wire protocol version this SDK speaks. It is defined
// once, in the protocol itself.
const ProtocolVersion = uint32(backendv1.ProtocolVersion_PROTOCOL_VERSION_V1)

// Identity of this SDK, advertised to Core in the Hello message.
const (
	SDKName    = "go"
	SDKVersion = "0.1.0-dev"
)

// TLSConfig describes the transport security of the outbound connection.
type TLSConfig struct {
	// Enabled turns on TLS. Remote Core access must use TLS (spec section 28);
	// plaintext is only acceptable for a local development Core.
	Enabled bool
	// CAFile is an optional PEM bundle trusted in addition to the system roots.
	CAFile string
	// ServerName overrides the name verified in the Core certificate.
	ServerName string
	// InsecureSkipVerify disables certificate verification. Development only.
	InsecureSkipVerify bool
}

// Config configures the control connection.
type Config struct {
	// CoreAddress is the Core gRPC endpoint, as host:port.
	CoreAddress string
	// Token is the persistent backend credential obtained at registration. It is
	// presented as an "authorization: Bearer" header and never logged.
	Token string

	// InstanceName is the human-readable name of this BackendInstance.
	InstanceName string
	// BackendName and BackendVersion describe the provider integration, for
	// example "claude".
	BackendName    string
	BackendVersion string

	Capabilities        []backendv1.Capability
	FeatureJobInputNow  bool
	FeatureJobInputNext bool
	MaxConcurrentRuns   int32

	TLS TLSConfig

	DialTimeout       time.Duration
	HeartbeatInterval time.Duration

	ReconnectMinBackoff time.Duration
	ReconnectMaxBackoff time.Duration

	// SendBuffer bounds the outbound queue of the control stream.
	SendBuffer int

	// DialOptions are appended to the gRPC options used to reach Core. They
	// exist for tests and for deployments needing a custom dialer or
	// interceptor; transport security always comes from TLS above.
	DialOptions []grpc.DialOption
}

// DefaultConfig returns the SDK defaults.
func DefaultConfig() Config {
	return Config{
		Capabilities:        []backendv1.Capability{backendv1.Capability_CAPABILITY_CODE},
		MaxConcurrentRuns:   1,
		TLS:                 TLSConfig{Enabled: true},
		DialTimeout:         10 * time.Second,
		HeartbeatInterval:   15 * time.Second,
		ReconnectMinBackoff: time.Second,
		ReconnectMaxBackoff: 30 * time.Second,
		SendBuffer:          128,
	}
}

// Validate checks the configuration.
func (c Config) Validate() error {
	if c.CoreAddress == "" {
		return errors.New("core address is required")
	}
	if c.Token == "" {
		return errors.New("backend credential is required")
	}
	if c.InstanceName == "" {
		return errors.New("instance name is required")
	}
	if len(c.Capabilities) == 0 {
		return errors.New("at least one capability must be advertised")
	}
	if c.MaxConcurrentRuns < 0 {
		return errors.New("max concurrent runs cannot be negative")
	}
	if c.HeartbeatInterval <= 0 {
		return errors.New("heartbeat interval must be greater than zero")
	}
	if c.ReconnectMinBackoff <= 0 || c.ReconnectMaxBackoff < c.ReconnectMinBackoff {
		return errors.New("invalid reconnection backoff bounds")
	}
	if c.SendBuffer <= 0 {
		return errors.New("send buffer must be greater than zero")
	}
	if c.TLS.CAFile != "" && !c.TLS.Enabled {
		return errors.New("a CA bundle was configured but TLS is disabled")
	}
	return nil
}

// transportCredentials builds the gRPC transport security for the configuration.
func (c Config) transportCredentials() (credentials.TransportCredentials, error) {
	if !c.TLS.Enabled {
		return insecure.NewCredentials(), nil
	}

	tlsCfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         c.TLS.ServerName,
		InsecureSkipVerify: c.TLS.InsecureSkipVerify, //nolint:gosec // explicit development opt-in
	}
	if c.TLS.CAFile != "" {
		pem, err := os.ReadFile(c.TLS.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA bundle: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA bundle %s contains no usable certificate", c.TLS.CAFile)
		}
		tlsCfg.RootCAs = pool
	}
	return credentials.NewTLS(tlsCfg), nil
}

// dialOptions builds the gRPC client options.
func (c Config) dialOptions() ([]grpc.DialOption, error) {
	creds, err := c.transportCredentials()
	if err != nil {
		return nil, err
	}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(creds)}
	return append(opts, c.DialOptions...), nil
}

// featureFlags builds the advertised optional CODE feature flags.
func (c Config) featureFlags() *backendv1.FeatureFlags {
	return &backendv1.FeatureFlags{
		JobInputNow:  c.FeatureJobInputNow,
		JobInputNext: c.FeatureJobInputNext,
	}
}
