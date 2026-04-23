package ghostcursor

import (
	"math"
	"runtime"
	"testing"
	"time"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/coregraphics"
)

func TestWindowCollectionBehaviorAvoidsConflictingFlags(t *testing.T) {
	got := windowCollectionBehavior()
	if got&appkit.NSWindowCollectionBehaviorCanJoinAllSpaces != 0 &&
		got&appkit.NSWindowCollectionBehaviorMoveToActiveSpace != 0 {
		t.Fatalf("windowCollectionBehavior = %v, includes conflicting space flags", got)
	}
}

func TestCircleFrameCentersSize(t *testing.T) {
	got := circleFrame(10)
	if got.Origin.X != 23 || got.Origin.Y != 23 {
		t.Fatalf("circleFrame origin = (%v,%v), want (23,23)", got.Origin.X, got.Origin.Y)
	}
	if got.Size.Width != 10 || got.Size.Height != 10 {
		t.Fatalf("circleFrame size = (%v,%v), want (10,10)", got.Size.Width, got.Size.Height)
	}
}

func TestResolveScreenPoint(t *testing.T) {
	gotX, gotY, err := resolveScreenPoint(ScreenPosition(12, 34))
	if err != nil {
		t.Fatalf("resolveScreenPoint(screen) error = %v", err)
	}
	if gotX != 12 || gotY != 34 {
		t.Fatalf("resolveScreenPoint(screen) = (%d,%d), want (12,34)", gotX, gotY)
	}
	if _, _, err := resolveScreenPoint(Position{}); err == nil {
		t.Fatal("resolveScreenPoint(unknown) returned nil, want error")
	}
}

func TestTypingPositionForFrame(t *testing.T) {
	pos := TypingPositionForFrame(100, 200, 80, 20)
	if pos.Space != CoordinateSpaceScreen {
		t.Fatalf("TypingPositionForFrame space = %v, want screen", pos.Space)
	}
	if pos.X != 110 || pos.Y != 210 {
		t.Fatalf("TypingPositionForFrame = (%.0f,%.0f), want (110,210)", pos.X, pos.Y)
	}
}

func TestMoveSteps(t *testing.T) {
	if got := moveSteps(0, 0); got != 1 {
		t.Fatalf("moveSteps(0, 0) = %d, want 1", got)
	}
	if got := moveSteps(32, 250*time.Millisecond); got < 15 {
		t.Fatalf("moveSteps(32, 250ms) = %d, want at least 15", got)
	}
	if got := moveSteps(10000, 10*time.Second); got != 240 {
		t.Fatalf("moveSteps clamp = %d, want 240", got)
	}
}

func TestSamplePathLinearStaysOnAxis(t *testing.T) {
	path, err := SamplePath(ScreenPosition(0, 0), ScreenPosition(120, 0), MoveOptions{
		Duration:   200 * time.Millisecond,
		CurveStyle: CurveLinear,
	})
	if err != nil {
		t.Fatalf("SamplePath(linear) error = %v", err)
	}
	if len(path) < 2 {
		t.Fatalf("SamplePath(linear) length = %d, want at least 2", len(path))
	}
	if path[0] != ScreenPosition(0, 0) {
		t.Fatalf("SamplePath(linear) start = %#v, want screen origin", path[0])
	}
	if path[len(path)-1] != ScreenPosition(120, 0) {
		t.Fatalf("SamplePath(linear) end = %#v, want screen target", path[len(path)-1])
	}
	lastX := -1.0
	for i, step := range path {
		if step.Y != 0 {
			t.Fatalf("SamplePath(linear)[%d] y = %v, want 0", i, step.Y)
		}
		if step.X < lastX {
			t.Fatalf("SamplePath(linear)[%d] x = %v, want monotonic increase from %v", i, step.X, lastX)
		}
		lastX = step.X
	}
}

