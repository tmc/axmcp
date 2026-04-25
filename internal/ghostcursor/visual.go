package ghostcursor

import (
	"math"
	"time"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/apple/quartzcore"
)

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

func cgColor(red, green, blue, alpha float64) coregraphics.CGColorRef {
	return coregraphics.CGColorCreateSRGB(red, green, blue, alpha)
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
