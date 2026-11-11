package launcher

import "github.com/jbuffin/admiral/internal/config"

// Placeholders until the gate and flags exist.

func uiServerSupported(bin string) bool { return false }

func uiServerFlags(req Request, dock config.Harness, port int) []string { return nil }
