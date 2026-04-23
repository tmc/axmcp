package ghostcursor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objectivec"
	"github.com/tmc/apple/quartzcore"
	"golang.org/x/sys/unix"
)

const (
	windowSize       = 56.0
	hoverHideDelay   = 700 * time.Millisecond
	releaseHideDelay = 320 * time.Millisecond
	minFrameInterval = 5 * time.Millisecond
	captureFlashTime = 220 * time.Millisecond
	moveGlowTime     = 180 * time.Millisecond
	idleDimTime      = 240 * time.Millisecond
	pausedFadeTime   = 180 * time.Millisecond
)

const (
	eventTapOptionListenOnly  coregraphics.CGEventTapOptions = 1
	userInterventionCooldown                                 = 150 * time.Millisecond
	userInterventionHideDelay                                = 900 * time.Millisecond
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

type Controller struct {
	mu         sync.Mutex
	enabled    bool
	seq        atomic.Uint64
	swaySeq    atomic.Uint64
	visualSeq  atomic.Uint64
	current    Position
	hasCursor  bool
	visible    bool
	activity   ActivityState
	palette    palette
	eyecandy   EyecandyConfig
	theme      Theme
	tuning     TuningConfig
	lastRipple time.Time
	lastAngle  float64
	tiltReady  bool

	win     appkit.NSWindow
	fog     appkit.NSVisualEffectView
	fogMask quartzcore.CAShapeLayer
	aura    quartzcore.CAShapeLayer
	dot     quartzcore.CAShapeLayer
	halo    quartzcore.CAShapeLayer
	root    quartzcore.CALayer
	trail   quartzcore.CAEmitterLayer
	cache   map[ActivityState]cursorSpriteSet

	spaceObserver     objectivec.Object
	screenObserver    objectivec.Object
	occlusionObserver objectivec.Object
	interventionTap   corefoundation.CFMachPortRef
	interventionSrc   corefoundation.CFRunLoopSourceRef
	interventionID    uintptr
	lastIntervention  atomic.Int64
	moveGlowStarted   atomic.Int64
}

type cursorTokens struct {
	bodyScale       float64
	bodyAlpha       float64
	outlineWidth    float64
	outlineAlpha    float64
	fogScale        float64
	fogAlpha        float64
	fogShadowAlpha  float64
	fogShadowBlur   float64
	bodyShadowAlpha float64
	bodyShadowBlur  float64
	motionScale     float64
}

type cursorVisualState struct {
	spriteActivity ActivityState
	tokens         cursorTokens
	bodyRed        float64
	bodyGreen      float64
	bodyBlue       float64
	outlineRed     float64
	outlineGreen   float64
	outlineBlue    float64
	fogRed         float64
	fogGreen       float64
	fogBlue        float64
}

type palette struct {
	dotRed      float64
	dotGreen    float64
	dotBlue     float64
	haloRed     float64
	haloGreen   float64
	haloBlue    float64
	borderRed   float64
	borderGreen float64
	borderBlue  float64
}

type processRecord struct {
	pid  int
	ppid int
	name string
}

type harnessInfo struct {
	pid       int
	name      string
	kind      hostKind
	paletteID int
}

type moveGate struct {
	enabled         bool
	distancePx      float64
	progress        float64
	idleVelocityPPS float64
	dwell           time.Duration
	maxWait         time.Duration
}

type moveGateState struct {
	startedAt  time.Time
	dwellStart time.Time
	signaled   bool
}

type hostKind int

const (
	hostUnknown hostKind = iota
	hostClaude
	hostCodex
)

var codexPalettes = []palette{
	colorPalette(0.87, 0.93, 1.00, 0.58, 0.77, 1.00),
	colorPalette(0.84, 0.95, 1.00, 0.48, 0.80, 0.98),
	colorPalette(0.90, 0.96, 1.00, 0.63, 0.82, 1.00),
	colorPalette(0.82, 0.91, 1.00, 0.52, 0.73, 0.98),
}

var claudePalettes = []palette{
	colorPalette(1.00, 0.44, 0.08, 1.00, 0.55, 0.18),
	colorPalette(0.97, 0.51, 0.14, 1.00, 0.63, 0.24),
	colorPalette(0.93, 0.41, 0.21, 0.99, 0.53, 0.31),
	colorPalette(0.99, 0.58, 0.20, 1.00, 0.69, 0.32),
}

var fallbackPalettes = []palette{
	colorPalette(0.16, 0.63, 0.67, 0.25, 0.74, 0.78),
	colorPalette(0.24, 0.55, 0.95, 0.35, 0.65, 1.00),
	colorPalette(0.54, 0.71, 0.30, 0.64, 0.80, 0.41),
	colorPalette(0.88, 0.47, 0.40, 0.96, 0.59, 0.52),
	colorPalette(0.91, 0.66, 0.23, 0.98, 0.77, 0.35),
}

var interventionRegistry struct {
	sync.Mutex
	next uintptr
	byID map[uintptr]*Controller
}

var (
	arrowPathOnce sync.Once
	arrowPath     coregraphics.CGPathRef
)

var defaultController = New(Config{
	Enabled: true,
	Eyecandy: EyecandyConfig{
		SharingVisible: true,
	},
})

// New returns a controller that marshals all AppKit work to the main thread.
func New(cfg Config) *Controller {
	return &Controller{
		enabled:  cfg.Enabled,
		cache:    make(map[ActivityState]cursorSpriteSet),
		palette:  paletteForTheme(cfg.Theme, os.Getpid),
		eyecandy: normalizeEyecandy(cfg.Eyecandy),
		theme:    cfg.Theme,
		tuning:   normalizeTuning(cfg.Tuning),
	}
}

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

// Close releases AppKit and monitoring resources held by the controller.
func (c *Controller) Close() {
	c.seq.Add(1)
	c.stopIdleSway()
	c.mu.Lock()
	c.enabled = false
	c.hasCursor = false
	c.visible = false
	c.mu.Unlock()

	runOnMain(func() {
		c.stopObservers()
		if c.win.GetID() == 0 {
			return
		}
		c.win.OrderOut(nil)
		c.win.Close()
		c.win = appkit.NSWindow{}
		c.aura = quartzcore.CAShapeLayer{}
		c.dot = quartzcore.CAShapeLayer{}
		c.halo = quartzcore.CAShapeLayer{}
		c.root = quartzcore.CALayer{}
		c.trail = quartzcore.CAEmitterLayer{}
	})
}

func FlashCaptureRect(rect corefoundation.CGRect) {
	if rect.Size.Width <= 0 || rect.Size.Height <= 0 {
		return
	}
	p := defaultController.palette
	go flashRect(rect, p, captureFlashTime)
}

func windowCollectionBehavior() appkit.NSWindowCollectionBehavior {
	return appkit.NSWindowCollectionBehaviorCanJoinAllSpaces |
		appkit.NSWindowCollectionBehaviorTransient |
		appkit.NSWindowCollectionBehaviorIgnoresCycle |
		appkit.NSWindowCollectionBehaviorFullScreenAuxiliary
}

func (c *Controller) Configure(cfg Config) {
	c.mu.Lock()
	c.enabled = cfg.Enabled
	c.eyecandy = normalizeEyecandy(cfg.Eyecandy)
	c.theme = cfg.Theme
	c.tuning = normalizeTuning(cfg.Tuning)
	c.palette = paletteForTheme(cfg.Theme, os.Getpid)
	c.cache = make(map[ActivityState]cursorSpriteSet)
	c.seq.Add(1)
	c.mu.Unlock()
	if !cfg.Enabled {
		c.Hide()
		return
	}
	runOnMain(func() {
		if c.win.GetID() != 0 {
			c.win.SetSharingType(c.overlaySharingType())
		}
	})
}

func (c *Controller) Enabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enabled
}

func (c *Controller) Show(pos Position, activity ActivityState, hideAfter time.Duration) error {
	x, y, err := resolveScreenPoint(pos)
	if err != nil {
		return err
	}

	c.mu.Lock()
	if !c.enabled {
		c.mu.Unlock()
		return nil
	}
	seq := c.seq.Add(1)
	prevActivity := c.activity
	c.current = pos
	c.hasCursor = true
	c.visible = true
	c.activity = activity
	c.mu.Unlock()
	visualSeq := c.visualSeq.Add(1)

	runOnMain(func() {
		c.ensureWindow()
		c.placeWindow(x, y)
		c.renderActivityChange(visualSeq, prevActivity, activity)
		c.applyMotionTransform(activity, 0, 0, 0)
		c.win.SetAlphaValue(1)
		c.win.OrderFrontRegardless()
		c.maybeTriggerRipple(prevActivity, activity)
	})
	c.syncIdleSway(seq, activity)
	if hideAfter > 0 {
		c.hideAfter(seq, hideAfter)
	}
	return nil
}

func (c *Controller) SetActivity(activity ActivityState) error {
	c.mu.Lock()
	if !c.enabled || !c.hasCursor || !c.visible {
		c.activity = activity
		c.mu.Unlock()
		return nil
	}
	prevActivity := c.activity
	c.activity = activity
	seq := c.seq.Load()
	c.mu.Unlock()
	visualSeq := c.visualSeq.Add(1)

	runOnMain(func() {
		c.ensureWindow()
		c.renderActivityChange(visualSeq, prevActivity, activity)
		c.applyMotionTransform(activity, 0, 0, 0)
		c.win.SetAlphaValue(1)
		c.win.OrderFrontRegardless()
		c.maybeTriggerRipple(prevActivity, activity)
	})
	c.syncIdleSway(seq, activity)
	return nil
}

