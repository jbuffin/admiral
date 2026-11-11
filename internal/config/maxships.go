package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

// DefaultMaxShips is the top-level max_ships cap when the file sets none.
const DefaultMaxShips = 3

func positiveInt(path string, raw json.RawMessage) (int, error) {
	n, err := strconv.Atoi(string(bytes.TrimSpace(raw)))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s: must be a positive integer, got %s", path, raw)
	}
	return n, nil
}
