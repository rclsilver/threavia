// Package transport authenticates streams without redirects, proxies or an
// implicit system trust store. Backend and worker credentials are separate.
package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/rclsilver/threavia/internal/execution/bridge"
	"golang.org/x/net/websocket"
)

type Transport struct {
	Base string
	HTTP *http.Client
	TLS  *tls.Config
}

func New(endpoint, caFile string) (*Transport, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return nil, errors.New("executor endpoint must be an HTTPS origin")
	}
	raw, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(raw) {
		return nil, errors.New("executor CA contains no certificate")
	}
	tlsConfig := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.TLSClientConfig = tlsConfig
	t.ResponseHeaderTimeout = 15 * time.Second
	return &Transport{Base: strings.TrimRight(endpoint, "/"), TLS: tlsConfig, HTTP: &http.Client{Transport: t, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("executor redirects refused") }}}, nil
}
func (t *Transport) Dial(ctx context.Context, path string, headers http.Header) (*websocket.Conn, error) {
	cfg, err := websocket.NewConfig("wss"+strings.TrimPrefix(t.Base, "https")+path, t.Base)
	if err != nil {
		return nil, err
	}
	cfg.TlsConfig = t.TLS.Clone()
	// net/http enables HTTP/2 on its TLS config after the first REST call.
	// RFC 6455 uses an HTTP/1.1 upgrade; negotiating h2 corrupts the handshake.
	cfg.TlsConfig.NextProtos = []string{"http/1.1"}
	cfg.Dialer = &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 15 * time.Second}
	cfg.Header = headers.Clone()
	conn, err := cfg.DialContext(ctx)
	if err != nil {
		var dial *websocket.DialError
		if errors.As(err, &dial) {
			return nil, fmt.Errorf("executor stream connection failed: %w", dial.Err)
		}
		return nil, errors.New("executor stream connection failed")
	}
	conn.PayloadType = websocket.BinaryFrame
	conn.MaxPayloadBytes = bridge.MaxFrame + 4
	return conn, nil
}