// MoveTo blocks until the requested motion has completed or the
// next-interaction gate is satisfied. Callers may invoke it from any
// goroutine.
func (c *Controller) MoveTo(ctx context.Context, pos Position, opts MoveOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	x1, y1, err := resolveScreenPoint(pos)
	if err != nil {
		return err
	}

	c.mu.Lock()
	if !c.enabled {
		c.mu.Unlock()
		return nil
	}
	seq := c.seq.Add(1)
	c.visualSeq.Add(1)
	start := pos
	if c.hasCursor && c.current.Space == pos.Space {
		start = c.current
	}
	c.current = start
	c.hasCursor = true
	c.visible = true
	c.activity = opts.Activity
	c.mu.Unlock()

	x0, y0, err := resolveScreenPoint(start)
	if err != nil {
		return err
	}
	if opts.Duration <= 0 || (x0 == x1 && y0 == y1) {
		return c.Show(pos, opts.Activity, opts.HideAfter)
	}
	path, err := SamplePath(start, pos, opts)
	if err != nil {
		return err
	}
	if len(path) == 0 {
		return c.Show(pos, opts.Activity, opts.HideAfter)
	}
	interval := frameInterval(opts.Duration, len(path))
	gate := normalizeNextInteraction(opts.NextInteraction, opts.Duration)
	if !gate.enabled {
		return c.runMove(ctx, seq, path, opts, interval, gate, nil)
	}
	ready := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.runMove(ctx, seq, path, opts, interval, gate, ready)
	}()
	select {
	case err := <-done:
		return err
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Controller) Hide() {
	c.seq.Add(1)
	c.visualSeq.Add(1)
	c.stopIdleSway()
	c.mu.Lock()
	c.visible = false
	c.mu.Unlock()
	runOnMain(func() {
		if c.win.GetID() == 0 {
			return
		}
		c.win.OrderOut(nil)
	})
}

func (c *Controller) runMove(ctx context.Context, seq uint64, path []Position, opts MoveOptions, interval time.Duration, gate moveGate, ready chan<- struct{}) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c.stopIdleSway()
	c.triggerMoveGlowPulse()
	var (
		state moveGateState
		prev  = path[0]
		last  = time.Now()
	)
	state.startedAt = last
	x1, y1, err := resolveScreenPoint(path[len(path)-1])
	if err != nil {
		return err
	}
	for i, step := range path {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if c.seq.Load() != seq {
			return ErrMoveAborted
		}
		x, y, err := resolveScreenPoint(step)
		if err != nil {
			return err
		}
		now := time.Now()
		dx := 0.0
		dy := 0.0
		speed := 0.0
		if i > 0 {
			if dt := now.Sub(last); dt > 0 {
				px, py, err := resolveScreenPoint(prev)
				if err != nil {
					return err
				}
				dx = float64(x - px)
				dy = float64(y - py)
				speed = distance(px, py, x, y) / dt.Seconds()
			}
		}
		progress := 1.0
		if len(path) > 1 {
			progress = float64(i) / float64(len(path)-1)
		}
		dist := distance(x, y, x1, y1)

		c.mu.Lock()
		c.current = step
		c.hasCursor = true
		c.visible = true
		c.activity = opts.Activity
		c.mu.Unlock()

		runOnMain(func() {
			c.ensureWindow()
			c.placeWindow(x, y)
			c.applyActivity(opts.Activity)
			c.applyMotionTransform(opts.Activity, dx, dy, speed)
			c.win.SetAlphaValue(1)
			c.win.OrderFrontRegardless()
		})

		if gate.shouldSignal(now, progress, dist, speed, &state) {
			signalMoveReady(ready)
		}
		prev = step
		last = now
		if i+1 >= len(path) {
			break
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	signalMoveReady(ready)
	if opts.HideAfter > 0 {
		c.hideAfter(seq, opts.HideAfter)
	}
	return nil
}

func (c *Controller) hideAfter(seq uint64, delay time.Duration) {
	if delay <= 0 {
		c.hideIfCurrent(seq)
		return
	}
	go func() {
		time.Sleep(delay)
		c.hideIfCurrent(seq)
	}()
}

func (c *Controller) hideIfCurrent(seq uint64) {
	if c.seq.Load() != seq {
		return
	}
	c.Hide()
}

func (c *Controller) syncIdleSway(seq uint64, activity ActivityState) {
	c.stopIdleSway()
}

func (c *Controller) stopIdleSway() {
	c.swaySeq.Add(1)
	c.mu.Lock()
	pos := c.current
	visible := c.visible && c.hasCursor
	c.mu.Unlock()
	if !visible {
		return
	}
	x, y, err := resolveScreenPoint(pos)
	if err != nil {
		return
	}
	runOnMain(func() {
		if c.win.GetID() == 0 {
			return
		}
		c.placeWindow(x, y)
	})
}

func (c *Controller) runIdleSway(seq, token uint64) {
	const tickInterval = 34 * time.Millisecond
	started := time.Now()
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for range ticker.C {
		if c.seq.Load() != seq || c.swaySeq.Load() != token {
			return
		}
		c.mu.Lock()
		pos := c.current
		activity := c.activity
		visible := c.enabled && c.visible && c.hasCursor
		c.mu.Unlock()
		if !visible || !isInactiveActivity(activity) || activity == ActivityTyping {
			return
		}
		x, y, err := resolveScreenPoint(pos)
		if err != nil {
			return
		}
		offsetX, offsetY := idleSwayOffset(time.Since(started))
		runOnMain(func() {
			if c.seq.Load() != seq || c.swaySeq.Load() != token || c.win.GetID() == 0 {
				return
			}
			c.placeWindow(
				x+int(math.Round(offsetX)),
				y+int(math.Round(offsetY)),
			)
		})
	}
}

func (c *Controller) installLayerTree(root quartzcore.CALayer) {
	if c.root.GetID() != 0 {
		return
	}
	scale := c.layerContentsScale()
	root.SetFrame(windowFrame())
	root.SetAnchorPoint(corefoundation.CGPoint{X: 0.5, Y: 0.5})
	root.SetMasksToBounds(false)
	root.SetAllowsEdgeAntialiasing(true)
	root.SetAllowsGroupOpacity(true)
	root.SetContentsScale(scale)
	root.SetRasterizationScale(scale)
	c.root = root
	if c.eyecandy.CometTrail {
		c.ensureTrailLayer()
	}

	aura := quartzcore.NewCAShapeLayer()
	aura.SetContentsScale(scale)
	aura.SetFrame(windowFrame())
	aura.SetLineCap(quartzcore.KCALineCapRound)
	aura.SetLineJoin(quartzcore.KCALineJoinRound)
	aura.SetAllowsEdgeAntialiasing(true)
	aura.SetRasterizationScale(scale)
	root.AddSublayer(aura)

	halo := quartzcore.NewCAShapeLayer()
	halo.SetContentsScale(scale)
	halo.SetFrame(windowFrame())
	halo.SetLineCap(quartzcore.KCALineCapRound)
	halo.SetLineJoin(quartzcore.KCALineJoinRound)
	halo.SetAllowsEdgeAntialiasing(true)
	halo.SetRasterizationScale(scale)
	root.AddSublayer(halo)

	dot := quartzcore.NewCAShapeLayer()
	dot.SetContentsScale(scale)
	dot.SetFrame(windowFrame())
	dot.SetLineCap(quartzcore.KCALineCapRound)
	dot.SetLineJoin(quartzcore.KCALineJoinRound)
	dot.SetAllowsEdgeAntialiasing(true)
	dot.SetRasterizationScale(scale)
	root.AddSublayer(dot)

	c.aura = aura
	c.halo = halo
	c.dot = dot
	c.applyActivity(ActivityIdle)
	c.applyMotionTransform(ActivityIdle, 0, 0, 0)
}

func (c *Controller) layerContentsScale() float64 {
	if c.win.GetID() != 0 {
		if scale := c.win.BackingScaleFactor(); scale >= 1 {
			return scale
		}
	}
	main := appkit.GetNSScreenClass().MainScreen()
	if main.GetID() != 0 {
		if scale := main.BackingScaleFactor(); scale >= 1 {
			return scale
		}
	}
	return 2
}

func (c *Controller) ensureWindow() {
	if c.win.GetID() != 0 {
		return
	}
	frame := corefoundation.CGRect{
		Size: corefoundation.CGSize{
			Width:  windowSize,
			Height: windowSize,
		},
	}
	win := appkit.NewWindowWithContentRectStyleMaskBackingDefer(
		frame,
		appkit.NSWindowStyleMaskBorderless,
		appkit.NSBackingStoreBuffered,
		false,
	)
	win.SetOpaque(false)
	win.SetBackgroundColor(appkit.NewColorWithSRGBRedGreenBlueAlpha(0, 0, 0, 0))
	win.SetHasShadow(false)
	win.SetIgnoresMouseEvents(true)
	win.SetReleasedWhenClosed(false)
	win.SetLevel(appkit.StatusWindowLevel)
	win.SetCollectionBehavior(windowCollectionBehavior())
	win.SetSharingType(c.overlaySharingType())

	container := appkit.NewViewWithFrame(frame)
	fog := appkit.NewVisualEffectViewWithFrame(windowFrame())
	fog.SetState(appkit.NSVisualEffectStateActive)
	fog.SetBlendingMode(appkit.NSVisualEffectBlendingModeBehindWindow)
	fog.SetMaterial(appkit.NSVisualEffectMaterialHUDWindow)
	fog.SetEmphasized(false)
	fog.SetAlphaValue(0)
	fog.SetWantsLayer(true)
	fogMask := quartzcore.NewCAShapeLayer()
	fogMask.SetFrame(windowFrame())
	fogMask.SetContentsScale(c.layerContentsScale())
	fogMask.SetAllowsEdgeAntialiasing(true)
	fogMask.SetFillColor(cgColor(1, 1, 1, 1))
	fogMask.SetStrokeColor(0)
	fogMask.SetLineWidth(0)
	if fogLayer := fog.Layer(); fogLayer.ID != 0 {
		fogLayer.SetFrame(windowFrame())
		fogLayer.SetMasksToBounds(false)
		fogLayer.SetMask(fogMask)
	}

	overlay := appkit.NewViewWithFrame(frame)
	overlay.SetWantsLayer(true)
	overlay.Layer().SetFrame(windowFrame())
	container.AddSubview(fog)
	container.AddSubviewPositionedRelativeTo(overlay, appkit.NSWindowAbove, fog)
	win.SetContentView(container)
	root := overlay.Layer()

	c.win = win
	c.fog = fog
	c.fogMask = fogMask
	c.installLayerTree(root)
	win.SetAlphaValue(0)
	c.startObservers()
}

func (c *Controller) startObservers() {
	if c.spaceObserver.ID != 0 || c.win.GetID() == 0 {
		return
	}
	ws := appkit.GetNSWorkspaceClass().SharedWorkspace()
	wsCenter := ws.NotificationCenter()
	c.spaceObserver = wsCenter.AddObserverForNameObjectQueueUsingBlock(
		appkit.WorkspaceActiveSpaceDidChangeNotification,
		objectivec.Object{},
		nil,
		func(_ *foundation.NSNotification) {
			c.Hide()
		},
	)

	defaultCenter := foundation.GetNotificationCenterClass().DefaultCenter()
	c.screenObserver = defaultCenter.AddObserverForNameObjectQueueUsingBlock(
		appkit.ApplicationDidChangeScreenParametersNotification,
		objectivec.Object{},
		nil,
		func(_ *foundation.NSNotification) {
			c.Hide()
		},
	)
	c.occlusionObserver = defaultCenter.AddObserverForNameObjectQueueUsingBlock(
		appkit.WindowDidChangeOcclusionStateNotification,
		c.win,
		nil,
		func(_ *foundation.NSNotification) {
			if c.win.GetID() == 0 {
				return
			}
			if c.win.OcclusionState()&appkit.NSWindowOcclusionStateVisible == 0 {
				c.Hide()
			}
		},
	)
	c.startInterventionMonitor()
}

func (c *Controller) stopObservers() {
	if c.interventionSrc != 0 {
		corefoundation.CFRunLoopRemoveSource(corefoundation.CFRunLoopGetMain(), c.interventionSrc, corefoundation.KCFRunLoopCommonModes)
		corefoundation.CFRelease(corefoundation.CFTypeRef(c.interventionSrc))
		c.interventionSrc = 0
	}
	if c.interventionTap != 0 {
		corefoundation.CFMachPortInvalidate(c.interventionTap)
		corefoundation.CFRelease(corefoundation.CFTypeRef(c.interventionTap))
		c.interventionTap = 0
	}
	if c.interventionID != 0 {
		unregisterInterventionController(c.interventionID)
		c.interventionID = 0
	}
	ws := appkit.GetNSWorkspaceClass().SharedWorkspace()
	wsCenter := ws.NotificationCenter()
	if c.spaceObserver.ID != 0 {
		wsCenter.RemoveObserver(c.spaceObserver)
		c.spaceObserver = objectivec.Object{}
	}
	defaultCenter := foundation.GetNotificationCenterClass().DefaultCenter()
	if c.screenObserver.ID != 0 {
		defaultCenter.RemoveObserver(c.screenObserver)
		c.screenObserver = objectivec.Object{}
	}
	if c.occlusionObserver.ID != 0 {
		defaultCenter.RemoveObserver(c.occlusionObserver)
		c.occlusionObserver = objectivec.Object{}
	}
}

func (c *Controller) startInterventionMonitor() {
	if c.interventionTap != 0 {
		return
	}
	id := registerInterventionController(c)
	tap := coregraphics.CGEventTapCreate(
		coregraphics.KCGSessionEventTap,
		coregraphics.KCGHeadInsertEventTap,
		eventTapOptionListenOnly,
		mouseInterventionMask(),
		ghostCursorInterventionCallback,
		unsafe.Pointer(id),
	)
	if tap == 0 {
		unregisterInterventionController(id)
		return
	}
	src := corefoundation.CFMachPortCreateRunLoopSource(0, tap, 0)
	if src == 0 {
		corefoundation.CFMachPortInvalidate(tap)
		corefoundation.CFRelease(corefoundation.CFTypeRef(tap))
		unregisterInterventionController(id)
		return
	}
	corefoundation.CFRunLoopAddSource(corefoundation.CFRunLoopGetMain(), src, corefoundation.KCFRunLoopCommonModes)
	coregraphics.CGEventTapEnable(tap, true)
	c.interventionTap = tap
	c.interventionSrc = src
	c.interventionID = id
}

func (c *Controller) handleUserIntervention(_ int, _ int) {
	c.mu.Lock()
	active := c.enabled && c.visible && c.hasCursor
	if active {
		c.activity = ActivityPaused
	}
	seq := c.seq.Add(1)
	c.mu.Unlock()
	if !active {
		return
	}
	now := time.Now().UnixNano()
	if last := c.lastIntervention.Load(); last != 0 && time.Duration(now-last) < userInterventionCooldown {
		return
	}
	c.lastIntervention.Store(now)
	runOnMain(func() {
		if c.win.GetID() == 0 {
			return
		}
		c.applyActivityAnimated(ActivityPaused, pausedFadeTime)
		c.applyMotionTransform(ActivityPaused, 0, 0, 0)
	})
	c.hideAfter(seq, userInterventionHideDelay)
}

// primaryDisplayHeight returns the height of the primary display (the one
// whose bottom-left is the origin of AppKit global screen coords). Must use
// [NSScreen screens][0]; NSScreen.mainScreen() is the focused screen and is
// wrong on multi-monitor setups or when another app is key.
func primaryDisplayHeight() float64 {
	screens := appkit.GetNSScreenClass().Screens()
	if len(screens) == 0 {
		return appkit.GetNSScreenClass().MainScreen().Frame().Size.Height
	}
	return screens[0].Frame().Size.Height
}

func (c *Controller) placeWindow(x, y int) {
	// (x, y) are CoreGraphics / AX global coords (top-left origin, Y down).
	// NSWindow.setFrameOrigin takes AppKit screen coords (bottom-left origin,
	// Y up). Flip against the primary display height.
	primaryHeight := primaryDisplayHeight()
	c.win.SetFrameOrigin(corefoundation.CGPoint{
		X: float64(x) - windowSize/2,
		Y: primaryHeight - float64(y) - windowSize/2,
	})
	if c.trail.ID != 0 {
		c.trail.SetEmitterPosition(corefoundation.CGPoint{X: windowSize / 2, Y: windowSize / 2})
	}
}

func (c *Controller) renderActivityChange(token uint64, prev, next ActivityState) {
	if token != c.visualSeq.Load() {
		return
	}
	if shouldAnimateIdleDimming(prev, next) {
		c.scheduleIdleFade(token, next)
		return
	}
	c.applyActivity(next)
}

func (c *Controller) scheduleIdleFade(token uint64, activity ActivityState) {
	delay := c.tuning.IdleFadeDelay
	duration := c.tuning.IdleFadeTime
	if delay <= 0 {
		c.applyActivityAnimated(activity, duration)
		return
	}
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		<-timer.C
		runOnMain(func() {
			if token != c.visualSeq.Load() {
				return
			}
			c.applyActivityAnimated(activity, duration)
		})
	}()
}

