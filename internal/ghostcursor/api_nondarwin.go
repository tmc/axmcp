//go:build !darwin

// Package ghostcursor provides no-op cursor feedback on non-Darwin systems.
package ghostcursor

import (
	"context"
	"errors"
	"fmt"
	"time"
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

// NextInteractionTiming controls when MoveTo may return before completion.
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

// Info describes the detected host harness and selected cursor colors.
type Info struct {
	Harness      string
	MatchName    string
	MatchPID     int
	PaletteID    int
	PaletteIndex int
	DotColor     string
	BorderColor  string
}

// Controller is a no-op cursor controller on non-Darwin systems.
type Controller struct {
	enabled bool
}

var defaultController = &Controller{}

func Default() *Controller {
	return defaultController
}

func DefaultEyecandyConfig() EyecandyConfig {
	return EyecandyConfig{SharingVisible: true}
}

func DefaultTuningConfig() TuningConfig {
	return TuningConfig{
		Brightness:     2.5,
		CursorScale:    1.1,
		BodyOpacity:    1.2,
		OutlineOpacity: 1.3,
		GlowOpacity:    1.5,
		GlowScale:      1.2,
	}
}

// DetectInfo reports an empty host harness on non-Darwin systems.
func DetectInfo() Info {
	return Info{}
}

func Configure(cfg Config) {
	defaultController.Configure(cfg)
}

func Enabled() bool {
	return defaultController.Enabled()
}

func ScreenPosition(x, y int) Position {
	return Position{Space: CoordinateSpaceScreen, X: float64(x), Y: float64(y)}
}

func TypingPositionForFrame(x, y, width, height float64) Position {
	offset := width / 8
	switch {
	case offset < 2:
		offset = 2
	case offset > 12:
		offset = 12
	}
	return Position{Space: CoordinateSpaceScreen, X: x + offset, Y: y + height/2}
}

func HoverAt(int, int)                 {}
func SettleAt(int, int, time.Duration) {}
func PressAt(int, int)                 {}
func DragTo(int, int)                  {}
func ReleaseAt(int, int)               {}
func Hide()                            { defaultController.Hide() }

func New(cfg Config) *Controller {
	c := &Controller{}
	c.Configure(cfg)
	return c
}

func (c *Controller) Close() {}

func (c *Controller) Configure(cfg Config) {
	c.enabled = cfg.Enabled
}

func (c *Controller) Enabled() bool {
	return c != nil && c.enabled
}

func (c *Controller) Show(Position, ActivityState, time.Duration) error {
	return nil
}

func (c *Controller) MoveTo(ctx context.Context, pos Position, _ MoveOptions) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if pos.Space != CoordinateSpaceScreen {
		return fmt.Errorf("unsupported coordinate space")
	}
	return nil
}

func (c *Controller) Hide() {}

// SamplePath returns a minimal path suitable for non-Darwin compile-time use.
func SamplePath(start, end Position, _ MoveOptions) ([]Position, error) {
	if start.Space != CoordinateSpaceScreen || end.Space != CoordinateSpaceScreen {
		return nil, fmt.Errorf("unsupported coordinate space")
	}
	if start == end {
		return []Position{start}, nil
	}
	return []Position{start, end}, nil
}

// RenderColor describes an sRGB color for offscreen cursor rendering.
type RenderColor struct {
	Red   float64
	Green float64
	Blue  float64
	Alpha float64
}

// RenderOptions controls offscreen cursor rendering.
type RenderOptions struct {
	Width      int
	Height     int
	Scale      float64
	Activity   ActivityState
	Theme      Theme
	Background RenderColor
}

// RenderPNG reports that cursor rendering is unavailable off Darwin.
func RenderPNG(RenderOptions) ([]byte, error) {
	return nil, fmt.Errorf("ghost cursor rendering is not available on this platform")
}
