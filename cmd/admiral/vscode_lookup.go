//go:build !e2e

package main

import "github.com/jbuffin/admiral/internal/editors"

func vscodeInstalled() bool { return editors.VSCodeInstalled() }