func (c *Controller) applyActivity(activity ActivityState) {
	if activity == ActivityTyping {
		c.applyTypingActivity()
		return
	}
	c.applyVisualState(visualStateForTuning(c.palette, c.tuning, activity, c.moveGlowFactor(activity)))
}

func (c *Controller) applyActivityAnimated(activity ActivityState, duration time.Duration) {
	if duration <= 0 {
		c.applyActivity(activity)
		return
	}
	withLayerAnimation(duration, func() {
		c.applyActivity(activity)
	})
}

func (c *Controller) applyVisualState(visual cursorVisualState) {
	if visual.spriteActivity == ActivityTyping {
		c.applyTypingActivity()
		return
	}
	c.applyVectorVisual(visual)
}

func (c *Controller) applyVectorVisual(visual cursorVisualState) {
	clearCursorLayer(c.aura)
	clearCursorLayer(c.halo)
	clearCursorLayer(c.dot)
	tokens := visual.tokens
	bodyPath := cursorPath(tokens.bodyScale)
	fogPath := cursorFogPath(tokens.fogScale)
	c.applyFog(fogPath, tokens, visual.spriteActivity)
	if c.aura.ID != 0 {
		c.aura.SetFrame(windowFrame())
		c.aura.SetPath(fogPath)
		c.aura.SetOpacity(1)
		fillAlpha := tokens.fogAlpha
		if c.fog.GetID() != 0 {
			fillAlpha *= 0.30
		}
		c.aura.SetFillColor(cgColor(visual.fogRed, visual.fogGreen, visual.fogBlue, fillAlpha))
		c.aura.SetStrokeColor(0)
		c.aura.SetLineWidth(0)
		c.aura.SetShadowColor(cgColor(visual.fogRed, visual.fogGreen, visual.fogBlue, 1))
		c.aura.SetShadowOpacity(float32(tokens.fogShadowAlpha))
		c.aura.SetShadowRadius(tokens.fogShadowBlur)
		c.aura.SetShadowOffset(corefoundation.CGSize{})
		c.aura.SetShadowPath(fogPath)
	}
	c.halo.SetFrame(windowFrame())
	c.halo.SetPath(bodyPath)
	c.halo.SetOpacity(1)
	c.halo.SetFillColor(cgColor(visual.bodyRed, visual.bodyGreen, visual.bodyBlue, tokens.bodyAlpha))
	c.halo.SetStrokeColor(0)
	c.halo.SetLineWidth(0)
	c.halo.SetShadowColor(0)
	c.halo.SetShadowOpacity(0)
	c.halo.SetShadowRadius(0)
	c.halo.SetShadowOffset(corefoundation.CGSize{})
	c.halo.SetShadowPath(0)

	c.dot.SetFrame(windowFrame())
	c.dot.SetPath(bodyPath)
	c.dot.SetOpacity(1)
	c.dot.SetFillColor(0)
	c.dot.SetStrokeColor(cgColor(visual.outlineRed, visual.outlineGreen, visual.outlineBlue, tokens.outlineAlpha))
	c.dot.SetLineWidth(tokens.outlineWidth)
	c.dot.SetShadowOpacity(0)
}

