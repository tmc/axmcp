// Package focusless reports the window server's focus and Space state.
//
// [Capture] reads which process the window server and NSWorkspace each
// consider frontmost, and which Space is active. [FocusedWindow] reports the
// window accessibility considers focused in a process. [IsOffSpace] reports
// whether a window lives on a Space other than the active one, which is when
// synthetic input aimed at it is delivered to whatever is showing instead.
//
// The mechanisms come from github.com/tmc/apple/x/skylight. All functions
// report [ErrUnavailable] when SkyLight cannot be reached, so a caller can
// degrade instead of failing.
package focusless
