package adapter

import (
	"strings"
	"testing"
)

// TestDefaultsAreUsable pins that a backend started with nothing but a
// registration token can actually reach Core and run work.
func TestDefaultsAreUsable(t *testing.T) {
	cfg := Default()

	if cfg.Registration.CoreAPI == "" {
		t.Error("a backend with no CORE_API configured must still know where to register")
	}
	if cfg.Claude.DefaultWorkingDirectory == "" {
		t.Error("a session with no working directory must land somewhere chosen, not inherited")
	}
	if cfg.StatePath == "" {
		t.Error("the durable local state needs a path")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the defaults must validate: %v", err)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"defaults", func(*Config) {}, ""},
		{"unknown log level", func(c *Config) { c.Log.Level = "trace" }, "LOG_LEVEL"},
		{"no state path", func(c *Config) { c.StatePath = "" }, "STATE_PATH"},
		{"no executable", func(c *Config) { c.Claude.Binary = "" }, "CLAUDE_BINARY"},
		{"no default directory", func(c *Config) { c.Claude.DefaultWorkingDirectory = "" }, "DEFAULT_WORKING_DIRECTORY"},
		{
			"registration token without a core api",
			func(c *Config) { c.Registration.Token = "t"; c.Registration.CoreAPI = "" },
			"CORE_API",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			tc.mutate(&cfg)

			err := cfg.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want no error", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want an error mentioning %s", err, tc.want)
			}
		})
	}
}