func (c *Controller) applyTypingActivity() {
	c.hideFog()
	clearCursorLayer(c.aura)
	clearCursorLayer(c.halo)
	clearCursorLayer(c.dot)
	outer := roundedRectPath(roundedRectFrame(12, 28), 6)
	inner := roundedRectPath(roundedRectFrame(5, 22), 2.5)
	if c.aura.ID != 0 {
		frame := roundedRectFrame(18, 32)
		c.aura.SetFrame(windowFrame())
		c.aura.SetPath(roundedRectPath(frame, 9))
		c.aura.SetOpacity(1)
		c.aura.SetFillColor(cgColor(c.palette.haloRed, c.palette.haloGreen, c.palette.haloBlue, 0.16))
		c.aura.SetStrokeColor(cgColor(c.palette.borderRed, c.palette.borderGreen, c.palette.borderBlue, 0.28))
		c.aura.SetLineWidth(2.25)
		c.aura.SetShadowColor(cgColor(c.palette.borderRed, c.palette.borderGreen, c.palette.borderBlue, 0.96))
		c.aura.SetShadowOpacity(0.78)
		c.aura.SetShadowRadius(12)
		c.aura.SetShadowOffset(corefoundation.CGSize{})
		c.aura.SetShadowPath(roundedRectPath(frame, 9))
	}
	c.halo.SetFrame(windowFrame())
	c.halo.SetPath(outer)
	c.halo.SetOpacity(1)
	c.halo.SetFillColor(cgColor(c.palette.haloRed, c.palette.haloGreen, c.palette.haloBlue, 0.12))
	c.halo.SetStrokeColor(cgColor(c.palette.borderRed, c.palette.borderGreen, c.palette.borderBlue, 0.98))
	c.halo.SetLineWidth(2.5)
	c.halo.SetShadowOpacity(0)

	c.dot.SetFrame(windowFrame())
	c.dot.SetPath(inner)
	c.dot.SetOpacity(1)
	c.dot.SetFillColor(cgColor(c.palette.dotRed, c.palette.dotGreen, c.palette.dotBlue, 0.99))
	c.dot.SetStrokeColor(0)
	c.dot.SetLineWidth(0)
	c.dot.SetShadowOpacity(0)
}

func (c *Controller) hideFog() {
	if c.fog.GetID() == 0 {
		return
	}
	c.fog.SetAlphaValue(0)
	c.fog.SetMaskImage(appkit.NSImage{})
	if c.fogMask.ID != 0 {
		c.fogMask.SetPath(0)
	}
}

func (c *Controller) applyFog(path coregraphics.CGPathRef, tokens cursorTokens, activity ActivityState) {
	if c.fog.GetID() == 0 {
		return
	}
	c.fog.SetMaterial(appkit.NSVisualEffectMaterialHUDWindow)
	c.fog.SetState(appkit.NSVisualEffectStateActive)
	c.fog.SetBlendingMode(appkit.NSVisualEffectBlendingModeBehindWindow)
	c.fog.SetEmphasized(activity == ActivityPressed)
	c.fog.SetMaskImage(appkit.NSImage{})
	c.fog.SetFrame(windowFrame())
	if fogLayer := c.fog.Layer(); fogLayer.ID != 0 && c.fogMask.ID != 0 {
		fogLayer.SetFrame(windowFrame())
		fogLayer.SetMasksToBounds(false)
		fogLayer.SetMask(c.fogMask)
	}
	if c.fogMask.ID != 0 {
		c.fogMask.SetFrame(windowFrame())
		c.fogMask.SetFillColor(cgColor(1, 1, 1, 1))
		c.fogMask.SetStrokeColor(0)
		c.fogMask.SetLineWidth(0)
		c.fogMask.SetPath(path)
	}
	c.fog.SetAlphaValue(fogViewAlpha(activity, tokens))
}

func fogViewAlpha(activity ActivityState, tokens cursorTokens) float64 {
	base := 0.0
	switch activity {
	case ActivityIdle:
		base = 0.24
	case ActivityThinking:
		base = 0.28
	case ActivityMoving, ActivityDragging:
		base = 0.42
	case ActivityPressed:
		base = 0.50
	case ActivityPaused:
		base = 0.10
	default:
		base = 0.24
	}
	return clamp01(base + tokens.fogAlpha*2.9)
}

func (c *Controller) applyMotionTransform(activity ActivityState, dx, dy, speed float64) {
	if c.root.ID == 0 {
		return
	}
	tokens := cursorTokensFor(activity)
	transform := quartzcore.CATransform3DIdentity
	if tokens.motionScale != 0 && tokens.motionScale != 1 {
		transform = quartzcore.CATransform3DMakeScale(tokens.motionScale, tokens.motionScale, 1)
	}
	if !c.eyecandy.VelocityTilt || speed <= 0 || (math.Abs(dx) < 0.5 && math.Abs(dy) < 0.5) {
		c.tiltReady = false
		c.root.SetTransform(transform)
		return
	}
	angle := clampFloat(math.Atan2(dy, dx)*0.10, -0.12, 0.12)
	if !c.tiltReady {
		c.lastAngle = angle
		c.tiltReady = true
	} else {
		c.lastAngle = blendAngle(c.lastAngle, angle, 0.16)
	}
	rotate := quartzcore.CATransform3DMakeRotation(c.lastAngle, 0, 0, 1)
	c.root.SetTransform(quartzcore.CATransform3DConcat(rotate, transform))
}

func cursorTokensFor(activity ActivityState) cursorTokens {
	return cursorTokensForTuning(activity, DefaultTuningConfig())
}

func cursorTokensForTuning(activity ActivityState, tuning TuningConfig) cursorTokens {
	var tokens cursorTokens
	switch activity {
	case ActivityIdle:
		tokens = cursorTokens{bodyScale: 0.95, bodyAlpha: 0.014, outlineWidth: 1.02, outlineAlpha: 0.42, fogScale: 1.08, fogAlpha: 0.028, fogShadowAlpha: 0.11, fogShadowBlur: 6.2, bodyShadowAlpha: 0.010, bodyShadowBlur: 1.1, motionScale: 1}
	case ActivityMoving:
		tokens = cursorTokens{bodyScale: 0.97, bodyAlpha: 0.018, outlineWidth: 1.04, outlineAlpha: 0.50, fogScale: 1.12, fogAlpha: 0.050, fogShadowAlpha: 0.15, fogShadowBlur: 7.0, bodyShadowAlpha: 0.012, bodyShadowBlur: 1.2, motionScale: 1}
	case ActivityPressed:
		tokens = cursorTokens{bodyScale: 0.98, bodyAlpha: 0.024, outlineWidth: 1.08, outlineAlpha: 0.56, fogScale: 1.14, fogAlpha: 0.062, fogShadowAlpha: 0.18, fogShadowBlur: 7.6, bodyShadowAlpha: 0.015, bodyShadowBlur: 1.3, motionScale: 1.00}
	case ActivityDragging:
		tokens = cursorTokens{bodyScale: 0.97, bodyAlpha: 0.020, outlineWidth: 1.06, outlineAlpha: 0.52, fogScale: 1.13, fogAlpha: 0.054, fogShadowAlpha: 0.16, fogShadowBlur: 7.2, bodyShadowAlpha: 0.013, bodyShadowBlur: 1.2, motionScale: 1}
	case ActivityTyping:
		tokens = cursorTokens{motionScale: 1}
	case ActivityThinking:
		tokens = cursorTokens{bodyScale: 0.95, bodyAlpha: 0.014, outlineWidth: 1.02, outlineAlpha: 0.44, fogScale: 1.09, fogAlpha: 0.032, fogShadowAlpha: 0.12, fogShadowBlur: 6.4, bodyShadowAlpha: 0.010, bodyShadowBlur: 1.1, motionScale: 1}
	case ActivityPaused:
		tokens = cursorTokens{bodyScale: 0.94, bodyAlpha: 0.008, outlineWidth: 0.98, outlineAlpha: 0.32, fogScale: 1.03, fogAlpha: 0.016, fogShadowAlpha: 0.07, fogShadowBlur: 5.0, bodyShadowAlpha: 0.007, bodyShadowBlur: 0.9, motionScale: 1}
	default:
		tokens = cursorTokens{bodyScale: 0.95, bodyAlpha: 0.014, outlineWidth: 1.02, outlineAlpha: 0.42, fogScale: 1.08, fogAlpha: 0.028, fogShadowAlpha: 0.11, fogShadowBlur: 6.2, bodyShadowAlpha: 0.010, bodyShadowBlur: 1.1, motionScale: 1}
	}
	return applyTuningToTokens(tokens, tuning)
}

