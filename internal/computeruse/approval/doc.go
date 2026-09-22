// Package approval coordinates app-control grants and revocation. File-backed
// stores share current authority across cooperating processes using a stable
// sidecar lock and atomic replacement. All writers sharing a file must use this
// format; older writers do not honor its locks or revocations.
//
// Session grants remain local but are invalidated by shared revocation records.
// Read errors deny access. A write error after replacement can mean a decision
// is visible but its durability is uncertain; callers must inspect current state
// rather than automatically repeating the decision. App approval is separate
// from operating-system permissions and target identity.
package approval
