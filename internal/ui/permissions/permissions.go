package permissions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/dispatch"
	"github.com/tmc/axmcp/internal/ui"
)

const requestSettlingTime = 3 * time.Second

type Requirement int

const (
	ReqAccessibility Requirement = iota
	ReqScreenRecording
)

type Status int

const (
	StatusUnknown Status = iota
	StatusGranted
	StatusDenied
	StatusMissing
	StatusStale
	StatusInProgress
)

type Event struct {
	Requirement Requirement
	Status      Status
	Detail      string
}

type Snapshot struct {
	AppName         string `json:"app_name,omitempty"`
	BundleID        string `json:"bundle_id,omitempty"`
	Accessibility   string `json:"accessibility"`
	ScreenRecording string `json:"screen_recording"`
	IdentityChanged bool   `json:"identity_changed"`
	IdentityDetail  string `json:"identity_detail,omitempty"`
	Pending         bool   `json:"pending"`
	Message         string `json:"message,omitempty"`
}

type identityRecord struct {
	BundleID    string    `json:"bundle_id"`
	AppName     string    `json:"app_name"`
	Executable  string    `json:"executable"`
	Fingerprint string    `json:"fingerprint"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type onboardingRow struct {
	requirement Requirement
	status      appkit.NSTextField
	action      appkit.NSButton
	detail      appkit.NSTextField
}

type onboardingWindow struct {
	win     appkit.NSWindow
	rows    []onboardingRow
	summary appkit.NSTextField
	reqs    []Requirement
}

var state struct {
	sync.RWMutex
	appName        string
	bundleID       string
	inProgress     map[Requirement]bool
	lastAttempt    map[Requirement]time.Time
	identityLoaded bool
	identityChange bool
	identityDetail string
}

func init() {
	state.inProgress = make(map[Requirement]bool)
	state.lastAttempt = make(map[Requirement]time.Time)
}

func ConfigureIdentity(appName, bundleID string) {
	state.Lock()
	defer state.Unlock()
	if strings.TrimSpace(appName) != "" {
		state.appName = strings.TrimSpace(appName)
	}
	if strings.TrimSpace(bundleID) != "" {
		state.bundleID = strings.TrimSpace(bundleID)
	}
}

func Check(r Requirement) Status {
	loadIdentityState()
	state.RLock()
	inProgress := state.inProgress[r]
	identityChanged := state.identityChange
	lastAttempt := state.lastAttempt[r]
	state.RUnlock()

	if requirementGranted(r) {
		return StatusGranted
	}
	if inProgress {
		return StatusInProgress
	}
	if identityChanged {
		return StatusStale
	}
	if !lastAttempt.IsZero() && time.Since(lastAttempt) > 3*time.Second {
		return StatusDenied
	}
	return StatusMissing
}

func Request(ctx context.Context, r Requirement) (Status, error) {
	markAttempt(r)
	setInProgress(r, true)
	defer setInProgress(r, false)

	switch r {
	case ReqAccessibility:
		dispatch.MainQueue().Async(func() {
			ui.RequestAccessibilityPermission()
		})
		OpenSystemSettings(r)
	case ReqScreenRecording:
		dispatch.MainQueue().Async(func() {
			ui.RequestScreenCapturePermission()
		})
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		status := requestOutcome(r)
		switch status {
		case StatusGranted:
			return status, nil
		case StatusStale:
			return status, nil
		case StatusMissing, StatusDenied:
			if time.Since(lastAttemptTime(r)) >= requestSettlingTime {
				if ctx.Err() != nil {
					return status, ctx.Err()
				}
				return status, nil
			}
		}
		select {
		case <-ctx.Done():
			return requestOutcome(r), ctx.Err()
		case <-ticker.C:
		}
	}
}

func Watch(ctx context.Context, r Requirement, ch chan<- Event) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	last := StatusUnknown
	for {
		status := Check(r)
		if status != last {
			detail := statusDetail(r, status)
			select {
			case ch <- Event{Requirement: r, Status: status, Detail: detail}:
				last = status
			case <-ctx.Done():
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func ResetAndRetry(r Requirement) error {
	service := serviceName(r)
	if service == "" {
		return fmt.Errorf("unsupported requirement")
	}
	ui.ResetTCC(service)
	if r == ReqScreenRecording {
		_ = OpenSystemSettings(r)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, err := Request(ctx, r)
	if err != nil && err != context.Canceled {
		return fmt.Errorf("reset and retry %s: %w", service, err)
	}
	return nil
}

func OpenSystemSettings(r Requirement) error {
	service := serviceName(r)
	if service == "" {
		return fmt.Errorf("unsupported requirement")
	}
	return exec.Command("open", ui.PrivacySettingsURL(service)).Run()
}

func CurrentSnapshot(reqs ...Requirement) Snapshot {
	loadIdentityState()
	if len(reqs) == 0 {
		reqs = []Requirement{ReqAccessibility, ReqScreenRecording}
	}
	seen := map[Requirement]bool{}
	for _, req := range reqs {
		seen[req] = true
	}
	s := Snapshot{
		AppName:         appName(),
		BundleID:        bundleID(),
		Accessibility:   statusName(Check(ReqAccessibility)),
		ScreenRecording: statusName(Check(ReqScreenRecording)),
	}
	state.RLock()
	s.IdentityChanged = state.identityChange
	s.IdentityDetail = state.identityDetail
	state.RUnlock()
	s.Pending = s.Accessibility != "granted" || (seen[ReqScreenRecording] && s.ScreenRecording != "granted")
	s.Message = snapshotMessage(s, seen)
	return s
}

func OnboardingWindow(ctx context.Context, reqs ...Requirement) error {
	if len(reqs) == 0 {
		reqs = []Requirement{ReqAccessibility, ReqScreenRecording}
	}
	ready := make(chan *onboardingWindow, 1)
	dispatch.MainQueue().Async(func() {
		ow := newOnboardingWindow(reqs)
		if ow.update() {
			ow.win.Close()
			ready <- nil
			return
		}
		ready <- ow
	})
	var ow *onboardingWindow
	select {
	case <-ctx.Done():
		return ctx.Err()
	case ow = <-ready:
		if ow == nil {
			return nil
		}
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			dispatch.MainQueue().Async(func() {
				ow.win.Close()
			})
			return ctx.Err()
		case <-ticker.C:
			result := make(chan bool, 1)
			dispatch.MainQueue().Async(func() {
				granted := ow.update()
				if granted {
					time.AfterFunc(900*time.Millisecond, func() {
						dispatch.MainQueue().Async(func() {
							ow.win.Close()
						})
					})
				}
				result <- granted
			})
			select {
			case <-ctx.Done():
				dispatch.MainQueue().Async(func() {
					ow.win.Close()
				})
				return ctx.Err()
			case granted := <-result:
				if granted {
					return nil
				}
			}
		}
	}
}

func newOnboardingWindow(reqs []Requirement) *onboardingWindow {
	app := appkit.GetNSApplicationClass().SharedApplication()
	app.SetActivationPolicy(appkit.NSApplicationActivationPolicyAccessory)

	const (
		width   = 560.0
		headerH = 88.0
		rowH    = 88.0
		footerH = 58.0
		pad     = 16.0
		buttonW = 158.0
		buttonH = 28.0
		statusW = 56.0
		colGap  = 10.0
	)
	height := headerH + footerH + rowH*float64(len(reqs))
	win := appkit.NewWindowWithContentRectStyleMaskBackingDefer(
		corefoundation.CGRect{Size: corefoundation.CGSize{Width: width, Height: height}},
		appkit.NSWindowStyleMaskTitled,
		appkit.NSBackingStoreBuffered,
		false,
	)
	win.SetTitle("")
	win.SetLevel(appkit.FloatingWindowLevel)
	win.SetHidesOnDeactivate(false)
	content := appkit.NSViewFromID(win.ContentView().GetID())
	fonts := appkit.GetNSFontClass()

	title := appkit.NewTextFieldLabelWithString(fmt.Sprintf("%s needs system permissions", appName()))
	title.SetFont(fonts.BoldSystemFontOfSize(14))
	title.SetUsesSingleLineMode(false)
	title.SetLineBreakMode(appkit.NSLineBreakByWordWrapping)
	title.SetMaximumNumberOfLines(2)
	title.SetPreferredMaxLayoutWidth(width - pad*2)
	title.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: pad, Y: height - 38},
		Size:   corefoundation.CGSize{Width: width - pad*2, Height: 24},
	})
	content.AddSubview(title)

	body := appkit.NewTextFieldLabelWithString("Grant access in System Settings. This window updates live while permissions change.")
	body.SetFont(fonts.SystemFontOfSize(11))
	body.SetUsesSingleLineMode(false)
	body.SetLineBreakMode(appkit.NSLineBreakByWordWrapping)
	body.SetMaximumNumberOfLines(2)
	body.SetPreferredMaxLayoutWidth(width - pad*2)
	body.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: pad, Y: height - 66},
		Size:   corefoundation.CGSize{Width: width - pad*2, Height: 32},
	})
	content.AddSubview(body)

	rows := make([]onboardingRow, 0, len(reqs))
	y := height - headerH - rowH
	for _, req := range reqs {
		actionX := width - pad - buttonW
		statusX := actionX - colGap - statusW
		textW := statusX - colGap - pad

		label := appkit.NewTextFieldLabelWithString(requirementTitle(req))
		label.SetFont(fonts.BoldSystemFontOfSize(12))
		label.SetUsesSingleLineMode(false)
		label.SetLineBreakMode(appkit.NSLineBreakByWordWrapping)
		label.SetMaximumNumberOfLines(1)
		label.SetFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: pad, Y: y + rowH - 32},
			Size:   corefoundation.CGSize{Width: textW, Height: 18},
		})
		content.AddSubview(label)

		detail := appkit.NewTextFieldLabelWithString("")
		detail.SetFont(fonts.SystemFontOfSize(11))
		detail.SetUsesSingleLineMode(false)
		detail.SetLineBreakMode(appkit.NSLineBreakByWordWrapping)
		detail.SetMaximumNumberOfLines(2)
		detail.SetPreferredMaxLayoutWidth(textW)
		detail.SetFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: pad, Y: y + 18},
			Size:   corefoundation.CGSize{Width: textW, Height: 34},
		})
		content.AddSubview(detail)

		status := appkit.NewTextFieldLabelWithString("")
		status.SetFont(fonts.SystemFontOfSize(11))
		status.SetAlignment(appkit.NSTextAlignmentRight)
		status.SetUsesSingleLineMode(false)
		status.SetFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: statusX, Y: y + rowH - 32},
			Size:   corefoundation.CGSize{Width: statusW, Height: 18},
		})
		content.AddSubview(status)

		reqCopy := req
		action := appkit.NewButtonWithFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: actionX, Y: y + rowH - 38},
			Size:   corefoundation.CGSize{Width: buttonW, Height: buttonH},
		})
		action.SetBezelStyle(appkit.NSBezelStylePush)
		action.SetActionHandler(func() {
			switch Check(reqCopy) {
			case StatusGranted:
				return
			case StatusStale:
				go ResetAndRetry(reqCopy)
			case StatusMissing, StatusDenied:
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
					defer cancel()
					if reqCopy == ReqAccessibility {
						_ = OpenSystemSettings(reqCopy)
					}
					_, _ = Request(ctx, reqCopy)
				}()
			default:
				_ = OpenSystemSettings(reqCopy)
			}
		})
		content.AddSubview(action)

		rows = append(rows, onboardingRow{
			requirement: req,
			status:      status,
			action:      action,
			detail:      detail,
		})
		y -= rowH
	}

	summary := appkit.NewTextFieldLabelWithString("")
	summary.SetFont(fonts.SystemFontOfSize(11))
	summary.SetUsesSingleLineMode(false)
	summary.SetLineBreakMode(appkit.NSLineBreakByWordWrapping)
	summary.SetMaximumNumberOfLines(2)
	summary.SetPreferredMaxLayoutWidth(width - pad*2)
	summary.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: pad, Y: 16},
		Size:   corefoundation.CGSize{Width: width - pad*2, Height: 28},
	})
	content.AddSubview(summary)

	win.Center()
	win.MakeKeyAndOrderFront(nil)
	app.Activate()

	return &onboardingWindow{
		win:     win,
		rows:    rows,
		summary: summary,
		reqs:    append([]Requirement(nil), reqs...),
	}
}

func (ow *onboardingWindow) update() bool {
	allGranted := true
	for _, row := range ow.rows {
		status := Check(row.requirement)
		row.status.SetStringValue(statusTitle(status))
		row.detail.SetStringValue(statusDetail(row.requirement, status))
		row.action.SetTitle(actionTitle(row.requirement, status))
		if status == StatusGranted {
			row.action.SetEnabled(false)
		} else {
			row.action.SetEnabled(true)
			allGranted = false
		}
	}
	snapshot := CurrentSnapshot(ow.reqs...)
	if snapshot.IdentityChanged && snapshot.IdentityDetail != "" {
		ow.summary.SetStringValue(snapshot.IdentityDetail)
	} else {
		ow.summary.SetStringValue(snapshot.Message)
	}
	return allGranted
}

func requirementGranted(r Requirement) bool {
	switch r {
	case ReqAccessibility:
		return ui.IsTrusted()
	case ReqScreenRecording:
		return ui.IsScreenRecordingTrusted()
	default:
		return false
	}
}

func serviceName(r Requirement) string {
	switch r {
	case ReqAccessibility:
		return "Accessibility"
	case ReqScreenRecording:
		return "ScreenCapture"
	default:
		return ""
	}
}

func requirementTitle(r Requirement) string {
	switch r {
	case ReqAccessibility:
		return "Accessibility"
	case ReqScreenRecording:
		return "Screen Recording"
	default:
		return "Unknown"
	}
}

func statusTitle(s Status) string {
	switch s {
	case StatusGranted:
		return "Granted"
	case StatusDenied:
		return "Denied"
	case StatusMissing:
		return "Missing"
	case StatusStale:
		return "Stale"
	case StatusInProgress:
		return "In progress"
	default:
		return "Unknown"
	}
}

func statusName(s Status) string {
	switch s {
	case StatusGranted:
		return "granted"
	case StatusDenied:
		return "denied"
	case StatusMissing:
		return "missing"
	case StatusStale:
		return "stale"
	case StatusInProgress:
		return "in_progress"
	default:
		return "unknown"
	}
}

func statusDetail(r Requirement, s Status) string {
	switch s {
	case StatusGranted:
		return "Permission is active."
	case StatusDenied:
		return "A request was made, but access has not been granted."
	case StatusMissing:
		if r == ReqAccessibility {
			return "Open System Settings and add or enable this app if no row appears."
		}
		return "Open System Settings and enable screen recording for this app."
	case StatusStale:
		return "The app identity changed since the last run. Reset and request again."
	case StatusInProgress:
		return "Waiting for macOS to register the new permission state."
	default:
		return ""
	}
}

func actionTitle(r Requirement, s Status) string {
	switch s {
	case StatusGranted:
		return "Granted"
	case StatusStale:
		return "Reset & Retry"
	case StatusDenied:
		return "Open Settings"
	case StatusMissing:
		if r == ReqAccessibility {
			return "Add to Settings"
		}
		return "Request Access"
	case StatusInProgress:
		return "Waiting..."
	default:
		return "Request"
	}
}

func snapshotMessage(s Snapshot, seen map[Requirement]bool) string {
	if !s.Pending {
		return "All required permissions are granted."
	}
	var missing []string
	if seen[ReqAccessibility] && s.Accessibility != "granted" {
		missing = append(missing, "Accessibility")
	}
	if seen[ReqScreenRecording] && s.ScreenRecording != "granted" {
		missing = append(missing, "Screen Recording")
	}
	return fmt.Sprintf("Waiting on %s.", strings.Join(missing, " and "))
}

func appName() string {
	state.RLock()
	defer state.RUnlock()
	if state.appName != "" {
		return state.appName
	}
	return "axmcp"
}

func bundleID() string {
	state.RLock()
	defer state.RUnlock()
	return state.bundleID
}

func setInProgress(r Requirement, v bool) {
	state.Lock()
	defer state.Unlock()
	state.inProgress[r] = v
}

func requestOutcome(r Requirement) Status {
	loadIdentityState()
	state.RLock()
	identityChanged := state.identityChange
	lastAttempt := state.lastAttempt[r]
	state.RUnlock()

	if requirementGranted(r) {
		return StatusGranted
	}
	if identityChanged {
		return StatusStale
	}
	if !lastAttempt.IsZero() && time.Since(lastAttempt) >= requestSettlingTime {
		return StatusDenied
	}
	return StatusMissing
}

func markAttempt(r Requirement) {
	state.Lock()
	defer state.Unlock()
	state.lastAttempt[r] = time.Now()
}

func lastAttemptTime(r Requirement) time.Time {
	state.RLock()
	defer state.RUnlock()
	return state.lastAttempt[r]
}

func loadIdentityState() {
	state.Lock()
	defer state.Unlock()
	if state.identityLoaded {
		return
	}
	current, err := currentIdentityRecord(state.appName, state.bundleID)
	if err == nil {
		if prev, err := readIdentityRecord(identityRecordPath(current.BundleID)); err == nil {
			if prev.Fingerprint != "" && prev.Fingerprint != current.Fingerprint {
				state.identityChange = true
				state.identityDetail = "App identity changed since the last run. Existing TCC grants may be stale."
			}
		}
		_ = writeIdentityRecord(identityRecordPath(current.BundleID), current)
	}
	state.identityLoaded = true
}

func currentIdentityRecord(appName, bundleID string) (identityRecord, error) {
	exe, err := os.Executable()
	if err != nil {
		return identityRecord{}, err
	}
	stat, err := os.Stat(exe)
	if err != nil {
		return identityRecord{}, err
	}
	cmd := exec.Command("codesign", "-dv", "--verbose=4", exe)
	out, _ := cmd.CombinedOutput()
	sum := sha256.Sum256([]byte(strings.Join([]string{
		exe,
		bundleID,
		appName,
		stat.ModTime().UTC().Format(time.RFC3339Nano),
		fmt.Sprint(stat.Size()),
		string(out),
	}, "\n")))
	return identityRecord{
		AppName:     appName,
		BundleID:    bundleID,
		Executable:  exe,
		Fingerprint: hex.EncodeToString(sum[:]),
		UpdatedAt:   time.Now().UTC(),
	}, nil
}

func identityRecordPath(bundleID string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	if bundleID == "" {
		bundleID = "dev.tmc.axmcp"
	}
	return filepath.Join(home, "Library", "Application Support", bundleID, "last_identity.json")
}

func readIdentityRecord(path string) (identityRecord, error) {
	var rec identityRecord
	if path == "" {
		return rec, fmt.Errorf("empty identity path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return rec, err
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		return rec, err
	}
	return rec, nil
}

func writeIdentityRecord(path string, rec identityRecord) error {
	if path == "" {
		return fmt.Errorf("empty identity path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
