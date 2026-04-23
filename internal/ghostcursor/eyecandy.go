package ghostcursor

import (
	"time"

	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/apple/quartzcore"
)

const clickRippleDuration = 420 * time.Millisecond

func (c *Controller) maybeTriggerRipple(prev, next ActivityState) {
	if !c.eyecandy.RippleOnClick || c.win.GetID() == 0 {
		return
	}
	if next != ActivityPressed && prev != ActivityPressed {
		return
	}
	now := time.Now()
	if !c.lastRipple.IsZero() && now.Sub(c.lastRipple) < 60*time.Millisecond {
		return
	}
	c.lastRipple = now
	c.playRipple()
}

func (c *Controller) playRipple() {
	if c.root.ID == 0 {
		return
	}
	primary := quartzcore.NewCAShapeLayer()
	primary.SetFillColor(0)
	primary.SetStrokeColor(cgColor(c.palette.borderRed, c.palette.borderGreen, c.palette.borderBlue, 0.94))
	primary.SetLineWidth(2.8)
	primary.SetOpacity(0.9)
	primary.SetPath(ellipsePath(circleFrame(16)))
	c.root.AddSublayer(primary)

	secondary := quartzcore.NewCAShapeLayer()
	secondary.SetFillColor(0)
	secondary.SetStrokeColor(cgColor(c.palette.dotRed, c.palette.dotGreen, c.palette.dotBlue, 0.65))
	secondary.SetLineWidth(1.5)
	secondary.SetOpacity(0.55)
	secondary.SetPath(ellipsePath(circleFrame(10)))
	c.root.AddSublayer(secondary)

	go func(outer, inner quartzcore.CAShapeLayer) {
		const steps = 7
		sleep := clickRippleDuration / steps
		if sleep <= 0 {
			sleep = time.Millisecond
		}
		for i := 1; i <= steps; i++ {
			progress := float64(i) / float64(steps)
			outerSize := 16.0 + 30.0*progress
			innerSize := 10.0 + 18.0*progress
			outerAlpha := 0.90 * (1 - progress)
			innerAlpha := 0.55 * (1 - progress*0.9)
			runOnMain(func() {
				outer.SetPath(ellipsePath(circleFrame(outerSize)))
				outer.SetOpacity(float32(outerAlpha))
				inner.SetPath(ellipsePath(circleFrame(innerSize)))
				inner.SetOpacity(float32(innerAlpha))
			})
			time.Sleep(sleep)
		}
		runOnMain(func() {
			outer.RemoveFromSuperlayer()
			inner.RemoveFromSuperlayer()
		})
	}(primary, secondary)
}

func ellipsePath(rect corefoundation.CGRect) coregraphics.CGPathRef {
	path := coregraphics.CGPathCreateMutable()
	coregraphics.CGPathAddEllipseInRect(path, nil, rect)
	return coregraphics.CGPathRef(path)
}
