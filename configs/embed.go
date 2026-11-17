// Package configs embeds the default configuration files so the simulator
// runs without any external file. The YAML files here are also the
// documented examples.
package configs

import (
	"embed"
)

// DefaultRegisterMap is register-map.yaml.
//
//go:embed register-map.yaml
var DefaultRegisterMap []byte

// Scenarios holds the built-in scenarios (scenarios/<name>.yaml).
//
//go:embed scenarios/*.yaml
var Scenarios embed.FS