func TestSamplePathBezierArcsDeterministically(t *testing.T) {
	opts := MoveOptions{Duration: 220 * time.Millisecond}
	first, err := SamplePath(ScreenPosition(0, 0), ScreenPosition(140, 0), opts)
	if err != nil {
		t.Fatalf("SamplePath(first) error = %v", err)
	}
	second, err := SamplePath(ScreenPosition(0, 0), ScreenPosition(140, 0), opts)
	if err != nil {
		t.Fatalf("SamplePath(second) error = %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("SamplePath length mismatch = %d vs %d", len(first), len(second))
	}
	sawArc := false
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("SamplePath mismatch at %d: %#v != %#v", i, first[i], second[i])
		}
		if i > 0 && i+1 < len(first) && math.Abs(first[i].Y) >= 1 {
			sawArc = true
		}
	}
	if !sawArc {
		t.Fatal("SamplePath(bezier) never left the straight axis")
	}
}

func TestSamplePathSamePointReturnsSingleFrame(t *testing.T) {
	path, err := SamplePath(ScreenPosition(24, 48), ScreenPosition(24, 48), MoveOptions{
		Duration: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("SamplePath(same) error = %v", err)
	}
	if len(path) != 1 {
		t.Fatalf("SamplePath(same) length = %d, want 1", len(path))
	}
	if path[0] != ScreenPosition(24, 48) {
		t.Fatalf("SamplePath(same)[0] = %#v, want target", path[0])
	}
}

func TestBaseArrowPathGeometryMatchesWideSilhouette(t *testing.T) {
	bounds := coregraphics.CGPathGetBoundingBox(baseArrowPath())
	if bounds.Size.Width <= 10 {
		t.Fatalf("baseArrowPath width = %v, want broader silhouette", bounds.Size.Width)
	}
	if bounds.Size.Height >= 18 {
		t.Fatalf("baseArrowPath height = %v, want lower silhouette", bounds.Size.Height)
	}
	if ratio := bounds.Size.Width / bounds.Size.Height; ratio <= 0.85 {
		t.Fatalf("baseArrowPath width/height = %v, want close to square like the target", ratio)
	}
	if bounds.Origin.X < -3 || bounds.Origin.X > 0 || bounds.Origin.Y < -1 || bounds.Origin.Y > 0.5 {
		t.Fatalf("baseArrowPath origin = (%v,%v), want tip-adjacent contour with small left overhang and near-zero top boundary", bounds.Origin.X, bounds.Origin.Y)
	}
}

func TestCursorFogPathExtendsBeyondBody(t *testing.T) {
	body := coregraphics.CGPathGetBoundingBox(cursorPath(1))
	fog := coregraphics.CGPathGetBoundingBox(cursorFogPath(1))
	if fog.Size.Width <= body.Size.Width {
		t.Fatalf("fog width = %v, body width = %v, want wider fog envelope", fog.Size.Width, body.Size.Width)
	}
	if fog.Size.Height <= body.Size.Height {
		t.Fatalf("fog height = %v, body height = %v, want taller fog envelope", fog.Size.Height, body.Size.Height)
	}
}

func TestCursorTokensPressedAddsDensityWithoutSizeExplosion(t *testing.T) {
	tuning := TuningConfig{
		Brightness:     1,
		CursorScale:    1,
		BodyOpacity:    1,
		OutlineOpacity: 1,
		GlowOpacity:    1,
		GlowScale:      1,
		IdleFadeTime:   idleDimTime,
		MoveGlowTime:   moveGlowTime,
	}
	idle := cursorTokensForTuning(ActivityIdle, tuning)
	pressed := cursorTokensForTuning(ActivityPressed, tuning)
	if pressed.bodyScale-idle.bodyScale > 0.08 {
		t.Fatalf("pressed body scale = %v, idle = %v, want denser not much larger", pressed.bodyScale, idle.bodyScale)
	}
	if pressed.fogAlpha <= idle.fogAlpha {
		t.Fatalf("pressed fog alpha = %v, idle = %v, want stronger pressed density", pressed.fogAlpha, idle.fogAlpha)
	}
	if pressed.bodyAlpha <= idle.bodyAlpha {
		t.Fatalf("pressed body alpha = %v, idle = %v, want stronger pressed fill", pressed.bodyAlpha, idle.bodyAlpha)
	}
}

func TestCursorTokensPausedIsDimmerThanIdle(t *testing.T) {
	tuning := TuningConfig{
		Brightness:     1,
		CursorScale:    1,
		BodyOpacity:    1,
		OutlineOpacity: 1,
		GlowOpacity:    1,
		GlowScale:      1,
		IdleFadeTime:   idleDimTime,
		MoveGlowTime:   moveGlowTime,
	}
	idle := cursorTokensForTuning(ActivityIdle, tuning)
	paused := cursorTokensForTuning(ActivityPaused, tuning)
	if paused.bodyAlpha >= idle.bodyAlpha {
		t.Fatalf("paused body alpha = %v, idle = %v, want dimmer paused state", paused.bodyAlpha, idle.bodyAlpha)
	}
	if paused.fogAlpha >= idle.fogAlpha {
		t.Fatalf("paused fog alpha = %v, idle = %v, want dimmer paused fog", paused.fogAlpha, idle.fogAlpha)
	}
	if paused.outlineAlpha >= idle.outlineAlpha {
		t.Fatalf("paused outline alpha = %v, idle = %v, want dimmer paused outline", paused.outlineAlpha, idle.outlineAlpha)
	}
}

func TestVisualStateGlowBoostsMovingState(t *testing.T) {
	p := paletteForTheme(ThemeCodex, func() int { return 7 })
	tuning := DefaultTuningConfig()
	tuning.Brightness = 1
	idleGlow := visualStateForTuning(p, tuning, ActivityMoving, 0)
	moveGlow := visualStateForTuning(p, tuning, ActivityMoving, 1)
	if moveGlow.tokens.fogAlpha <= idleGlow.tokens.fogAlpha {
		t.Fatalf("moving fog alpha = %v, baseline = %v, want stronger move glow", moveGlow.tokens.fogAlpha, idleGlow.tokens.fogAlpha)
	}
	if moveGlow.tokens.outlineAlpha <= idleGlow.tokens.outlineAlpha {
		t.Fatalf("moving outline alpha = %v, baseline = %v, want stronger move highlight", moveGlow.tokens.outlineAlpha, idleGlow.tokens.outlineAlpha)
	}
	if moveGlow.tokens.fogShadowBlur <= idleGlow.tokens.fogShadowBlur {
		t.Fatalf("moving fog blur = %v, baseline = %v, want softer larger move glow", moveGlow.tokens.fogShadowBlur, idleGlow.tokens.fogShadowBlur)
	}
}

func TestShouldAnimateIdleDimming(t *testing.T) {
	if !shouldAnimateIdleDimming(ActivityMoving, ActivityIdle) {
		t.Fatal("moving -> idle should animate dimming")
	}
	if shouldAnimateIdleDimming(ActivityIdle, ActivityIdle) {
		t.Fatal("idle -> idle should not animate dimming")
	}
	if shouldAnimateIdleDimming(ActivityTyping, ActivityIdle) {
		t.Fatal("typing -> idle should not reuse cursor dimming animation")
	}
}

func TestMoveGlowFactorReturnsToBaseLevel(t *testing.T) {
	var c Controller
	base := c.moveGlowFactor(ActivityMoving)
	c.moveGlowStarted.Store(time.Now().Add(-moveGlowTime / 10).UnixNano())
	pulsed := c.moveGlowFactor(ActivityMoving)
	c.moveGlowStarted.Store(time.Now().Add(-moveGlowTime).UnixNano())
	settled := c.moveGlowFactor(ActivityMoving)
	if pulsed <= base {
		t.Fatalf("pulsed glow = %v, base = %v, want move-start pulse", pulsed, base)
	}
	if math.Abs(settled-base) > 0.02 {
		t.Fatalf("settled glow = %v, base = %v, want pulse to decay back to baseline", settled, base)
	}
}

func TestPaletteForThemeCodexUsesCoolFamily(t *testing.T) {
	got := paletteForTheme(ThemeCodex, func() int { return 42 })
	if got.dotBlue <= got.dotGreen {
		t.Fatalf("paletteForTheme(codex) = %#v, want cool family", got)
	}
}

func TestBlendAngleWrapsShortestDirection(t *testing.T) {
	got := blendAngle(179*math.Pi/180, -179*math.Pi/180, 0.5)
	if math.Abs(got-math.Pi) > 5*math.Pi/180 {
		t.Fatalf("blendAngle wrapped the long way: %v", got)
	}
}

func TestSamplePathBezierKeepsSwoopBounded(t *testing.T) {
	path, err := SamplePath(ScreenPosition(0, 0), ScreenPosition(220, 40), MoveOptions{
		Duration: 320 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("SamplePath(bezier) error = %v", err)
	}
	maxOffset := 0.0
	for i := 1; i+1 < len(path); i++ {
		offset := math.Abs(path[i].Y - (40.0*path[i].X)/220.0)
		if offset > maxOffset {
			maxOffset = offset
		}
	}
	if maxOffset <= 2 {
		t.Fatalf("SamplePath(bezier) max offset = %v, want a visible swoop", maxOffset)
	}
	if maxOffset >= 28 {
		t.Fatalf("SamplePath(bezier) max offset = %v, want bounded lateral offset", maxOffset)
	}
}

func TestNormalizeNextInteractionDefaults(t *testing.T) {
	got := normalizeNextInteraction(NextInteractionTiming{DistancePx: 1}, 200*time.Millisecond)
	if !got.enabled {
		t.Fatal("normalizeNextInteraction did not enable gate")
	}
	if got.distancePx != 1 {
		t.Fatalf("distancePx = %v, want 1", got.distancePx)
	}
	if got.progress != 0.95 {
		t.Fatalf("progress = %v, want 0.95", got.progress)
	}
	if got.idleVelocityPPS != 40 {
		t.Fatalf("idleVelocityPPS = %v, want 40", got.idleVelocityPPS)
	}
	if got.dwell != 180*time.Millisecond {
		t.Fatalf("dwell = %v, want 180ms", got.dwell)
	}
	if got.maxWait != 300*time.Millisecond {
		t.Fatalf("maxWait = %v, want 300ms", got.maxWait)
	}
}

func TestNormalizeNextInteractionPreservesExplicitValues(t *testing.T) {
	got := normalizeNextInteraction(NextInteractionTiming{
		DistancePx:      3,
		Progress:        0.97,
		IdleVelocityPPS: 24,
		Dwell:           32 * time.Millisecond,
		MaxWait:         80 * time.Millisecond,
	}, 200*time.Millisecond)
	if !got.enabled {
		t.Fatal("normalizeNextInteraction did not enable gate")
	}
	if got.distancePx != 3 {
		t.Fatalf("distancePx = %v, want 3", got.distancePx)
	}
	if got.progress != 0.97 {
		t.Fatalf("progress = %v, want 0.97", got.progress)
	}
	if got.idleVelocityPPS != 24 {
		t.Fatalf("idleVelocityPPS = %v, want 24", got.idleVelocityPPS)
	}
	if got.dwell != 32*time.Millisecond {
		t.Fatalf("dwell = %v, want 32ms", got.dwell)
	}
	if got.maxWait != 80*time.Millisecond {
		t.Fatalf("maxWait = %v, want 80ms", got.maxWait)
	}
}

func TestNormalizeNextInteractionClampsProgress(t *testing.T) {
	got := normalizeNextInteraction(NextInteractionTiming{Progress: 2}, 200*time.Millisecond)
	if got.progress != 1 {
		t.Fatalf("progress = %v, want 1", got.progress)
	}
}

func TestMoveGateSignalsAfterDwell(t *testing.T) {
	gate := normalizeNextInteraction(NextInteractionTiming{}, 200*time.Millisecond)
	if gate.enabled {
		t.Fatal("zero NextInteractionTiming unexpectedly enabled")
	}
	gate = normalizeNextInteraction(NextInteractionTiming{Progress: 0.9}, 100*time.Millisecond)
	state := moveGateState{startedAt: time.Unix(0, 0)}
	now := state.startedAt.Add(20 * time.Millisecond)
	if gate.shouldSignal(now, 0.95, 1, 10, &state) {
		t.Fatal("gate signaled before dwell elapsed")
	}
	if state.dwellStart.IsZero() {
		t.Fatal("gate did not start dwell timer")
	}
	now = state.dwellStart.Add(gate.dwell)
	if !gate.shouldSignal(now, 0.95, 1, 10, &state) {
		t.Fatal("gate did not signal after dwell elapsed")
	}
	if gate.shouldSignal(now.Add(time.Millisecond), 0.95, 1, 10, &state) {
		t.Fatal("gate signaled more than once")
	}
}

func TestMoveGateRequiresContinuousDwell(t *testing.T) {
	gate := normalizeNextInteraction(NextInteractionTiming{
		Progress: 0.9,
		Dwell:    20 * time.Millisecond,
	}, 100*time.Millisecond)
	state := moveGateState{startedAt: time.Unix(0, 0)}
	now := state.startedAt.Add(10 * time.Millisecond)
	if gate.shouldSignal(now, 0.95, 1, 10, &state) {
		t.Fatal("gate signaled before dwell elapsed")
	}
	if state.dwellStart.IsZero() {
		t.Fatal("gate did not start dwell timer")
	}
	if gate.shouldSignal(now.Add(5*time.Millisecond), 0.85, 1, 10, &state) {
		t.Fatal("gate signaled after progress regressed")
	}
	if !state.dwellStart.IsZero() {
		t.Fatal("gate kept dwell timer after thresholds regressed")
	}
	resume := now.Add(7 * time.Millisecond)
	if gate.shouldSignal(resume, 0.95, 1, 10, &state) {
		t.Fatal("gate signaled immediately after thresholds recovered")
	}
	if state.dwellStart != resume {
		t.Fatalf("dwellStart = %v, want %v", state.dwellStart, resume)
	}
	if !gate.shouldSignal(resume.Add(gate.dwell), 0.95, 1, 10, &state) {
		t.Fatal("gate did not require a fresh dwell after thresholds recovered")
	}
}

func TestMoveGateSignalsOnMaxWait(t *testing.T) {
	gate := normalizeNextInteraction(NextInteractionTiming{MaxWait: 50 * time.Millisecond}, 200*time.Millisecond)
	state := moveGateState{startedAt: time.Unix(0, 0)}
	now := state.startedAt.Add(60 * time.Millisecond)
	if !gate.shouldSignal(now, 0.2, 20, 500, &state) {
		t.Fatal("gate did not signal after max wait")
	}
}

func TestNormalizeEyecandyDisablesUnsupportedFlagsOnAMD64(t *testing.T) {
	got := normalizeEyecandy(EyecandyConfig{
		HolographicOCR: true,
		LiquidLens:     true,
		RippleOnClick:  true,
	})
	if got.LiquidLens {
		t.Fatal("LiquidLens stayed enabled")
	}
	if runtime.GOARCH == "amd64" && got.HolographicOCR {
		t.Fatal("HolographicOCR stayed enabled on amd64")
	}
	if !got.RippleOnClick {
		t.Fatal("RippleOnClick changed unexpectedly")
	}
}

func TestDefaultEyecandyConfigMakesSharingVisible(t *testing.T) {
	got := DefaultEyecandyConfig()
	if !got.SharingVisible {
		t.Fatal("SharingVisible stayed disabled in default eyecandy config")
	}
}

func TestDefaultTuningConfigProvidesPositiveDefaults(t *testing.T) {
	got := DefaultTuningConfig()
	if got.Brightness <= 0 {
		t.Fatalf("Brightness = %v, want positive", got.Brightness)
	}
	if got.CursorScale <= 0 {
		t.Fatalf("CursorScale = %v, want positive", got.CursorScale)
	}
	if got.BodyOpacity <= 0 {
		t.Fatalf("BodyOpacity = %v, want positive", got.BodyOpacity)
	}
	if got.OutlineOpacity <= 0 {
		t.Fatalf("OutlineOpacity = %v, want positive", got.OutlineOpacity)
	}
	if got.GlowOpacity <= 0 {
		t.Fatalf("GlowOpacity = %v, want positive", got.GlowOpacity)
	}
	if got.GlowScale <= 0 {
		t.Fatalf("GlowScale = %v, want positive", got.GlowScale)
	}
	if got.IdleFadeTime <= 0 {
		t.Fatalf("IdleFadeTime = %v, want positive", got.IdleFadeTime)
	}
	if got.MoveGlowTime <= 0 {
		t.Fatalf("MoveGlowTime = %v, want positive", got.MoveGlowTime)
	}
}

func TestNormalizeTuningZeroUsesDefaults(t *testing.T) {
	got := normalizeTuning(TuningConfig{})
	want := DefaultTuningConfig()
	if got != want {
		t.Fatalf("normalizeTuning(zero) = %#v, want %#v", got, want)
	}
}

func TestNormalizeTuningPartialUsesPerFieldDefaults(t *testing.T) {
	defaults := DefaultTuningConfig()
	got := normalizeTuning(TuningConfig{
		Brightness: 3.1,
	})
	if got.Brightness != 3.1 {
		t.Fatalf("Brightness = %v, want explicit override", got.Brightness)
	}
	if got.CursorScale != defaults.CursorScale {
		t.Fatalf("CursorScale = %v, want default %v", got.CursorScale, defaults.CursorScale)
	}
	if got.BodyOpacity != defaults.BodyOpacity {
		t.Fatalf("BodyOpacity = %v, want default %v", got.BodyOpacity, defaults.BodyOpacity)
	}
	if got.OutlineOpacity != defaults.OutlineOpacity {
		t.Fatalf("OutlineOpacity = %v, want default %v", got.OutlineOpacity, defaults.OutlineOpacity)
	}
	if got.GlowOpacity != defaults.GlowOpacity {
		t.Fatalf("GlowOpacity = %v, want default %v", got.GlowOpacity, defaults.GlowOpacity)
	}
	if got.GlowScale != defaults.GlowScale {
		t.Fatalf("GlowScale = %v, want default %v", got.GlowScale, defaults.GlowScale)
	}
	if got.IdleFadeTime != defaults.IdleFadeTime {
		t.Fatalf("IdleFadeTime = %v, want default %v", got.IdleFadeTime, defaults.IdleFadeTime)
	}
	if got.MoveGlowTime != defaults.MoveGlowTime {
		t.Fatalf("MoveGlowTime = %v, want default %v", got.MoveGlowTime, defaults.MoveGlowTime)
	}
}

func TestCursorTokensRespectTuningScales(t *testing.T) {
	base := cursorTokensForTuning(ActivityMoving, TuningConfig{
		Brightness:     1,
		CursorScale:    1,
		BodyOpacity:    1,
		OutlineOpacity: 1,
		GlowOpacity:    1,
		GlowScale:      1,
		IdleFadeTime:   idleDimTime,
		MoveGlowTime:   moveGlowTime,
	})
	tuned := cursorTokensForTuning(ActivityMoving, TuningConfig{
		Brightness:     1.2,
		CursorScale:    1.35,
		BodyOpacity:    1.1,
		OutlineOpacity: 1.3,
		GlowOpacity:    1.6,
		GlowScale:      1.4,
		IdleFadeTime:   idleDimTime,
		MoveGlowTime:   moveGlowTime,
	})
	if tuned.bodyAlpha <= base.bodyAlpha {
		t.Fatalf("bodyAlpha = %v, base = %v, want brighter tuned body", tuned.bodyAlpha, base.bodyAlpha)
	}
	if tuned.bodyScale <= base.bodyScale {
		t.Fatalf("bodyScale = %v, base = %v, want larger tuned cursor", tuned.bodyScale, base.bodyScale)
	}
	if tuned.outlineAlpha <= base.outlineAlpha {
		t.Fatalf("outlineAlpha = %v, base = %v, want brighter tuned outline", tuned.outlineAlpha, base.outlineAlpha)
	}
	if tuned.fogAlpha <= base.fogAlpha {
		t.Fatalf("fogAlpha = %v, base = %v, want stronger tuned glow", tuned.fogAlpha, base.fogAlpha)
	}
	if tuned.fogScale <= base.fogScale {
		t.Fatalf("fogScale = %v, base = %v, want larger tuned glow", tuned.fogScale, base.fogScale)
	}
	if tuned.outlineWidth <= base.outlineWidth {
		t.Fatalf("outlineWidth = %v, base = %v, want larger tuned cursor footprint", tuned.outlineWidth, base.outlineWidth)
	}
}

func TestHandleUserInterventionKeepsCurrentPosition(t *testing.T) {
	t.Skip("requires a live AppKit window stack")
}

func TestIdleSwayOffsetDisabled(t *testing.T) {
	for _, elapsed := range []time.Duration{0, 350 * time.Millisecond, 900 * time.Millisecond, 1500 * time.Millisecond} {
		dx, dy := idleSwayOffset(elapsed)
		if dx != 0 || dy != 0 {
			t.Fatalf("idleSwayOffset(%v) = (%v,%v), want no sway", elapsed, dx, dy)
		}
	}
}

func TestSystemArrowCursorMaskGeometry(t *testing.T) {
	mask, err := systemArrowCursorMask()
	if err != nil {
		t.Fatalf("systemArrowCursorMask error = %v", err)
	}
	if mask.bounds.Empty() {
		t.Fatal("systemArrowCursorMask returned empty bounds")
	}
	if ratio := float64(mask.bounds.Dx()) / float64(mask.bounds.Dy()); ratio <= 0.4 || ratio >= 1.1 {
		t.Fatalf("systemArrowCursorMask width/height = %v, want a compact arrow silhouette", ratio)
	}
	if mask.hotX < 0 || mask.hotX > float64(mask.width)/2 {
		t.Fatalf("systemArrowCursorMask hotX = %v, want tip near the left edge", mask.hotX)
	}
	if mask.hotY < 0 || mask.hotY > float64(mask.height)/2 {
		t.Fatalf("systemArrowCursorMask hotY = %v, want tip in the upper half", mask.hotY)
	}
}

func TestDetectHostKindFromNames(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  hostKind
	}{
		{name: "codex ancestor", input: []string{"bash", "codex", "launchd"}, want: hostCodex},
		{name: "codex helper substring", input: []string{"bash", "codex-agent"}, want: hostCodex},
		{name: "claude ancestor", input: []string{"node", "Claude", "launchd"}, want: hostClaude},
		{name: "claude code collapsed", input: []string{"bash", "Claude Code"}, want: hostClaude},
		{name: "unknown", input: []string{"bash", "tmux"}, want: hostUnknown},
	}
	for _, tt := range tests {
		if got := detectHostKindFromNames(tt.input); got != tt.want {
			t.Fatalf("%s: detectHostKindFromNames(%v) = %v, want %v", tt.name, tt.input, got, tt.want)
		}
	}
}

func TestDetectPaletteUsesCodexCoolPalette(t *testing.T) {
	oldProcessInfo := processInfo
	processInfo = func(pid int) (string, int, error) {
		switch pid {
		case 10:
			return "bash", 20, nil
		case 20:
			return "codex", 1, nil
		default:
			return "", 0, nil
		}
	}
	defer func() { processInfo = oldProcessInfo }()

	got := detectPalette(func() int { return 10 })
	if got.dotBlue <= got.dotGreen {
		t.Fatalf("detectPalette(codex) = %#v, want cool blue palette", got)
	}
}

func TestDetectHarnessUsesNearestRecognizedAncestor(t *testing.T) {
	oldProcessInfo := processInfo
	processInfo = func(pid int) (string, int, error) {
		switch pid {
		case 10:
			return "axmcp", 30, nil
		case 30:
			return "bash", 42, nil
		case 42:
			return "Codex", 1, nil
		default:
			return "", 0, nil
		}
	}
	defer func() { processInfo = oldProcessInfo }()

	got := detectHarness(func() int { return 10 })
	want := harnessInfo{kind: hostCodex, pid: 42, name: "Codex", paletteID: 42}
	if got != want {
		t.Fatalf("detectHarness(codex) = %#v, want %#v", got, want)
	}
}

func TestDetectInfoReportsPaletteSelection(t *testing.T) {
	oldProcessInfo := processInfo
	processInfo = func(pid int) (string, int, error) {
		switch pid {
		case 10:
			return "ghost-cursor-test", 30, nil
		case 30:
			return "Codex Helper", 1, nil
		default:
			return "", 0, nil
		}
	}
	defer func() { processInfo = oldProcessInfo }()

	got := detectInfo(func() int { return 10 })
	if got.Harness != "codex" {
		t.Fatalf("detectInfo harness = %q, want codex", got.Harness)
	}
	if got.MatchName != "Codex Helper" || got.MatchPID != 30 {
		t.Fatalf("detectInfo match = (%q,%d), want (Codex Helper,30)", got.MatchName, got.MatchPID)
	}
	if got.PaletteID != 30 || got.PaletteIndex != paletteIndex(codexPalettes, 30) {
		t.Fatalf("detectInfo palette = (%d,%d), want (%d,%d)", got.PaletteID, got.PaletteIndex, 30, paletteIndex(codexPalettes, 30))
	}
	if got.DotColor == "" || got.BorderColor == "" {
		t.Fatalf("detectInfo colors = (%q,%q), want non-empty", got.DotColor, got.BorderColor)
	}
}

func TestDetectPaletteIndexesClaudeFamilyByPID(t *testing.T) {
	oldProcessInfo := processInfo
	processInfo = func(pid int) (string, int, error) {
		switch pid {
		case 10:
			return "computer-use-mcp", 30, nil
		case 30:
			return "Claude Code", 1, nil
		default:
			return "", 0, nil
		}
	}
	defer func() { processInfo = oldProcessInfo }()

	got := detectPalette(func() int { return 10 })
	want := paletteForID(claudePalettes, 30)
	if got != want {
		t.Fatalf("detectPalette(claude) = %#v, want %#v", got, want)
	}
}

func TestDetectPaletteFallsBackToParentPID(t *testing.T) {
	oldProcessInfo := processInfo
	processInfo = func(pid int) (string, int, error) {
		switch pid {
		case 10:
			return "axmcp", 77, nil
		case 77:
			return "zsh", 1, nil
		default:
			return "", 0, nil
		}
	}
	defer func() { processInfo = oldProcessInfo }()

	got := detectPalette(func() int { return 10 })
	want := paletteForID(fallbackPalettes, 77)
	if got != want {
		t.Fatalf("detectPalette(fallback) = %#v, want %#v", got, want)
	}
}
