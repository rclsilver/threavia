//go:build !linux

package worker

import "errors"

func harden() error                 { return errors.New("Kubernetes workers require Linux") }
func monitorCommand([]string) error { return errors.New("Kubernetes monitor commands require Linux") }
