// Package skylightinput posts synthetic mouse events to a target process via
// SkyLight's per-pid SPI, bypassing the public CGEvent HID-tap pipeline.
//
// Three problems this package solves that CGEventPost cannot:
//
//  1. Receivers that filter events arriving on kCGHIDEventTap as untrusted
//     (Chromium renderers, iPhone Mirroring's input filter, Slack/Discord
//     and other Electron apps in some configurations). Posting through
//     SLEventPostToPid lands events on a SkyLight-trust-envelope channel
//     those filters accept.
//
//  2. Cursor-warp-on-click. CGEventPost(kCGHIDEventTap, ...) updates the
//     real on-screen cursor. The SkyLight per-pid path delivers to the
//     target's mach port without moving the user's pointer.
//
//  3. Focus-on-click. Activating a target via NSRunningApplication.activate
//     or SLPSSetFrontProcessWithOptions raises the window AND on multi-Space
//     setups follows the user across Spaces. The yabai pattern of two
//     SLPSPostEventRecordTo calls (defocus prev, focus target) flips the
//     AppKit-active state without raising or Space-following.
//
// Recipe lifted from trycua's cua-driver, documented at
// https://github.com/trycua/cua/blob/main/blog/inside-macos-window-internals.md
// and implemented in Swift at libs/cua-driver/Sources/CuaDriverCore/Input/
// {SkyLightEventPost,FocusWithoutRaise,MouseInput}.swift.
//
// Status: this package speaks to private Apple SPIs that are not in any
// public header and are not API-stable. Symbols are resolved lazily; if any
// SPI is missing on the running OS, the corresponding helper returns
// ErrUnavailable and callers should fall back to CGEventPost.
package skylightinput
