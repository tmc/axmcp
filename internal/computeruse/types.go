package computeruse

// AppInfo identifies a running macOS application.
type AppInfo struct {
	Name     string `json:"name,omitempty"`
	BundleID string `json:"bundle_id,omitempty"`
	PID      int    `json:"pid,omitempty"`
}

// WindowInfo describes the active target window for a state snapshot.
type WindowInfo struct {
	WindowID         uint32 `json:"window_id,omitempty"`
	Title            string `json:"title,omitempty"`
	X                int    `json:"x,omitempty"`
	Y                int    `json:"y,omitempty"`
	Width            int    `json:"width,omitempty"`
	Height           int    `json:"height,omitempty"`
	ScreenshotWidth  int    `json:"screenshot_width,omitempty"`
	ScreenshotHeight int    `json:"screenshot_height,omitempty"`
}

// ElementNode is an indexed AX node in a returned app state.
type ElementNode struct {
	Index            int      `json:"index"`
	ParentIndex      int      `json:"parent_index"`
	Role             string   `json:"role,omitempty"`
	Title            string   `json:"title,omitempty"`
	Value            string   `json:"value,omitempty"`
	Description      string   `json:"description,omitempty"`
	Identifier       string   `json:"identifier,omitempty"`
	X                int      `json:"x,omitempty"`
	Y                int      `json:"y,omitempty"`
	Width            int      `json:"width,omitempty"`
	Height           int      `json:"height,omitempty"`
	Enabled          bool     `json:"enabled"`
	Settable         bool     `json:"settable"`
	SecondaryActions []string `json:"secondary_actions,omitempty"`
}

// PermissionState reports the current system-permission status needed for
// computer-use actions.
type PermissionState struct {
	AccessibilityGranted   bool   `json:"accessibility_granted"`
	ScreenRecordingGranted bool   `json:"screen_recording_granted"`
	Pending                bool   `json:"pending"`
	Message                string `json:"message,omitempty"`
}

// ApprovalState reports whether control of an app has been approved.
type ApprovalState struct {
	Required   bool   `json:"required"`
	Approved   bool   `json:"approved"`
	Persistent bool   `json:"persistent"`
	Message    string `json:"message,omitempty"`
}

// AppState is the canonical snapshot returned by get_app_state.
type AppState struct {
	SessionID           string          `json:"session_id"`
	StateID             string          `json:"state_id"`
	App                 AppInfo         `json:"app"`
	Window              WindowInfo      `json:"window"`
	Tree                []ElementNode   `json:"tree"`
	ScreenshotPNGBase64 string          `json:"screenshot_png_base64,omitempty"`
	Instructions        string          `json:"instructions,omitempty"`
	Approval            ApprovalState   `json:"approval"`
	Permissions         PermissionState `json:"permissions"`
}

// ActionResult reports the outcome of a state-bound interaction.
type ActionResult struct {
	SessionID       string `json:"session_id,omitempty"`
	StateID         string `json:"state_id,omitempty"`
	Action          string `json:"action,omitempty"`
	Target          string `json:"target,omitempty"`
	Message         string `json:"message,omitempty"`
	RequiresRefresh bool   `json:"requires_refresh,omitempty"`
}

// InstructionProvider returns app-specific guidance for a snapshot.
type InstructionProvider interface {
	Instructions(app AppInfo) string
}

// ApprovalStore manages app-control approvals.
type ApprovalStore interface {
	Status(bundleID string) ApprovalState
	Approve(bundleID string, persistent bool) (ApprovalState, error)
}
