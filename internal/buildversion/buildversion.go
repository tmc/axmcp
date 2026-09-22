// Package buildversion reports the version of the module a binary was built
// from, for use as an MCP server's advertised version.
package buildversion

import "runtime/debug"

// String returns the main module's version as the go command recorded it,
// such as "v0.3.0" for a binary installed with go install ...@v0.3.0, or
// "(devel)" for a build from a local checkout.
func String() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}