func applyTuningToTokens(tokens cursorTokens, tuning TuningConfig) cursorTokens {
	brightness := tuning.Brightness
	if brightness <= 0 {
		brightness = 1
	}
	bodyOpacity := tuning.BodyOpacity
	if bodyOpacity <= 0 {
		bodyOpacity = 1
	}
	cursorScale := tuning.CursorScale
	if cursorScale <= 0 {
		cursorScale = 1
	}
	outlineOpacity := tuning.OutlineOpacity
	if outlineOpacity <= 0 {
		outlineOpacity = 1
	}
	glowOpacity := tuning.GlowOpacity
	if glowOpacity <= 0 {
		glowOpacity = 1
	}
	glowScale := tuning.GlowScale
	if glowScale <= 0 {
		glowScale = 1
	}
	bodyOpacity *= brightness
	outlineOpacity *= brightness
	glowOpacity *= brightness

	tokens.bodyScale *= cursorScale
	tokens.outlineWidth *= cursorScale
	tokens.bodyAlpha = clamp01(tokens.bodyAlpha * bodyOpacity)
	tokens.outlineAlpha = clamp01(tokens.outlineAlpha * outlineOpacity)
	tokens.fogAlpha = clamp01(tokens.fogAlpha * glowOpacity)
	tokens.fogScale *= glowScale * cursorScale
	tokens.fogShadowAlpha = clamp01(tokens.fogShadowAlpha * glowOpacity)
	tokens.fogShadowBlur *= glowScale
	tokens.bodyShadowAlpha = clamp01(tokens.bodyShadowAlpha * bodyOpacity)
	return tokens
}

func visualStateForActivity(p palette, activity ActivityState, glow float64) cursorVisualState {
	return visualStateForTuning(p, DefaultTuningConfig(), activity, glow)
}

func visualStateForTuning(p palette, tuning TuningConfig, activity ActivityState, glow float64) cursorVisualState {
	glow = clamp01(glow)
	tokens := cursorTokensForTuning(activity, tuning)
	if glow > 0 {
		tokens.bodyAlpha = clamp01(tokens.bodyAlpha + 0.008*glow)
		tokens.outlineAlpha = clamp01(tokens.outlineAlpha + 0.16*glow)
		tokens.fogAlpha = clamp01(tokens.fogAlpha + 0.095*glow)
		tokens.fogScale += 0.06 * glow
		tokens.fogShadowAlpha = clamp01(tokens.fogShadowAlpha + 0.18*glow)
		tokens.fogShadowBlur += 2.2 * glow
		tokens.bodyShadowAlpha = clamp01(tokens.bodyShadowAlpha + 0.028*glow)
		tokens.bodyShadowBlur += 0.7 * glow
	}
	bodyRed, bodyGreen, bodyBlue := cursorBodyComponents(p, activity)
	outlineRed, outlineGreen, outlineBlue := cursorOutlineComponents(p, activity)
	fogRed, fogGreen, fogBlue := cursorFogComponents(p, activity)
	return cursorVisualState{
		spriteActivity: activity,
		tokens:         tokens,
		bodyRed:        bodyRed,
		bodyGreen:      bodyGreen,
		bodyBlue:       bodyBlue,
		outlineRed:     outlineRed,
		outlineGreen:   outlineGreen,
		outlineBlue:    outlineBlue,
		fogRed:         fogRed,
		fogGreen:       fogGreen,
		fogBlue:        fogBlue,
	}
}

func blendCursorVisualState(from, to cursorVisualState, progress float64) cursorVisualState {
	progress = clamp01(progress)
	return cursorVisualState{
		spriteActivity: to.spriteActivity,
		tokens:         blendCursorTokens(from.tokens, to.tokens, progress),
		bodyRed:        lerpFloat(from.bodyRed, to.bodyRed, progress),
		bodyGreen:      lerpFloat(from.bodyGreen, to.bodyGreen, progress),
		bodyBlue:       lerpFloat(from.bodyBlue, to.bodyBlue, progress),
		outlineRed:     lerpFloat(from.outlineRed, to.outlineRed, progress),
		outlineGreen:   lerpFloat(from.outlineGreen, to.outlineGreen, progress),
		outlineBlue:    lerpFloat(from.outlineBlue, to.outlineBlue, progress),
		fogRed:         lerpFloat(from.fogRed, to.fogRed, progress),
		fogGreen:       lerpFloat(from.fogGreen, to.fogGreen, progress),
		fogBlue:        lerpFloat(from.fogBlue, to.fogBlue, progress),
	}
}

func blendCursorTokens(from, to cursorTokens, progress float64) cursorTokens {
	progress = clamp01(progress)
	return cursorTokens{
		bodyScale:       lerpFloat(from.bodyScale, to.bodyScale, progress),
		bodyAlpha:       lerpFloat(from.bodyAlpha, to.bodyAlpha, progress),
		outlineWidth:    lerpFloat(from.outlineWidth, to.outlineWidth, progress),
		outlineAlpha:    lerpFloat(from.outlineAlpha, to.outlineAlpha, progress),
		fogScale:        lerpFloat(from.fogScale, to.fogScale, progress),
		fogAlpha:        lerpFloat(from.fogAlpha, to.fogAlpha, progress),
		fogShadowAlpha:  lerpFloat(from.fogShadowAlpha, to.fogShadowAlpha, progress),
		fogShadowBlur:   lerpFloat(from.fogShadowBlur, to.fogShadowBlur, progress),
		bodyShadowAlpha: lerpFloat(from.bodyShadowAlpha, to.bodyShadowAlpha, progress),
		bodyShadowBlur:  lerpFloat(from.bodyShadowBlur, to.bodyShadowBlur, progress),
		motionScale:     lerpFloat(from.motionScale, to.motionScale, progress),
	}
}

func lerpFloat(from, to, progress float64) float64 {
	return from + (to-from)*clamp01(progress)
}

func shouldAnimateIdleDimming(prev, next ActivityState) bool {
	if next != ActivityIdle || prev == next {
		return false
	}
	if prev == ActivityTyping || next == ActivityTyping {
		return false
	}
	return !isInactiveActivity(prev)
}

func cursorBodyComponents(p palette, activity ActivityState) (red, green, blue float64) {
	if isInactiveActivity(activity) {
		return mixChannel(p.dotRed, 0.74, 0.72),
			mixChannel(p.dotGreen, 0.77, 0.71),
			mixChannel(p.dotBlue, 0.82, 0.66)
	}
	return mixChannel(p.dotRed, 0.80, 0.74),
		mixChannel(p.dotGreen, 0.83, 0.73),
		mixChannel(p.dotBlue, 0.88, 0.68)
}

func cursorOutlineComponents(p palette, activity ActivityState) (red, green, blue float64) {
	if isInactiveActivity(activity) {
		return mixChannel(p.borderRed, 0.72, 0.54),
			mixChannel(p.borderGreen, 0.75, 0.52),
			mixChannel(p.borderBlue, 0.82, 0.49)
	}
	return mixChannel(p.borderRed, 0.78, 0.60),
		mixChannel(p.borderGreen, 0.81, 0.58),
		mixChannel(p.borderBlue, 0.88, 0.54)
}

func cursorFogComponents(p palette, activity ActivityState) (red, green, blue float64) {
	if isInactiveActivity(activity) {
		return mixChannel(p.haloRed, 0.70, 0.28),
			mixChannel(p.haloGreen, 0.80, 0.30),
			mixChannel(p.haloBlue, 0.86, 0.22)
	}
	return mixChannel(p.haloRed, 0.76, 0.34),
		mixChannel(p.haloGreen, 0.86, 0.38),
		mixChannel(p.haloBlue, 0.92, 0.28)
}

func (c *Controller) triggerMoveGlowPulse() {
	c.moveGlowStarted.Store(time.Now().UnixNano())
}

func (c *Controller) moveGlowFactor(activity ActivityState) float64 {
	base := 0.0
	switch activity {
	case ActivityMoving, ActivityDragging:
		base = 0.22
	case ActivityPressed:
		base = 0.12
	default:
		return 0
	}
	started := c.moveGlowStarted.Load()
	if started == 0 {
		return base
	}
	elapsed := time.Since(time.Unix(0, started))
	duration := c.tuning.MoveGlowTime
	if duration <= 0 {
		duration = moveGlowTime
	}
	if elapsed <= 0 || elapsed >= duration {
		return base
	}
	pulse := 1 - easeOutCubic(float64(elapsed)/float64(duration))
	return clamp01(base + 0.55*pulse)
}

func moveSteps(distance float64, duration time.Duration) int {
	if duration <= 0 {
		return 1
	}
	steps := int(math.Ceil(duration.Seconds() * 60))
	if distanceSteps := int(math.Ceil(distance / 16)); distanceSteps > steps {
		steps = distanceSteps
	}
	if steps < 1 {
		return 1
	}
	if steps > 240 {
		return 240
	}
	return steps
}

// SamplePath returns the screen-space keyframes for moving between start and
// end using the requested curve. The returned slice always contains the first
// and last point when the positions differ.
func SamplePath(start, end Position, opts MoveOptions) ([]Position, error) {
	x0, y0, err := resolveScreenPoint(start)
	if err != nil {
		return nil, err
	}
	x1, y1, err := resolveScreenPoint(end)
	if err != nil {
		return nil, err
	}
	if x0 == x1 && y0 == y1 {
		return []Position{ScreenPosition(x0, y0)}, nil
	}
	if opts.Duration <= 0 {
		return []Position{ScreenPosition(x0, y0), ScreenPosition(x1, y1)}, nil
	}
	points := sampleScreenPath(x0, y0, x1, y1, moveSteps(distance(x0, y0, x1, y1), opts.Duration), normalizeMoveOptions(opts))
	path := make([]Position, len(points))
	for i, p := range points {
		path[i] = ScreenPosition(p.X, p.Y)
	}
	return path, nil
}

