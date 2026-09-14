// Package buildinfo holds offline provenance and compiled channel defaults.
package buildinfo

import (
	"fmt"
	"strings"
)

// These strings may be set with go build -ldflags -X. An empty provenance field
// means unknown, not evidence that the binary matches a running backend.
var (
	Channel      = "live"
	BackendSHA   = "unknown"
	SchemaSHA256 = "unknown"
	APIVersion   = "unknown"
	SourceSHA256 = "unknown"
	SourceDirty  = "unknown"
)

const (
	LiveURL     = "https://console.jetscale.ai"
	LocalURL    = "https://jetscale.internal"
	UnstableURL = "https://unstable.jetscale.ai"
)

// Validate fails closed rather than interpreting an unknown channel as live.
func Validate() error {
	switch Channel {
	case "live", "local", "unstable":
		return nil
	default:
		return fmt.Errorf("invalid compiled build channel %q; rebuild with live, local, or unstable", Channel)
	}
}

func DefaultInstance() string {
	switch Channel {
	case "live":
		return "production"
	case "local", "unstable":
		return Channel
	default:
		return ""
	}
}

func DefaultURL() string {
	switch Channel {
	case "live":
		return LiveURL
	case "local":
		return LocalURL
	case "unstable":
		return UnstableURL
	default:
		return ""
	}
}

// EnvVar keeps the legacy live environment contract, but never reads generic
// endpoint, config, or account overrides in a non-live build.
func EnvVar(suffix string) string {
	if Channel == "live" {
		return "JETSCALE_" + suffix
	}
	return "JETSCALE_" + strings.ToUpper(Channel) + "_" + suffix
}

func BinaryName() string {
	if Channel == "live" {
		return "jetscale"
	}
	return "jetscale-" + Channel
}
