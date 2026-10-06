// Package envutil provides the small environment-variable helpers used by the
// Core and backend configuration loaders.
//
// It deliberately has no dependencies and no magic: every configuration field is
// read explicitly by the package that owns it.
package envutil

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Loader reads environment variables under a common prefix and accumulates
// parsing errors instead of failing on the first one, so a misconfigured
// deployment reports every problem at once.
type Loader struct {
	prefix string
	errs   []error
}

// NewLoader returns a Loader reading variables named prefix + key.
func NewLoader(prefix string) *Loader {
	return &Loader{prefix: prefix}
}

// Err returns all accumulated parsing errors, or nil.
func (l *Loader) Err() error {
	if len(l.errs) == 0 {
		return nil
	}
	msgs := make([]string, 0, len(l.errs))
	for _, err := range l.errs {
		msgs = append(msgs, err.Error())
	}
	return fmt.Errorf("invalid configuration: %s", strings.Join(msgs, "; "))
}

// Name returns the full environment variable name for key.
func (l *Loader) Name(key string) string { return l.prefix + key }

func (l *Loader) lookup(key string) (string, bool) {
	raw, ok := os.LookupEnv(l.Name(key))
	if !ok {
		return "", false
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	return raw, true
}

func (l *Loader) fail(key string, raw string, err error) {
	l.errs = append(l.errs, fmt.Errorf("%s=%q: %w", l.Name(key), raw, err))
}

// String returns the value of key, or def when unset or empty.
func (l *Loader) String(key, def string) string {
	if raw, ok := l.lookup(key); ok {
		return raw
	}
	return def
}

// Bool parses a boolean value, or returns def when unset or empty.
func (l *Loader) Bool(key string, def bool) bool {
	raw, ok := l.lookup(key)
	if !ok {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		l.fail(key, raw, err)
		return def
	}
	return v
}

// Int parses an integer value, or returns def when unset or empty.
func (l *Loader) Int(key string, def int) int {
	raw, ok := l.lookup(key)
	if !ok {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		l.fail(key, raw, err)
		return def
	}
	return v
}

// Int64 parses a 64-bit integer value, or returns def when unset or empty.
func (l *Loader) Int64(key string, def int64) int64 {
	raw, ok := l.lookup(key)
	if !ok {
		return def
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		l.fail(key, raw, err)
		return def
	}
	return v
}

// Duration parses a Go duration such as "30s", or returns def when unset.
func (l *Loader) Duration(key string, def time.Duration) time.Duration {
	raw, ok := l.lookup(key)
	if !ok {
		return def
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		l.fail(key, raw, err)
		return def
	}
	return v
}

// StringSlice parses a comma-separated list, trimming empty entries. It returns
// def when unset or empty.
func (l *Loader) StringSlice(key string, def []string) []string {
	raw, ok := l.lookup(key)
	if !ok {
		return def
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}

// StringMap parses a comma-separated list of key=value pairs. It returns nil
// when unset or empty.
func (l *Loader) StringMap(key string) map[string]string {
	raw, ok := l.lookup(key)
	if !ok {
		return nil
	}
	out := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, value, found := strings.Cut(pair, "=")
		if !found {
			l.fail(key, raw, fmt.Errorf("entry %q is not in key=value form", pair))
			return nil
		}
		out[strings.TrimSpace(name)] = strings.TrimSpace(value)
	}
	return out
}

// Invalid records a validation error discovered by the caller after parsing.
func (l *Loader) Invalid(key string, err error) {
	l.errs = append(l.errs, fmt.Errorf("%s: %w", l.Name(key), err))
}