func frameInterval(duration time.Duration, frames int) time.Duration {
	if duration <= 0 || frames <= 1 {
		return minFrameInterval
	}
	interval := duration / time.Duration(frames-1)
	if interval < minFrameInterval {
		return minFrameInterval
	}
	return interval
}

func resolveScreenPoint(pos Position) (int, int, error) {
	if pos.Space != CoordinateSpaceScreen {
		return 0, 0, fmt.Errorf("unsupported coordinate space %d", pos.Space)
	}
	return int(math.Round(pos.X)), int(math.Round(pos.Y)), nil
}

func distance(x0, y0, x1, y1 int) float64 {
	return math.Hypot(float64(x1-x0), float64(y1-y0))
}

func circleFrame(size float64) corefoundation.CGRect {
	inset := (windowSize - size) / 2
	return corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: inset, Y: inset},
		Size:   corefoundation.CGSize{Width: size, Height: size},
	}
}

func windowFrame() corefoundation.CGRect {
	return corefoundation.CGRect{
		Size: corefoundation.CGSize{
			Width:  windowSize,
			Height: windowSize,
		},
	}
}

func roundedRectFrame(width, height float64) corefoundation.CGRect {
	return corefoundation.CGRect{
		Origin: corefoundation.CGPoint{
			X: (windowSize - width) / 2,
			Y: (windowSize - height) / 2,
		},
		Size: corefoundation.CGSize{
			Width:  width,
			Height: height,
		},
	}
}

func cursorTipPoint() corefoundation.CGPoint {
	return corefoundation.CGPoint{
		X: windowSize / 2,
		Y: windowSize / 2,
	}
}

func idleSwayOffset(time.Duration) (dx, dy float64) {
	return 0, 0
}

func cursorPath(scale float64) coregraphics.CGPathRef {
	if scale <= 0 {
		scale = 1
	}
	tip := cursorTipPoint()
	transform := corefoundation.CGAffineTransform{
		A:  scale,
		D:  -scale,
		Tx: tip.X,
		Ty: tip.Y,
	}
	return coregraphics.CGPathCreateCopyByTransformingPath(baseArrowPath(), &transform)
}

func cursorFogPath(scale float64) coregraphics.CGPathRef {
	body := cursorPath(scale)
	strokeWidth := math.Max(2.2, 4.0*scale)
	stroked := coregraphics.CGPathCreateCopyByStrokingPath(
		body,
		nil,
		strokeWidth,
		uint(coregraphics.KCGLineCapRound),
		uint(coregraphics.KCGLineJoinRound),
		10,
	)
	path := coregraphics.CGPathCreateMutable()
	coregraphics.CGPathAddPath(path, nil, body)
	if stroked != 0 {
		coregraphics.CGPathAddPath(path, nil, stroked)
	}
	return coregraphics.CGPathRef(path)
}

func baseArrowPath() coregraphics.CGPathRef {
	arrowPathOnce.Do(func() {
		mask, err := systemArrowCursorMask()
		if err == nil {
			if path, err := vectorPathForMask(mask); err == nil {
				arrowPath = path
				return
			}
		}
		path := coregraphics.CGPathCreateMutable()
		points := []corefoundation.CGPoint{
			{X: -0.8, Y: 6.2},
			{X: -0.6, Y: 2.6},
			{X: 0.0, Y: 0.0},
			{X: 4.2, Y: 1.1},
			{X: 8.7, Y: 3.0},
			{X: 11.5, Y: 5.0},
			{X: 11.4, Y: 6.9},
			{X: 9.4, Y: 8.0},
			{X: 6.8, Y: 12.0},
			{X: 5.7, Y: 14.4},
			{X: 4.0, Y: 14.2},
			{X: 3.0, Y: 10.8},
			{X: 1.0, Y: 8.1},
			{X: -0.2, Y: 7.0},
		}
		coregraphics.CGPathMoveToPoint(path, nil, points[0].X, points[0].Y)
		for i := 1; i < len(points)-1; i++ {
			midX := (points[i].X + points[i+1].X) / 2
			midY := (points[i].Y + points[i+1].Y) / 2
			coregraphics.CGPathAddQuadCurveToPoint(path, nil, points[i].X, points[i].Y, midX, midY)
		}
		last := points[len(points)-1]
		coregraphics.CGPathAddQuadCurveToPoint(path, nil, last.X, last.Y, points[0].X, points[0].Y)
		coregraphics.CGPathCloseSubpath(path)
		arrowPath = coregraphics.CGPathRef(path)
	})
	return arrowPath
}

func expandRect(rect corefoundation.CGRect, padding float64) corefoundation.CGRect {
	if padding <= 0 {
		return rect
	}
	return corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: rect.Origin.X - padding, Y: rect.Origin.Y - padding},
		Size: corefoundation.CGSize{
			Width:  rect.Size.Width + 2*padding,
			Height: rect.Size.Height + 2*padding,
		},
	}
}

func isInactiveActivity(activity ActivityState) bool {
	switch activity {
	case ActivityIdle, ActivityThinking, ActivityPaused:
		return true
	default:
		return false
	}
}

func insetRect(rect corefoundation.CGRect, padding float64) corefoundation.CGRect {
	if padding <= 0 {
		return rect
	}
	width := rect.Size.Width - 2*padding
	if width < 0 {
		width = 0
	}
	height := rect.Size.Height - 2*padding
	if height < 0 {
		height = 0
	}
	return corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: rect.Origin.X + padding, Y: rect.Origin.Y + padding},
		Size:   corefoundation.CGSize{Width: width, Height: height},
	}
}

func (c *Controller) ensureTrailLayer() {
	if c.root.ID == 0 || c.trail.ID != 0 {
		return
	}
	trail := quartzcore.NewCAEmitterLayer()
	trail.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{},
		Size:   corefoundation.CGSize{Width: windowSize, Height: windowSize},
	})
	trail.SetEmitterPosition(corefoundation.CGPoint{X: windowSize / 2, Y: windowSize / 2})
	trail.SetEmitterShape(quartzcore.KCAEmitterLayerPoint)
	trail.SetEmitterMode(quartzcore.KCAEmitterLayerPoints)
	trail.SetRenderMode(quartzcore.KCAEmitterLayerAdditive)
	trail.SetEmitterSize(corefoundation.CGSize{Width: 8, Height: 8})
	trail.SetBirthRate(1.3)
	trail.SetLifetime(1)

	cell := quartzcore.NewCAEmitterCell()
	cell.SetBirthRate(32)
	cell.SetLifetime(0.45)
	cell.SetLifetimeRange(0.12)
	cell.SetVelocity(44)
	cell.SetVelocityRange(22)
	cell.SetEmissionRange(2 * math.Pi)
	cell.SetScale(0.24)
	cell.SetScaleRange(0.12)
	cell.SetScaleSpeed(-0.36)
	cell.SetAlphaRange(0.25)
	cell.SetAlphaSpeed(-1.5)
	cell.SetSpin(1.8)
	cell.SetSpinRange(1.2)
	cell.SetContentsScale(2)
	cell.SetColor(cgColor(c.palette.haloRed, c.palette.haloGreen, c.palette.haloBlue, 0.95))
	cell.SetContents(trailParticleImage(c.palette))
	trail.SetEmitterCells([]quartzcore.CAEmitterCell{cell})
	c.root.InsertSublayerAtIndex(trail, 0)
	c.trail = trail
}

func trailParticleImage(p palette) objectivec.IObject {
	img := appkit.NewImageWithSystemSymbolNameAccessibilityDescription("circle.fill", "Ghost cursor trail particle")
	if img.GetID() == 0 {
		return objectivec.Object{}
	}
	cfg := appkit.NewImageSymbolConfigurationWithPaletteColors([]appkit.NSColor{
		appkit.NewColorWithSRGBRedGreenBlueAlpha(p.dotRed, p.dotGreen, p.dotBlue, 0.96),
	})
	return appkit.NSImageFromID(img.ImageWithSymbolConfiguration(cfg).GetID())
}

func cgColor(red, green, blue, alpha float64) coregraphics.CGColorRef {
	return coregraphics.CGColorCreateSRGB(red, green, blue, alpha)
}

type screenPoint struct {
	X int
	Y int
}

type curvePoint struct {
	X float64
	Y float64
}

func normalizeMoveOptions(opts MoveOptions) MoveOptions {
	switch opts.CurveStyle {
	case CurveBezier, CurveEaseInOut, CurveLinear:
	default:
		opts.CurveStyle = CurveBezier
	}
	if opts.Strength == 0 {
		opts.Strength = 0.08
	}
	if opts.Jitter == 0 {
		opts.Jitter = 0.015
	}
	if opts.Jitter < 0 {
		opts.Jitter = 0
	}
	return opts
}

func sampleScreenPath(x0, y0, x1, y1, steps int, opts MoveOptions) []screenPoint {
	if steps < 1 {
		steps = 1
	}
	start := curvePoint{X: float64(x0), Y: float64(y0)}
	end := curvePoint{X: float64(x1), Y: float64(y1)}
	control1, control2 := computeBezierControls(x0, y0, x1, y1, opts.Strength, opts.Jitter)
	points := make([]screenPoint, 0, steps+1)
	appendPoint := func(x, y int) {
		if len(points) > 0 && points[len(points)-1].X == x && points[len(points)-1].Y == y {
			return
		}
		points = append(points, screenPoint{X: x, Y: y})
	}
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		progress := curveProgress(opts.CurveStyle, t)
		p := pointAlongCurve(start, control1, control2, end, progress, opts.CurveStyle)
		appendPoint(int(math.Round(p.X)), int(math.Round(p.Y)))
	}
	appendPoint(x1, y1)
	return points
}

