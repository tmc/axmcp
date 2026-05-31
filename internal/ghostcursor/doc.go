//go:build darwin

// Package ghostcursor draws a transient overlay cursor for pointer actions.
// Animated moves follow a deterministic Bezier arc so drags read as motion
// instead of teleports.
//
// The package is built against the repository's default go.work workspace,
// which uses the sibling github.com/tmc/apple checkout. That checkout's
// CoreGraphics event-tap callback uses kernel.Pointer for userInfo; the
// published v0.6.9 module still uses unsafe.Pointer, so GOWORK=off is not the
// compatibility target for this package until the module dependency is updated.
package ghostcursor
