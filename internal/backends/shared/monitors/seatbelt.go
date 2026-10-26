package monitors

import (
	"fmt"
	"strings"
)

// seatbeltProfile is generated from canonical paths supplied by the launcher.
// Commands and descriptions are never interpolated into the policy language.
func seatbeltProfile(dir, temporary string, writable, network bool) (string, error) {
	quote := func(s string) (string, error) {
		if strings.ContainsAny(s, "\x00\r\n") {
			return "", fmt.Errorf("sandbox paths must not contain control characters")
		}
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`, nil
	}
	cwd, err := quote(dir)
	if err != nil {
		return "", err
	}
	tmp, err := quote(temporary)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(`(version 1)
(deny default)
(allow file-read*)
(allow process-exec process-fork)
(allow signal (target same-sandbox))
(allow process-info* (target same-sandbox))
(allow sysctl-read)
(allow mach-lookup (global-name "com.apple.system.opendirectoryd.libinfo"))
(allow file-write-data (literal "/dev/null"))
`)
	fmt.Fprintf(&b, "(allow file-write* (subpath %s))\n", tmp)
	if writable {
		fmt.Fprintf(&b, "(allow file-write* (subpath %s))\n", cwd)
	}
	if network {
		b.WriteString(`(allow network*)
(allow mach-lookup
 (global-name "com.apple.SecurityServer")
 (global-name "com.apple.networkd")
 (global-name "com.apple.ocspd")
 (global-name "com.apple.trustd.agent")
 (global-name "com.apple.TrustEvaluationAgent")
 (global-name "com.apple.SystemConfiguration.configd")
 (global-name "com.apple.mDNSResponder"))
`)
	}
	return b.String(), nil
}
