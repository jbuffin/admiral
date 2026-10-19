// Package plugins holds the order source plugins that ship inside the binary.
package plugins

import "embed"

// Freeform is the freeform plugin's folder, as plugins/freeform/. Admiral
// writes it to $ADMIRAL_HOME/plugins/freeform/ on a fresh install.
//
//go:embed freeform
var Freeform embed.FS
