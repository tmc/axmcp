package cmdflag

import (
	"strconv"
	"strings"
)

// Bool returns the effective value of a boolean command-line flag.
//
// Recognized forms are:
//   - --flag
//   - --flag=true
//   - --flag=false
//   - --no-flag
func Bool(args []string, name string, def bool) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return def
	}
	negated := "--no-" + strings.TrimPrefix(name, "--")
	for _, arg := range args {
		switch {
		case arg == name:
			return true
		case arg == negated:
			return false
		case strings.HasPrefix(arg, name+"="):
			v, err := strconv.ParseBool(strings.TrimPrefix(arg, name+"="))
			if err == nil {
				return v
			}
		}
	}
	return def
}
