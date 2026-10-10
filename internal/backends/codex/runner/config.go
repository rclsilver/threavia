package runner

import (
	"os"
	"path/filepath"
)

// Rules can exist without a config.toml layer. Scan both the provider home and
// project ancestors, conservatively retaining the host gate if any are found.
func nativeRulesPresent(cwd string) (bool, error) {
	directories := []string{}
	if root := os.Getenv("CODEX_HOME"); root != "" {
		directories = append(directories, root)
	} else if home, err := os.UserHomeDir(); err == nil {
		directories = append(directories, filepath.Join(home, ".codex"))
	}
	for dir := cwd; dir != ""; dir = filepath.Dir(dir) {
		directories = append(directories, filepath.Join(dir, ".codex"))
		if filepath.Dir(dir) == dir {
			break
		}
	}
	for _, dir := range directories {
		found, err := hasNativeRules(dir)
		if err != nil || found {
			return found, err
		}
	}
	return false, nil
}
