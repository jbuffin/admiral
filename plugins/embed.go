// Package plugins holds the order source plugins that ship inside the binary.
package plugins

import "embed"

// Shipped holds every plugin Admiral ships, one folder each: plugins/freeform/
// and plugins/github/. Admiral writes each to $ADMIRAL_HOME/plugins/<name>/.
//
//go:embed freeform github
var Shipped embed.FS

// Names lists the folders of Shipped.
var Names = []string{"freeform", "github"}
