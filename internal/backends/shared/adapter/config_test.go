package adapter

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexConfigurationIsIndependent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Paths set explicitly, as a deployed backend's environment does, are
	// shared on purpose: only the defaults are under test.
	for _, key := range []string{"STATE_PATH", "SKILL_CACHE_PATH", "SCRATCH_PATH"} {
		t.Setenv("THREAVIA_BACKEND_"+key, "")
	}
	t.Setenv("THREAVIA_BACKEND_CODEX_BINARY", "/opt/codex")
	t.Setenv("THREAVIA_BACKEND_CODEX_MODEL", "chosen-model")
	codex, err := LoadFor("codex")
	if err != nil {
		t.Fatal(err)
	}
	claude, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if codex.Client.BackendName != "codex" || codex.Provider.Binary != "/opt/codex" || codex.Provider.Model != "chosen-model" {
		t.Fatalf("wrong provider: %+v", codex.Provider)
	}
	if codex.StatePath == claude.StatePath || codex.Provider.SkillCachePath == claude.Provider.SkillCachePath || codex.Provider.ScratchPath == claude.Provider.ScratchPath {
		t.Fatal("providers share local state")
	}
	if !filepath.IsAbs(codex.Provider.ScratchPath) {
		t.Fatal("scratch directory must be absolute")
	}
	if !codex.Client.FeatureJobInputNext || !codex.Client.FeatureJobInputNow {
		t.Fatal("Codex message delivery was not advertised")
	}
	t.Setenv("HOME", "")
	codex, err = LoadFor("codex")
	if err != nil {
		t.Fatal(err)
	}
	if codex.Provider.ScratchPath != "" {
		t.Fatal("no home must disable the scratch directory")
	}
}

func TestCodexEnvironmentOverrides(t *testing.T) {
	t.Setenv("THREAVIA_BACKEND_STATE_PATH", "/tmp/codex-state.db")
	t.Setenv("THREAVIA_BACKEND_SKILL_CACHE_PATH", "/tmp/codex-skills")
	t.Setenv("THREAVIA_BACKEND_SCRATCH_PATH", "/tmp/codex-scratch")
	cfg, err := LoadFor("codex")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StatePath != "/tmp/codex-state.db" || cfg.Provider.SkillCachePath != "/tmp/codex-skills" || cfg.Provider.ScratchPath != "/tmp/codex-scratch" {
		t.Fatal("operator paths were replaced by defaults")
	}
}

// TestDefaultsAreUsable pins that a backend started with nothing but a
// registration token can actually reach Core and run work.
func TestDefaultsAreUsable(t *testing.T) {
	cfg := Default()

	if cfg.Registration.CoreAPI == "" {
		t.Error("a backend with no CORE_API configured must still know where to register")
	}
	if cfg.Provider.DefaultWorkingDirectory == "" {
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
		{"no executable", func(c *Config) { c.Provider.Binary = "" }, "CLAUDE_BINARY"},
		{"no default directory", func(c *Config) { c.Provider.DefaultWorkingDirectory = "" }, "DEFAULT_WORKING_DIRECTORY"},
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

// TestScratchLivesInTheAccountsHome pins where a Session's intermediate files
// go, which is not a question of taste.
//
// They are the files of the account the agent works as, made on its behalf, so
// they belong where that person would look for them and where no permission
// has to be arranged. A service state directory would mix what the backend
// owns with what the work produced, and ask the account for write access it
// has no other reason to want.
func TestScratchLivesInTheAccountsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	scratch := defaultScratchPath()
	if !strings.HasPrefix(scratch, home) {
		t.Fatalf("scratch = %q, want it under the account's home %q", scratch, home)
	}

	// With nowhere to put it the feature is off, rather than falling back to a
	// directory a read has to be approved out of.
	t.Setenv("HOME", "")
	if fallback := defaultScratchPath(); fallback != "" {
		t.Fatalf("with no home, scratch = %q, want none", fallback)
	}
}
