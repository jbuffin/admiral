package cli

import "errors"

// Campaigns is a stub so the failing tests compile; it is replaced by the real command.
func Campaigns(args []string, std IO) error { return errors.New("campaigns: not implemented") }

// ShipNote is a stub so the failing tests compile; it is replaced by the real command.
func ShipNote(args []string, std IO) error { return nil }
