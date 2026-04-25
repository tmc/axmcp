// Package spacedetect reports whether a CGWindowID lives on a macOS Space
// other than the user's currently active Space.
//
// macOS does not expose Space membership through any public API. The detector
// resolves three private SkyLight symbols at runtime
// (SLSMainConnectionID, SLSGetActiveSpace, SLSCopySpacesForWindows) and
// reports off-Space residency as plain metadata. Cross-Space migration is
// out of scope: it requires a private WindowServer entitlement Apple does
// not grant outside its own processes.
//
// IsOffSpace takes the full uint32 CGWindowID range. The detector encodes
// it through int64 / KCFNumberSInt64Type to survive IDs greater than
// math.MaxInt32 without truncation; the lookup honors the same kCGSAllSpacesMask
// (=7) that yabai and cua-driver use, so transient, fullscreen, tiled, and
// other-user Spaces are all considered. A window with no Space membership
// (transient or system windows) returns an error rather than a false on-Space
// result.
//
// IsOffSpace returns errors that wrap ErrSkyLightUnavailable when the
// framework or any of the three symbols cannot be resolved. Callers must
// branch via errors.Is(err, ErrSkyLightUnavailable) — bare == comparison
// will silently miss every real error, since errors are joined with
// fmt.Errorf("%w: ...", ErrSkyLightUnavailable, cause).
//
// Usage:
//
//	off, err := spacedetect.IsOffSpace(windowID)
//	switch {
//	case errors.Is(err, spacedetect.ErrSkyLightUnavailable):
//	    // SkyLight not loaded; treat as unknown and continue.
//	case err != nil:
//	    log.Printf("spacedetect: %v", err)
//	case off:
//	    // Window lives on a non-active Space.
//	}
//
// On non-darwin builds the package compiles with a stub that returns
// ErrSkyLightUnavailable unconditionally, so importing this package keeps
// `go install ./...` clean from any host.
//
// This package is the smallest possible adoption of the SkyLight dlsym
// pattern; landing it derisks the binding workflow before the larger
// SLEventPostToPid and AXObserverAddNotificationAndCheckRemote adoptions
// tracked in ROADMAP.md.
package spacedetect
