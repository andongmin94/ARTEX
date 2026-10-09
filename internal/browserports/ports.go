// Package browserports defines HTTP ports accepted by the bundled Chromium.
package browserports

import (
	_ "embed"
	"encoding/json"
	"slices"
)

// The same embedded definition is packaged for Electron's ready validation.
// Update it from the pinned Chromium source when upgrading Electron; do not
// disable Chromium's restriction or permit blocked ports with command flags.
//
//go:embed restricted-ports.json
var definitionJSON []byte

var definition = func() struct{ Ports []int } {
	var value struct{ Ports []int }
	if err := json.Unmarshal(definitionJSON, &value); err != nil {
		panic(err)
	}
	if len(value.Ports) == 0 {
		panic("Chromium restricted port definition is empty")
	}
	return value
}()

// Allowed excludes unbound/invalid ports as well as Chromium's HTTP deny list.
func Allowed(port int) bool {
	return port > 0 && port <= 65535 && !slices.Contains(definition.Ports, port)
}

// BindAttempts permits every blocked port to be held once, plus a safe port.
// Held rejected listeners prevent the OS from repeatedly assigning one port.
func BindAttempts() int {
	return len(definition.Ports) + 1
}
