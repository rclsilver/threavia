// Command threavia-executor owns namespaced Kubernetes execution pods. It does
// not connect to Core or hold provider authentication credentials.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rclsilver/threavia/internal/execution/controller"
	"github.com/rclsilver/threavia/internal/execution/kube"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	showVersion := flag.Bool("version", false, "print version")
	file := flag.String("config", "/etc/threavia-executor/config.json", "administrator-owned configuration")
	listen := flag.String("listen", ":8443", "HTTPS listen address")
	cert := flag.String("tls-cert", "/etc/threavia-executor/tls/tls.crt", "TLS certificate")
	key := flag.String("tls-key", "/etc/threavia-executor/tls/tls.key", "TLS private key")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return nil
	}
	f, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer f.Close()
	var cfg controller.Config
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&cfg); err != nil {
		return err
	}
	apiURL := os.Getenv("THREAVIA_EXECUTOR_KUBERNETES_URL")
	if apiURL == "" {
		apiURL = "https://kubernetes.default.svc"
	}
	api, err := kube.NewClient(kube.APIConfig{URL: apiURL, Namespace: cfg.Namespace, CAFile: "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt", TokenFile: "/var/run/secrets/kubernetes.io/serviceaccount/token"})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c, err := controller.New(ctx, cfg, api)
	if err != nil {
		return err
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = c.Close(shutdown)
	}()
	server := &http.Server{Addr: *listen, Handler: c.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = c.Close(shutdown)
		_ = server.Shutdown(shutdown)
	}()
	boot, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err = c.Boot(boot); err != nil {
		return err
	}
	err = server.ListenAndServeTLS(*cert, *key)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
