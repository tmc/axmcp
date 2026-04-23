package main

import (
	"os"
	"strings"
)

func envFlag(name string, def bool) bool {
	value, ok := os.LookupEnv(name)
	if !ok {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

func humanHighlightEnabled() bool {
	return envFlag("AXMCP_HIGHLIGHT_HUMAN", false)
}