func curveProgress(style CurveStyle, t float64) float64 {
	t = clamp01(t)
	switch style {
	case CurveLinear:
		return t
	default:
		return t * t * (3 - 2*t)
	}
}

func pointAlongCurve(start, control1, control2, end curvePoint, progress float64, style CurveStyle) curvePoint {
	switch style {
	case CurveLinear, CurveEaseInOut:
		return lerpCurvePoint(start, end, progress)
	default:
		return cubicBezierPoint(start, control1, control2, end, progress)
	}
}

func lerpCurvePoint(a, b curvePoint, t float64) curvePoint {
	return curvePoint{
		X: a.X + (b.X-a.X)*t,
		Y: a.Y + (b.Y-a.Y)*t,
	}
}

func cubicBezierPoint(p0, p1, p2, p3 curvePoint, t float64) curvePoint {
	u := 1 - t
	tt := t * t
	uu := u * u
	uuu := uu * u
	ttt := tt * t
	return curvePoint{
		X: uuu*p0.X + 3*uu*t*p1.X + 3*u*tt*p2.X + ttt*p3.X,
		Y: uuu*p0.Y + 3*uu*t*p1.Y + 3*u*tt*p2.Y + ttt*p3.Y,
	}
}

func computeBezierControls(x0, y0, x1, y1 int, strength, jitter float64) (curvePoint, curvePoint) {
	dx := float64(x1 - x0)
	dy := float64(y1 - y0)
	dist := math.Hypot(dx, dy)
	if dist == 0 {
		return curvePoint{X: float64(x0), Y: float64(y0)}, curvePoint{X: float64(x1), Y: float64(y1)}
	}
	nx, ny := -dy/dist, dx/dist
	sign, jitter1, jitter2 := deterministicCurveOffsets(x0, y0, x1, y1, jitter)
	swoop := clampFloat(dist*maxFloat(0.03, strength), 6, 24)
	off1 := (swoop + dist*jitter1) * sign
	off2 := (swoop*0.62 + dist*jitter2) * sign
	return curvePoint{
			X: float64(x0) + dx*0.30 + nx*off1,
			Y: float64(y0) + dy*0.30 + ny*off1,
		}, curvePoint{
			X: float64(x0) + dx*0.74 + nx*off2,
			Y: float64(y0) + dy*0.74 + ny*off2,
		}
}

func deterministicCurveOffsets(x0, y0, x1, y1 int, jitter float64) (sign, jitter1, jitter2 float64) {
	seed := curveSeed(x0, y0, x1, y1)
	sign = stableArcDirection(float64(x1-x0), float64(y1-y0))
	jitter1 = jitter * centeredUnit(seed>>9)
	jitter2 = jitter * centeredUnit(seed>>27)
	return sign, jitter1, jitter2
}

func stableArcDirection(dx, dy float64) float64 {
	if math.Abs(dx) >= math.Abs(dy) {
		if dx >= 0 {
			return -1
		}
		return 1
	}
	if dy >= 0 {
		return 1
	}
	return -1
}

func curveSeed(x0, y0, x1, y1 int) uint64 {
	const offset = 1469598103934665603
	const prime = 1099511628211
	h := uint64(offset)
	for _, v := range [...]int64{int64(x0), int64(y0), int64(x1), int64(y1)} {
		h ^= uint64(v) ^ uint64(v>>32)
		h *= prime
	}
	return h
}

func centeredUnit(v uint64) float64 {
	return float64(v&0xffff)/65535.0 - 0.5
}

func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

func easeOutCubic(t float64) float64 {
	t = clamp01(t)
	u := 1 - t
	return 1 - u*u*u
}

func withLayerAnimation(duration time.Duration, fn func()) {
	if fn == nil {
		return
	}
	tx := quartzcore.GetCATransactionClass()
	tx.Begin()
	if duration <= 0 {
		tx.SetDisableActions(true)
		fn()
		tx.Commit()
		return
	}
	tx.SetDisableActions(false)
	tx.SetAnimationDuration(duration.Seconds())
	tx.SetAnimationTimingFunction(quartzcore.NewMediaTimingFunctionWithName(quartzcore.KCAMediaTimingFunctionEaseInEaseOut))
	fn()
	tx.Commit()
}

func clampFloat(v, low, high float64) float64 {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

func blendAngle(prev, next, alpha float64) float64 {
	if alpha <= 0 {
		return prev
	}
	if alpha >= 1 {
		return next
	}
	delta := math.Atan2(math.Sin(next-prev), math.Cos(next-prev))
	return prev + delta*alpha
}

func mixChannel(base, target, weight float64) float64 {
	return base*(1-weight) + target*weight
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func roundedRectPath(rect corefoundation.CGRect, radius float64) coregraphics.CGPathRef {
	path := coregraphics.CGPathCreateMutable()
	coregraphics.CGPathAddRoundedRect(path, nil, rect, radius, radius)
	return coregraphics.CGPathRef(path)
}

func normalizeNextInteraction(next NextInteractionTiming, duration time.Duration) moveGate {
	if next == (NextInteractionTiming{}) {
		return moveGate{}
	}
	if next.DistancePx <= 0 {
		next.DistancePx = 2
	}
	if next.Progress <= 0 {
		next.Progress = 0.95
	}
	if next.Progress > 1 {
		next.Progress = 1
	}
	if next.IdleVelocityPPS <= 0 {
		next.IdleVelocityPPS = 40
	}
	if next.Dwell <= 0 {
		next.Dwell = 180 * time.Millisecond
	}
	if next.MaxWait <= 0 {
		next.MaxWait = duration + duration/2
	}
	if next.MaxWait <= 0 {
		next.MaxWait = 250 * time.Millisecond
	}
	return moveGate{
		enabled:         true,
		distancePx:      next.DistancePx,
		progress:        next.Progress,
		idleVelocityPPS: next.IdleVelocityPPS,
		dwell:           next.Dwell,
		maxWait:         next.MaxWait,
	}
}

func (g moveGate) shouldSignal(now time.Time, progress, distancePx, velocityPPS float64, state *moveGateState) bool {
	if !g.enabled || state == nil || state.signaled {
		return false
	}
	if g.maxWait > 0 && now.Sub(state.startedAt) >= g.maxWait {
		state.signaled = true
		return true
	}
	if progress < g.progress || distancePx > g.distancePx || velocityPPS > g.idleVelocityPPS {
		state.dwellStart = time.Time{}
		return false
	}
	if state.dwellStart.IsZero() {
		state.dwellStart = now
	}
	if now.Sub(state.dwellStart) < g.dwell {
		return false
	}
	state.signaled = true
	return true
}

func signalMoveReady(ch chan<- struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

func runOnMain(work func()) {
	if foundation.GetThreadClass().CurrentThread().IsMainThread() {
		work()
		return
	}
	done := make(chan struct{})
	dispatch.MainQueue().Async(func() {
		defer close(done)
		work()
	})
	<-done
}

func flashRect(rect corefoundation.CGRect, p palette, duration time.Duration) {
	if rect.Size.Width <= 0 || rect.Size.Height <= 0 {
		return
	}
	var win appkit.NSWindow
	runOnMain(func() {
		const margin = 12.0
		expanded := expandRect(rect, margin)
		// rect is AX / CG top-left coords; NSWindow wants AppKit bottom-left.
		primaryHeight := primaryDisplayHeight()
		flipped := corefoundation.CGRect{
			Origin: corefoundation.CGPoint{
				X: expanded.Origin.X,
				Y: primaryHeight - (expanded.Origin.Y + expanded.Size.Height),
			},
			Size: expanded.Size,
		}
		win = appkit.NewWindowWithContentRectStyleMaskBackingDefer(
			flipped,
			appkit.NSWindowStyleMaskBorderless,
			appkit.NSBackingStoreBuffered,
			false,
		)
		win.SetOpaque(false)
		win.SetBackgroundColor(appkit.NewColorWithSRGBRedGreenBlueAlpha(0, 0, 0, 0))
		win.SetHasShadow(false)
		win.SetIgnoresMouseEvents(true)
		win.SetReleasedWhenClosed(false)
		win.SetLevel(appkit.StatusWindowLevel)
		win.SetCollectionBehavior(windowCollectionBehavior())
		win.SetSharingType(defaultController.overlaySharingType())

		content := appkit.NSViewFromID(win.ContentView().GetID())
		main := corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: margin, Y: margin},
			Size:   rect.Size,
		}
		glow := appkit.NewBoxWithFrame(expandRect(main, 6))
		glow.SetBoxType(appkit.NSBoxCustom)
		glow.SetTitlePosition(appkit.NSNoTitle)
		glow.SetBorderWidth(0)
		glow.SetCornerRadius(16)
		glow.SetFillColor(appkit.NewColorWithSRGBRedGreenBlueAlpha(p.haloRed, p.haloGreen, p.haloBlue, 0.18))
		content.AddSubview(glow)

		box := appkit.NewBoxWithFrame(main)
		box.SetBoxType(appkit.NSBoxCustom)
		box.SetTitlePosition(appkit.NSNoTitle)
		box.SetBorderColor(appkit.NewColorWithSRGBRedGreenBlueAlpha(p.borderRed, p.borderGreen, p.borderBlue, 0.98))
		box.SetBorderWidth(4)
		box.SetCornerRadius(12)
		box.SetFillColor(appkit.NewColorWithSRGBRedGreenBlueAlpha(p.haloRed, p.haloGreen, p.haloBlue, 0.16))
		content.AddSubview(box)

		inner := appkit.NewBoxWithFrame(insetRect(main, 3))
		inner.SetBoxType(appkit.NSBoxCustom)
		inner.SetTitlePosition(appkit.NSNoTitle)
		inner.SetBorderColor(appkit.NewColorWithSRGBRedGreenBlueAlpha(1, 1, 1, 0.84))
		inner.SetBorderWidth(1.5)
		inner.SetCornerRadius(9)
		inner.SetFillColor(appkit.NewColorWithSRGBRedGreenBlueAlpha(1, 1, 1, 0))
		content.AddSubview(inner)

		win.SetAlphaValue(0)
		win.OrderFrontRegardless()
	})
	pulseWindow(win, duration)
	runOnMain(func() {
		if win.GetID() == 0 {
			return
		}
		win.OrderOut(nil)
		win.Close()
	})
}

func pulseWindow(win appkit.NSWindow, duration time.Duration) {
	if duration <= 0 {
		duration = captureFlashTime
	}
	const steps = 8
	fadeIn := duration / 3
	if fadeIn <= 0 {
		fadeIn = 70 * time.Millisecond
	}
	stepSleep := fadeIn / steps
	if stepSleep <= 0 {
		stepSleep = time.Millisecond
	}
	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		alpha := 0.28 + 0.72*(1-math.Pow(1-t, 2))
		runOnMain(func() {
			win.SetAlphaValue(alpha)
		})
		time.Sleep(stepSleep)
	}
	hold := duration - 2*fadeIn
	if hold > 0 {
		time.Sleep(hold)
	}
	for i := steps - 1; i >= 0; i-- {
		t := float64(i) / float64(steps)
		alpha := math.Pow(t, 1.6)
		runOnMain(func() {
			win.SetAlphaValue(alpha)
		})
		time.Sleep(stepSleep)
	}
}

