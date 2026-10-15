package runner

import (
	"fmt"
	contract "github.com/rclsilver/threavia/internal/backends/shared/runner"
	"time"
)

func codexProviderBlock(failure map[string]any) *contract.ProviderBlock {
	var code string
	if info, ok := failure["codexErrorInfo"].(string); ok {
		code = info
	}
	if code == "" {
		if raw, ok := failure["codexErrorInfo"].(map[string]any); ok {
			for _, value := range raw {
				if info, ok := value.(map[string]any); ok {
					if status, ok := info["httpStatusCode"].(float64); ok {
						code = fmt.Sprint(int(status))
					}
				}
			}
		}
	}
	if code == "" {
		if value, ok := failure["code"].(string); ok {
			code = value
		}
	}
	block := contract.ProviderBlockFor("codex", code, str(failure, "message"))
	if block != nil {
		block.RetryAt = contract.ProviderRetryAt(failure, time.Now())
	}
	return block
}
