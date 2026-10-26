package monitors

import (
	"strings"
	"testing"
)

func TestSeatbeltPermissionsAndPathEscaping(t *testing.T) {
	for _, writable := range []bool{false, true} {
		for _, network := range []bool{false, true} {
			profile, err := seatbeltProfile(`/workspace/odd")(allow default);\\`, "/private/tmp/threavia-monitor-123", writable, network)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(profile, "(deny default)") || !strings.Contains(profile, "(allow signal (target same-sandbox))") {
				t.Fatal(profile)
			}
			if strings.Contains(profile, "(allow network*)") != network {
				t.Fatal("wrong network permission:", profile)
			}
			expected := `(subpath "/workspace/odd\")(allow default);\\\\")`
			if strings.Contains(profile, expected) != writable {
				t.Fatal("wrong write grant or unsafe path interpolation:", profile)
			}
			if !strings.Contains(profile, `(subpath "/private/tmp/threavia-monitor-123")`) {
				t.Fatal("no private scratch grant")
			}
		}
	}
	for _, path := range []string{"/bad\npath", "/bad\rpath", "/bad\x00path"} {
		if _, err := seatbeltProfile(path, "/tmp", true, true); err == nil {
			t.Fatal("unsafe path accepted")
		}
		if _, err := seatbeltProfile("/cwd", path, true, true); err == nil {
			t.Fatal("unsafe temporary path accepted")
		}
	}
}

func TestMonitorEnvironmentDropsStartupAndLoaderInjection(t *testing.T) {
	for _, name := range []string{"THREAVIA_TOKEN", "DYLD_INSERT_LIBRARIES", "DYLD_LIBRARY_PATH", "BASH_ENV", "ENV", "LD_PRELOAD", "LD_LIBRARY_PATH"} {
		t.Setenv(name, "must-not-inherit")
	}
	t.Setenv("MONITOR_NORMAL_VARIABLE", "preserved")
	joined := strings.Join(monitorEnvironment(), "\n")
	if strings.Contains(joined, "must-not-inherit") {
		t.Fatal("unsafe environment inherited")
	}
	if !strings.Contains(joined, "MONITOR_NORMAL_VARIABLE=preserved") {
		t.Fatal("ordinary environment lost")
	}
}
