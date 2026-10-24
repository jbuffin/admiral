package sessionhost

import "regexp"

// SafeToken is what an id, a type or a reason must match to be kept: short, and
// made of characters tmux and a format leave alone. Any process in a pane can
// set its options, so a reader re-checks what a writer already checked.
var SafeToken = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
