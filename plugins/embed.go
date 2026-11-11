package plugins

import (
	"embed"
	"io/fs"
)

// Admiral writes each folder to $ADMIRAL_HOME/plugins/<name>/.
//
//go:embed github
var Shipped embed.FS

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
