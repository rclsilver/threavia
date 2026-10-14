package config

import "testing"

func TestMetricsConfiguration(t *testing.T) {
	cfg := Default()
	if cfg.Metrics.Enabled || cfg.Metrics.Addr != "127.0.0.1:9091" {
		t.Fatalf("defaults = %+v", cfg.Metrics)
	}
	t.Setenv("THREAVIA_METRICS_ENABLED", "true")
	t.Setenv("THREAVIA_METRICS_ADDR", ":9191")
	cfg, err := Load()
	if err != nil || !cfg.Metrics.Enabled || cfg.Metrics.Addr != ":9191" {
		t.Fatalf("configuration = %+v, %v", cfg.Metrics, err)
	}
	for _, addr := range []string{"", "invalid", cfg.HTTP.Addr, cfg.GRPC.Addr} {
		cfg.Metrics.Addr = addr
		if err := cfg.Validate(); err == nil {
			t.Errorf("accepted metrics address %q", addr)
		}
	}
}
