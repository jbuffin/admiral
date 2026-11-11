package sessionhost

import "regexp"

// Any process in a pane can set its options, so a reader re-checks what a writer already checked.
var SafeToken = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
