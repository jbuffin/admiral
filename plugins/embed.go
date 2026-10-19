// Package plugins holds the order source plugins that ship inside the binary.
package plugins

import (
	"embed"
	"io/fs"
)

// Shipped holds every plugin Admiral ships, one folder each: plugins/freeform/
// and plugins/github/. Admiral writes each to $ADMIRAL_HOME/plugins/<name>/.
//
//go:embed freeform github
var Shipped embed.FS

// Names lists the folders of Shipped.
var Names = folders()

func folders() []string {
	entries, err := fs.ReadDir(Shipped, ".")
	if err != nil {
		panic(err) // the embed is built in
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}
