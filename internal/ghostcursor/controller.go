package ghostcursor

import (
	"context"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objectivec"
	"github.com/tmc/apple/quartzcore"
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

func (c *Controller) overlaySharingType() appkit.NSWindowSharingType {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.eyecandy.SharingVisible {
		return appkit.NSWindowSharingType(1)
	}
	return appkit.NSWindowSharingNone
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
