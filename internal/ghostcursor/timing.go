package ghostcursor

import (
	"math"
	"time"
)

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

func curveProgress(style CurveStyle, t float64) float64 {
	t = clamp01(t)
	switch style {
	case CurveLinear:
		return t
	default:
		return t * t * (3 - 2*t)
	}
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

func clampFloat(v, low, high float64) float64 {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

func easeOutCubic(t float64) float64 {
	t = clamp01(t)
	u := 1 - t
	return 1 - u*u*u
}

func lerpFloat(from, to, progress float64) float64 {
	return from + (to-from)*clamp01(progress)
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

func idleSwayOffset(time.Duration) (dx, dy float64) {
	return 0, 0
}

func isInactiveActivity(activity ActivityState) bool {
	switch activity {
	case ActivityIdle, ActivityThinking, ActivityPaused:
		return true
	default:
		return false
	}
}
