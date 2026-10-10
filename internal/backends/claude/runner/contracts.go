package runner

import "github.com/rclsilver/threavia/internal/backends/shared/runner"

// Both providers implement the same backend execution contract.
type StartParams = runner.StartParams
type Sink = runner.Sink
type Runner = runner.Runner

var ErrUnknownJob = runner.ErrUnknownJob
var ErrProviderUnavailable = runner.ErrProviderUnavailable

func lookPath(binary string) error { return runner.LookPath(binary) }
