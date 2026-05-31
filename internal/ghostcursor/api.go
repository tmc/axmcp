//go:build darwin

package ghostcursor

import (
	"context"
	"errors"
	"os"
	"runtime"
	"time"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
)

var ErrMoveAborted = errors.New("ghost cursor move aborted")

// ActivityState describes the current visual state of the cursor.
type ActivityState int

const (
	ActivityIdle ActivityState = iota
	ActivityMoving
	ActivityPressed
	ActivityDragging
	ActivityTyping
	ActivityThinking
	ActivityPaused
)

// CoordinateSpace identifies how a position should be interpreted.
type CoordinateSpace int

const (
	CoordinateSpaceUnknown CoordinateSpace = iota
	CoordinateSpaceScreen
)

// Position identifies a cursor location in a specific coordinate space.
type Position struct {
	Space     CoordinateSpace
	DisplayID uint32
	X         float64
	Y         float64
}

// CurveStyle controls how sampled motion progresses toward a target.
type CurveStyle int

const (
	// CurveBezier is the default. Its zero value keeps existing callers on the
	// curved path without additional configuration.
	CurveBezier CurveStyle = iota
	CurveEaseInOut
	CurveLinear
)

// MoveOptions controls a blocking cursor movement.
type MoveOptions struct {
	Duration        time.Duration
	Activity        ActivityState
	HideAfter       time.Duration
	CurveStyle      CurveStyle
	Strength        float64
	Jitter          float64
	NextInteraction NextInteractionTiming
}

// NextInteractionTiming controls when MoveTo may return before the animation
// fully completes so input can be issued once the cursor is close enough.
type NextInteractionTiming struct {
	DistancePx      float64
	Progress        float64
	IdleVelocityPPS float64
	Dwell           time.Duration
	MaxWait         time.Duration
}

type Config struct {
	Enabled  bool
	Eyecandy EyecandyConfig
	Theme    Theme
	Tuning   TuningConfig
}

type EyecandyConfig struct {
	SharingVisible bool
	RippleOnClick  bool
	CometTrail     bool
	VelocityTilt   bool
	HolographicOCR bool
	LiquidLens     bool
}

type TuningConfig struct {
	Brightness     float64
	CursorScale    float64
	BodyOpacity    float64
	OutlineOpacity float64
	GlowOpacity    float64
	GlowScale      float64
	IdleFadeDelay  time.Duration
	IdleFadeTime   time.Duration
	MoveGlowTime   time.Duration
}

// Theme selects a cursor palette family.
type Theme int

const (
	ThemeAuto Theme = iota
	ThemeCodex
	ThemeClaude
	ThemeNeutral
)

// Info describes the detected harness and selected cursor colors.
type Info struct {
	Harness      string
	MatchName    string
	MatchPID     int
	PaletteID    int
	PaletteIndex int
	DotColor     string
	BorderColor  string
}

var defaultController = New(Config{
	Enabled: true,
	Eyecandy: EyecandyConfig{
		SharingVisible: true,
	},
})

func Default() *Controller {
	return defaultController
}

func DefaultEyecandyConfig() EyecandyConfig {
	return EyecandyConfig{
		SharingVisible: true,
		RippleOnClick:  true,
		CometTrail:     true,
		VelocityTilt:   true,
	}
}

func DefaultTuningConfig() TuningConfig {
	return TuningConfig{
		Brightness:     2.5,
		CursorScale:    1.1,
		BodyOpacity:    1.2,
		OutlineOpacity: 1.3,
		GlowOpacity:    1.5,
		GlowScale:      1.2,
		IdleFadeTime:   idleDimTime,
		MoveGlowTime:   moveGlowTime,
	}
}

// DetectInfo reports the detected host harness and selected palette.
func DetectInfo() Info {
	return detectInfo(os.Getpid)
}

func Configure(cfg Config) {
	defaultController.Configure(cfg)
}

func Enabled() bool {
	return defaultController.Enabled()
}

func ScreenPosition(x, y int) Position {
	return Position{
		Space: CoordinateSpaceScreen,
		X:     float64(x),
		Y:     float64(y),
	}
}

func TypingPositionForFrame(x, y, width, height float64) Position {
	offset := width / 8
	switch {
	case offset < 2:
		offset = 2
	case offset > 12:
		offset = 12
	}
	return Position{
		Space: CoordinateSpaceScreen,
		X:     x + offset,
		Y:     y + height/2,
	}
}

func HoverAt(x, y int) {
	_ = defaultController.Show(ScreenPosition(x, y), ActivityIdle, hoverHideDelay)
}

// SettleAt moves the ghost cursor toward x, y and returns once the
// next-interaction timing gate says the cursor is close and slow enough for
// the real input event to be posted.
func SettleAt(x, y int, duration time.Duration) {
	if duration <= 0 {
		_ = defaultController.Show(ScreenPosition(x, y), ActivityMoving, 0)
		return
	}
	_ = defaultController.MoveTo(context.Background(), ScreenPosition(x, y), MoveOptions{
		Duration:   duration,
		Activity:   ActivityMoving,
		CurveStyle: CurveBezier,
		NextInteraction: NextInteractionTiming{
			DistancePx:      2,
			Progress:        0.95,
			IdleVelocityPPS: 40,
			Dwell:           40 * time.Millisecond,
			MaxWait:         duration,
		},
	})
}

func PressAt(x, y int) {
	_ = defaultController.Show(ScreenPosition(x, y), ActivityPressed, 0)
}

func DragTo(x, y int) {
	_ = defaultController.Show(ScreenPosition(x, y), ActivityDragging, 0)
}

func ReleaseAt(x, y int) {
	_ = defaultController.Show(ScreenPosition(x, y), ActivityIdle, releaseHideDelay)
}

func Hide() {
	defaultController.Hide()
}

func OverlaySharingType() appkit.NSWindowSharingType {
	return defaultController.overlaySharingType()
}

func FlashCaptureRect(rect corefoundation.CGRect) {
	if rect.Size.Width <= 0 || rect.Size.Height <= 0 {
		return
	}
	p := defaultController.palette
	go flashRect(rect, p, captureFlashTime)
}

func normalizeEyecandy(cfg EyecandyConfig) EyecandyConfig {
	if runtime.GOARCH == "amd64" {
		cfg.HolographicOCR = false
		cfg.LiquidLens = false
	}
	if cfg.LiquidLens {
		cfg.LiquidLens = false
	}
	return cfg
}

func normalizeTuning(cfg TuningConfig) TuningConfig {
	defaults := DefaultTuningConfig()
	if cfg == (TuningConfig{}) {
		return defaults
	}
	if cfg.Brightness <= 0 {
		cfg.Brightness = defaults.Brightness
	}
	if cfg.CursorScale <= 0 {
		cfg.CursorScale = defaults.CursorScale
	}
	if cfg.BodyOpacity <= 0 {
		cfg.BodyOpacity = defaults.BodyOpacity
	}
	if cfg.OutlineOpacity <= 0 {
		cfg.OutlineOpacity = defaults.OutlineOpacity
	}
	if cfg.GlowOpacity <= 0 {
		cfg.GlowOpacity = defaults.GlowOpacity
	}
	if cfg.GlowScale <= 0 {
		cfg.GlowScale = defaults.GlowScale
	}
	if cfg.IdleFadeDelay < 0 {
		cfg.IdleFadeDelay = 0
	}
	if cfg.IdleFadeTime <= 0 {
		cfg.IdleFadeTime = defaults.IdleFadeTime
	}
	if cfg.MoveGlowTime <= 0 {
		cfg.MoveGlowTime = defaults.MoveGlowTime
	}
	return cfg
}
