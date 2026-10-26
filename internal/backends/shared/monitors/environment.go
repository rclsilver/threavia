package monitors

import (
	"os"
	"strings"
)

func monitorEnvironment() []string {
	var env []string
	for _, v := range os.Environ() {
		name, _, _ := strings.Cut(v, "=")
		if name != "BASH_ENV" && name != "ENV" && name != "LD_PRELOAD" && name != "LD_LIBRARY_PATH" && !strings.HasPrefix(name, "DYLD_") && !strings.HasPrefix(name, "THREAVIA_") {
			env = append(env, v)
		}
	}
	return env
}
