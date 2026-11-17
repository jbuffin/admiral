//go:build !e2e

package main

import "github.com/jbuffin/admiral/internal/moor"

func stackRemote(_ string, c moor.StackCheck) string { return "https://" + c.Host + "/" + c.Repo + ".git" }