func mouseInterventionMask() coregraphics.CGEventMask {
	types := []coregraphics.CGEventType{
		coregraphics.KCGEventMouseMoved,
		coregraphics.KCGEventLeftMouseDragged,
		coregraphics.KCGEventRightMouseDragged,
		coregraphics.KCGEventOtherMouseDragged,
	}
	var mask coregraphics.CGEventMask
	for _, typ := range types {
		mask |= 1 << uint(typ)
	}
	return mask
}

func ghostCursorInterventionCallback(_ uintptr, typ coregraphics.CGEventType, event uintptr, userInfo unsafe.Pointer) uintptr {
	switch typ {
	case coregraphics.KCGEventTapDisabledByTimeout, coregraphics.KCGEventTapDisabledByUserInput:
		if c := interventionController(userInfo); c != nil && c.interventionTap != 0 {
			coregraphics.CGEventTapEnable(c.interventionTap, true)
		}
		return event
	}
	c := interventionController(userInfo)
	if c == nil || event == 0 {
		return event
	}
	if pid := int(coregraphics.CGEventGetIntegerValueField(coregraphics.CGEventRef(event), coregraphics.KCGEventSourceUnixProcessID)); pid == os.Getpid() {
		return event
	}
	loc := coregraphics.CGEventGetLocation(coregraphics.CGEventRef(event))
	go c.handleUserIntervention(int(math.Round(loc.X)), int(math.Round(loc.Y)))
	return event
}

func registerInterventionController(c *Controller) uintptr {
	interventionRegistry.Lock()
	defer interventionRegistry.Unlock()
	if interventionRegistry.byID == nil {
		interventionRegistry.byID = make(map[uintptr]*Controller)
	}
	interventionRegistry.next++
	id := interventionRegistry.next
	interventionRegistry.byID[id] = c
	return id
}

func unregisterInterventionController(id uintptr) {
	if id == 0 {
		return
	}
	interventionRegistry.Lock()
	delete(interventionRegistry.byID, id)
	interventionRegistry.Unlock()
}

func interventionController(userInfo unsafe.Pointer) *Controller {
	id := uintptr(userInfo)
	if id == 0 {
		return nil
	}
	interventionRegistry.Lock()
	defer interventionRegistry.Unlock()
	return interventionRegistry.byID[id]
}

func colorPalette(dotRed, dotGreen, dotBlue, borderRed, borderGreen, borderBlue float64) palette {
	return palette{
		dotRed:      dotRed,
		dotGreen:    dotGreen,
		dotBlue:     dotBlue,
		haloRed:     dotRed,
		haloGreen:   dotGreen,
		haloBlue:    dotBlue,
		borderRed:   borderRed,
		borderGreen: borderGreen,
		borderBlue:  borderBlue,
	}
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

func (c *Controller) overlaySharingType() appkit.NSWindowSharingType {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.eyecandy.SharingVisible {
		return appkit.NSWindowSharingType(1)
	}
	return appkit.NSWindowSharingNone
}

func detectPalette(getpid func() int) palette {
	info := detectHarness(getpid)
	return paletteForID(paletteFamily(info.kind), info.paletteID)
}

func paletteForTheme(theme Theme, getpid func() int) palette {
	switch theme {
	case ThemeCodex:
		return paletteForID(codexPalettes, getpid())
	case ThemeClaude:
		return paletteForID(claudePalettes, getpid())
	case ThemeNeutral:
		return paletteForID([]palette{
			colorPalette(0.94, 0.97, 1.00, 0.58, 0.70, 0.86),
			colorPalette(0.95, 0.98, 1.00, 0.62, 0.74, 0.88),
		}, getpid())
	default:
		return detectPalette(getpid)
	}
}

func detectHostKind(getpid func() int) hostKind {
	return detectHarness(getpid).kind
}

func detectInfo(getpid func() int) Info {
	info := detectHarness(getpid)
	family := paletteFamily(info.kind)
	p := paletteForID(family, info.paletteID)
	return Info{
		Harness:      info.kind.String(),
		MatchName:    info.name,
		MatchPID:     info.pid,
		PaletteID:    info.paletteID,
		PaletteIndex: paletteIndex(family, info.paletteID),
		DotColor:     colorHex(p.dotRed, p.dotGreen, p.dotBlue),
		BorderColor:  colorHex(p.borderRed, p.borderGreen, p.borderBlue),
	}
}

func detectHarness(getpid func() int) harnessInfo {
	records := processAncestry(getpid)
	for _, record := range records {
		switch hostKindForProcessName(record.name) {
		case hostCodex:
			return harnessInfo{kind: hostCodex, pid: record.pid, name: record.name, paletteID: record.pid}
		case hostClaude:
			return harnessInfo{kind: hostClaude, pid: record.pid, name: record.name, paletteID: record.pid}
		}
	}
	if len(records) == 0 {
		return harnessInfo{}
	}
	if records[0].ppid > 1 {
		return harnessInfo{kind: hostUnknown, paletteID: records[0].ppid}
	}
	return harnessInfo{kind: hostUnknown, paletteID: records[0].pid}
}

func detectHostKindFromNames(names []string) hostKind {
	for _, name := range names {
		if kind := hostKindForProcessName(name); kind != hostUnknown {
			return kind
		}
	}
	return hostUnknown
}

func hostKindForProcessName(name string) hostKind {
	name = normalizeProcessName(name)
	switch {
	case name == "":
		return hostUnknown
	case strings.HasPrefix(name, "codex"):
		return hostCodex
	case strings.HasPrefix(name, "claude"):
		return hostClaude
	default:
		return hostUnknown
	}
}

func paletteForID(family []palette, id int) palette {
	if len(family) == 0 {
		return palette{}
	}
	return family[paletteIndex(family, id)]
}

func paletteIndex(family []palette, id int) int {
	if len(family) == 0 {
		return 0
	}
	if id < 0 {
		id = -id
	}
	return id % len(family)
}

func paletteFamily(kind hostKind) []palette {
	switch kind {
	case hostCodex:
		return codexPalettes
	case hostClaude:
		return claudePalettes
	default:
		return fallbackPalettes
	}
}

func processAncestry(getpid func() int) []processRecord {
	pid := getpid()
	if pid <= 0 {
		return nil
	}
	var records []processRecord
	seen := make(map[int]bool)
	for pid > 1 && !seen[pid] {
		seen[pid] = true
		name, ppid, err := processInfo(pid)
		if err != nil {
			break
		}
		records = append(records, processRecord{pid: pid, ppid: ppid, name: name})
		if ppid <= 1 {
			break
		}
		pid = ppid
	}
	return records
}

var processInfo = func(pid int) (name string, ppid int, err error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", 0, err
	}
	name = string(bytes.TrimRight(kp.Proc.P_comm[:], "\x00"))
	ppid = int(kp.Proc.P_oppid)
	return name, ppid, nil
}

func normalizeProcessName(name string) string {
	var out []rune
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
			out = append(out, r+'a'-'A')
		case r >= 'a' && r <= 'z':
			out = append(out, r)
		}
	}
	return string(out)
}

func (k hostKind) String() string {
	switch k {
	case hostCodex:
		return "codex"
	case hostClaude:
		return "claude"
	default:
		return "unknown"
	}
}

func colorHex(red, green, blue float64) string {
	return fmt.Sprintf("#%02x%02x%02x", colorByte(red), colorByte(green), colorByte(blue))
}

func colorByte(v float64) uint8 {
	switch {
	case v <= 0:
		return 0
	case v >= 1:
		return 255
	default:
		return uint8(math.Round(v * 255))
	}
}
