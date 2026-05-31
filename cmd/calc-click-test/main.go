//go:build darwin

package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/computeruse/input"
	"github.com/tmc/axmcp/internal/ghostcursor"
	"github.com/tmc/axmcp/internal/macsigning"
	"github.com/tmc/axmcp/internal/ui"
	"github.com/tmc/macgo"
)

const calculatorBundleID = "com.apple.calculator"

type config struct {
	sequence         string
	slow             time.Duration
	pause            time.Duration
	idleWait         time.Duration
	repeat           int
	endWait          time.Duration
	settle           time.Duration
	launchWait       time.Duration
	axWait           time.Duration
	nextInteraction  bool
	nextDistance     float64
	nextProgress     float64
	nextIdleVelocity float64
	nextDwell        time.Duration
	nextMaxWait      time.Duration
	ripple           bool
	trail            bool
	tilt             bool
	brightness       float64
	cursorScale      float64
	bodyOpacity      float64
	outlineOpacity   float64
	glowOpacity      float64
	glowScale        float64
	fadeDelay        time.Duration
	fadeDuration     time.Duration
	moveGlowDuration time.Duration
	screenshotDir    string
	video            bool
	videoFPS         int
	screenshotSettle time.Duration
	screenshotPad    float64
	screenWait       time.Duration
}

type stepTarget struct {
	label   string
	button  *axuiautomation.Element
	centerX int
	centerY int
}

func main() {
	runtime.LockOSThread()

	cfg := config{}
	tuning := ghostcursor.DefaultTuningConfig()
	flag.StringVar(&cfg.sequence, "sequence", "3,7", "buttons to click; use commas for multi-character labels")
	flag.DurationVar(&cfg.slow, "slow", 280*time.Millisecond, "cursor travel time between calculator buttons")
	flag.DurationVar(&cfg.pause, "pause", 120*time.Millisecond, "pause after each click")
	flag.DurationVar(&cfg.idleWait, "idle-wait", 1400*time.Millisecond, "extra idle hold between clicks so the cursor has time to dim")
	flag.IntVar(&cfg.repeat, "repeat", 0, "number of times to replay the flow; 0 repeats forever")
	flag.DurationVar(&cfg.endWait, "end-wait", 1600*time.Millisecond, "idle hold after the last click before the flow repeats")
	flag.DurationVar(&cfg.settle, "settle", 90*time.Millisecond, "pause after the move completes before clicking")
	flag.DurationVar(&cfg.launchWait, "launch-wait", 4*time.Second, "time to wait for Calculator to launch")
	flag.DurationVar(&cfg.axWait, "ax-wait", 8*time.Second, "time to wait for Accessibility permission")
	flag.BoolVar(&cfg.nextInteraction, "next-interaction", false, "let MoveTo return early once the close-enough gate is satisfied")
	flag.Float64Var(&cfg.nextDistance, "next-distance", 2, "close-enough distance in pixels when -next-interaction is enabled")
	flag.Float64Var(&cfg.nextProgress, "next-progress", 0.95, "minimum progress before early return when -next-interaction is enabled")
	flag.Float64Var(&cfg.nextIdleVelocity, "next-idle-velocity", 30, "maximum cursor speed in px/s before early return when -next-interaction is enabled")
	flag.DurationVar(&cfg.nextDwell, "next-dwell", 32*time.Millisecond, "close-enough dwell before early return when -next-interaction is enabled")
	flag.DurationVar(&cfg.nextMaxWait, "next-max-wait", 0, "upper bound for early return when -next-interaction is enabled; 0 uses the controller default")
	flag.BoolVar(&cfg.ripple, "ripple", false, "enable click ripple")
	flag.BoolVar(&cfg.trail, "trail", false, "enable comet trail")
	flag.BoolVar(&cfg.tilt, "tilt", false, "enable velocity tilt")
	flag.Float64Var(&cfg.brightness, "brightness", tuning.Brightness, "overall cursor brightness multiplier")
	flag.Float64Var(&cfg.cursorScale, "cursor-scale", tuning.CursorScale, "overall cursor size multiplier")
	flag.Float64Var(&cfg.bodyOpacity, "body-opacity", tuning.BodyOpacity, "cursor fill opacity multiplier")
	flag.Float64Var(&cfg.outlineOpacity, "outline-opacity", tuning.OutlineOpacity, "cursor outline opacity multiplier")
	flag.Float64Var(&cfg.glowOpacity, "glow-opacity", tuning.GlowOpacity, "cursor glow opacity multiplier")
	flag.Float64Var(&cfg.glowScale, "glow-scale", tuning.GlowScale, "cursor glow size multiplier")
	flag.DurationVar(&cfg.fadeDelay, "fade-delay", tuning.IdleFadeDelay, "delay before the cursor starts dimming after going idle")
	flag.DurationVar(&cfg.fadeDuration, "fade-duration", tuning.IdleFadeTime, "time for the active-to-idle dimming animation")
	flag.DurationVar(&cfg.moveGlowDuration, "move-glow-duration", tuning.MoveGlowTime, "length of the move-glow pulse")
	flag.StringVar(&cfg.screenshotDir, "screenshot-dir", "", "directory for phase screenshots; empty disables screenshot capture")
	flag.BoolVar(&cfg.video, "video", false, "record a run.mp4 next to the phase screenshots")
	flag.IntVar(&cfg.videoFPS, "video-fps", 30, "target frame rate for recorded video")
	flag.DurationVar(&cfg.screenshotSettle, "screenshot-settle", 90*time.Millisecond, "delay before each saved screenshot so the overlay settles")
	flag.Float64Var(&cfg.screenshotPad, "screenshot-padding", 16, "extra pixels around the Calculator window in saved screenshots")
	flag.DurationVar(&cfg.screenWait, "screen-wait", 8*time.Second, "time to wait for Screen Recording permission when screenshots are enabled")
	flag.Parse()

	steps := parseSequence(cfg.sequence)
	if len(steps) == 0 {
		fmt.Fprintln(os.Stderr, "calc-click-test: empty sequence")
		os.Exit(2)
	}
	if cfg.video && strings.TrimSpace(cfg.screenshotDir) == "" {
		fmt.Fprintln(os.Stderr, "calc-click-test: -video requires -screenshot-dir")
		os.Exit(2)
	}
	if cfg.videoFPS <= 0 {
		fmt.Fprintln(os.Stderr, "calc-click-test: -video-fps must be positive")
		os.Exit(2)
	}

	macgoCfg := macgo.NewConfig().
		WithAppName("calc-click-test").
		WithPermissions(macgo.Accessibility).
		WithUsageDescription("NSAccessibilityUsageDescription", "calc-click-test uses Accessibility to inspect Calculator buttons and drive the ghost cursor demo.").
		WithInfo("NSSupportsAutomaticTermination", false).
		WithUIMode(macgo.UIModeAccessory)
	macgoCfg.BundleID = "dev.tmc.calcclicktest"
	macgoCfg = macsigning.Configure(macgoCfg)
	ui.ConfigureIdentity("calc-click-test", macgoCfg.BundleID)
	if err := macgo.Start(macgoCfg); err != nil {
		fmt.Fprintf(os.Stderr, "calc-click-test: macgo start failed: %v\n", err)
		os.Exit(1)
	}

	var allowTerminate atomic.Bool
	runResult := make(chan error, 1)
	procInfo := foundation.GetProcessInfoClass().ProcessInfo()
	procInfo.SetAutomaticTerminationSupportEnabled(false)
	procInfo.DisableAutomaticTermination("calc-click-test")
	procInfo.DisableSuddenTermination()
	_ = procInfo.BeginActivityWithOptionsReason(
		foundation.NSActivitySuddenTerminationDisabled|foundation.NSActivityAutomaticTerminationDisabled,
		"calc-click-test",
	)

	app := appkit.GetNSApplicationClass().SharedApplication()
	app.SetActivationPolicy(appkit.NSApplicationActivationPolicyRegular)
	delegate := appkit.NewNSApplicationDelegate(appkit.NSApplicationDelegateConfig{
		DidFinishLaunching: func(_ foundation.NSNotification) {
			ghostcursor.Configure(ghostcursor.Config{
				Enabled: true,
				Theme:   ghostcursor.ThemeCodex,
				Eyecandy: ghostcursor.EyecandyConfig{
					SharingVisible: true,
					RippleOnClick:  cfg.ripple,
					CometTrail:     cfg.trail,
					VelocityTilt:   cfg.tilt,
				},
				Tuning: ghostcursor.TuningConfig{
					Brightness:     cfg.brightness,
					CursorScale:    cfg.cursorScale,
					BodyOpacity:    cfg.bodyOpacity,
					OutlineOpacity: cfg.outlineOpacity,
					GlowOpacity:    cfg.glowOpacity,
					GlowScale:      cfg.glowScale,
					IdleFadeDelay:  cfg.fadeDelay,
					IdleFadeTime:   cfg.fadeDuration,
					MoveGlowTime:   cfg.moveGlowDuration,
				},
			})
			go func() {
				runResult <- run(cfg, steps)
				allowTerminate.Store(true)
				dispatch.MainQueue().Async(func() {
					app.Terminate(nil)
				})
			}()
		},
		ShouldTerminate: func(app appkit.NSApplication) appkit.NSApplicationTerminateReply {
			if allowTerminate.Load() {
				return appkit.NSTerminateNow
			}
			return ui.ShouldTerminateReply(app)
		},
		ShouldTerminateAfterLastWindowClosed: func(_ appkit.NSApplication) bool {
			return false
		},
	})
	app.SetDelegate(delegate)
	for {
		app.Run()
		if allowTerminate.Load() {
			break
		}
	}
	runErr := <-runResult
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "calc-click-test:", runErr)
		os.Exit(1)
	}
}

func run(cfg config, steps []string) (err error) {
	defer ghostcursor.Hide()
	printVisualConfig(cfg)
	debugf("run start steps=%v repeat=%d screenshotDir=%q video=%v", steps, cfg.repeat, cfg.screenshotDir, cfg.video)
	if !ui.WaitForAccessibility(cfg.axWait) {
		return fmt.Errorf("accessibility permission not granted")
	}
	debugf("accessibility ready")
	capturer, err := newCapturer(cfg)
	if err != nil {
		return err
	}
	if capturer != nil {
		debugf("capturer ready dir=%s", capturer.dir)
	} else {
		debugf("capturer disabled")
	}
	if capturer != nil && !ui.WaitForScreenRecording(cfg.screenWait) {
		return fmt.Errorf("screen recording permission not granted")
	}
	if capturer != nil {
		debugf("screen recording ready for screenshots")
	}
	if cfg.video && !ui.WaitForScreenRecording(cfg.screenWait) {
		return fmt.Errorf("screen recording permission not granted")
	}
	if cfg.video {
		debugf("screen recording ready for video")
	}
	if err := launchCalculator(); err != nil {
		return err
	}
	debugf("calculator launch requested")

	app, err := waitForCalculator(cfg.launchWait)
	if err != nil {
		return err
	}
	defer app.Close()
	debugf("calculator app ready")

	win, err := waitForWindow(app, cfg.launchWait)
	if err != nil {
		return err
	}
	defer win.Release()
	debugf("calculator window ready frame=%+v", win.Frame())

	targets, err := resolveStepTargets(app, steps, cfg.launchWait)
	if err != nil {
		return err
	}
	defer releaseStepTargets(targets)
	debugf("resolved %d step targets", len(targets))

	recorder, err := newRecorder(cfg, win.Frame())
	if err != nil {
		return err
	}
	if recorder != nil {
		debugf("starting recorder")
		if err := recorder.Start(); err != nil {
			return err
		}
		debugf("recorder started")
		defer func() {
			if stopErr := recorder.Stop(); err == nil && stopErr != nil {
				err = stopErr
			}
		}()
	}

	start := startPoint(win)
	if err := ghostcursor.Default().Show(
		ghostcursor.ScreenPosition(start.X, start.Y),
		ghostcursor.ActivityIdle,
		0,
	); err != nil {
		return fmt.Errorf("show cursor: %w", err)
	}
	debugf("cursor shown start=%+v", start)
	time.Sleep(150 * time.Millisecond)

	moveOpts := ghostcursor.MoveOptions{
		Duration:   cfg.slow,
		Activity:   ghostcursor.ActivityMoving,
		CurveStyle: ghostcursor.CurveBezier,
	}
	if cfg.nextInteraction {
		moveOpts.NextInteraction = ghostcursor.NextInteractionTiming{
			DistancePx:      cfg.nextDistance,
			Progress:        cfg.nextProgress,
			IdleVelocityPPS: cfg.nextIdleVelocity,
			Dwell:           cfg.nextDwell,
			MaxWait:         cfg.nextMaxWait,
		}
	}

	cycle := 0
	for cfg.repeat <= 0 || cycle < cfg.repeat {
		debugf("cycle %d start", cycle+1)
		if err := capturePhase(capturer, win, cycle+1, 0, "", "phase-start"); err != nil {
			return err
		}
		debugf("cycle %d phase-start captured", cycle+1)
		for i, target := range targets {
			label := target.label
			button := target.button
			if button == nil {
				return fmt.Errorf("button %q target disappeared", label)
			}
			debugf("cycle %d step %d using button %q", cycle+1, i+1, label)

			move := ghostcursor.ScreenPosition(target.centerX, target.centerY)
			if cfg.slow > 0 {
				if err := ghostcursor.Default().MoveTo(context.Background(), move, moveOpts); err != nil {
					return fmt.Errorf("move to %q: %w", label, err)
				}
			} else {
				if err := ghostcursor.Default().Show(move, ghostcursor.ActivityIdle, 0); err != nil {
					return fmt.Errorf("jump to %q: %w", label, err)
				}
			}
			if cfg.settle > 0 {
				time.Sleep(cfg.settle)
			}
			if err := capturePhase(capturer, win, cycle+1, i+1, label, "before-click"); err != nil {
				return err
			}
			debugf("cycle %d step %d before-click captured", cycle+1, i+1)
			if err := input.ClickElement(button, "left", 1); err != nil {
				return fmt.Errorf("click %q: %w", label, err)
			}
			if err := capturePhase(capturer, win, cycle+1, i+1, label, "after-click"); err != nil {
				return err
			}
			debugf("cycle %d step %d after-click captured", cycle+1, i+1)
			if cfg.pause > 0 {
				time.Sleep(cfg.pause)
			}

			fmt.Printf("cycle %d clicked %q (%d/%d)\n", cycle+1, label, i+1, len(steps))
			wait := cfg.endWait
			if i+1 < len(steps) {
				wait = cfg.idleWait
			}
			if wait > 0 {
				if err := ghostcursor.Default().Show(move, ghostcursor.ActivityIdle, 0); err != nil {
					return fmt.Errorf("show idle on %q: %w", label, err)
				}
				if err := capturePhase(capturer, win, cycle+1, i+1, label, "idle-start"); err != nil {
					return err
				}
				first, second := splitWait(wait)
				if first > 0 {
					time.Sleep(first)
				}
				if err := capturePhase(capturer, win, cycle+1, i+1, label, "idle-mid"); err != nil {
					return err
				}
				if second > 0 {
					time.Sleep(second)
				}
				if err := capturePhase(capturer, win, cycle+1, i+1, label, "idle-end"); err != nil {
					return err
				}
			}
		}
		if err := capturePhase(capturer, win, cycle+1, 0, "", "phase-end"); err != nil {
			return err
		}
		debugf("cycle %d phase-end captured", cycle+1)
		cycle++
	}

	ghostcursor.Hide()
	debugf("run complete")
	return nil
}

func printVisualConfig(cfg config) {
	fmt.Printf(
		"visual brightness=%.2f cursor-scale=%.2f body=%.2f outline=%.2f glow=%.2f glow-scale=%.2f fade-delay=%s fade-duration=%s move-glow-duration=%s\n",
		cfg.brightness,
		cfg.cursorScale,
		cfg.bodyOpacity,
		cfg.outlineOpacity,
		cfg.glowOpacity,
		cfg.glowScale,
		cfg.fadeDelay,
		cfg.fadeDuration,
		cfg.moveGlowDuration,
	)
	if cfg.screenshotDir != "" {
		fmt.Printf(
			"screenshots dir=%s settle=%s padding=%.0f\n",
			cfg.screenshotDir,
			cfg.screenshotSettle,
			cfg.screenshotPad,
		)
	}
	if cfg.video {
		fmt.Printf("video fps=%d\n", cfg.videoFPS)
	}
}

func debugf(format string, args ...any) {
	if os.Getenv("CALC_CLICK_TEST_DEBUG") == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "calc-click-test: "+format+"\n", args...)
}

func launchCalculator() error {
	cmd := exec.Command("open", "-g", "-a", "Calculator")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("launch Calculator: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func waitForCalculator(timeout time.Duration) (*axuiautomation.Application, error) {
	deadline := time.Now().Add(timeout)
	for {
		app, err := axuiautomation.NewApplication(calculatorBundleID)
		if err == nil && app != nil {
			return app, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("Calculator did not launch within %s", timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func waitForWindow(app *axuiautomation.Application, timeout time.Duration) (*axuiautomation.Element, error) {
	deadline := time.Now().Add(timeout)
	for {
		win := app.MainWindow()
		if win != nil && win.Exists() {
			return win, nil
		}
		if win != nil {
			win.Release()
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("Calculator window did not appear within %s", timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func waitForButton(app *axuiautomation.Application, label string, timeout time.Duration) (*axuiautomation.Element, error) {
	deadline := time.Now().Add(timeout)
	for {
		button := app.Buttons().Matching(func(e *axuiautomation.Element) bool {
			return e.Title() == label || e.Description() == label || e.Value() == label
		}).First()
		if button != nil && button.Exists() {
			return button, nil
		}
		if button != nil {
			button.Release()
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("button %q not found within %s", label, timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func resolveStepTargets(app *axuiautomation.Application, steps []string, timeout time.Duration) ([]stepTarget, error) {
	targets := make([]stepTarget, 0, len(steps))
	for _, label := range steps {
		button, err := waitForButton(app, label, timeout)
		if err != nil {
			releaseStepTargets(targets)
			return nil, err
		}
		centerX, centerY := button.Center()
		targets = append(targets, stepTarget{
			label:   label,
			button:  button,
			centerX: centerX,
			centerY: centerY,
		})
	}
	return targets, nil
}

func releaseStepTargets(targets []stepTarget) {
	for i := range targets {
		if targets[i].button == nil {
			continue
		}
		targets[i].button.Release()
		targets[i].button = nil
	}
}

func startPoint(win *axuiautomation.Element) input.LocalPoint {
	frame := win.Frame()
	x := int(frame.Origin.X + 24)
	y := int(frame.Origin.Y + 24)
	return input.LocalPoint{X: x, Y: y}
}

func parseSequence(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.Contains(raw, ",") {
		parts := strings.Split(raw, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
		return out
	}

	out := make([]string, 0, len(raw))
	for _, r := range raw {
		if unicode.IsSpace(r) {
			continue
		}
		out = append(out, string(r))
	}
	return out
}

type capturer struct {
	dir     string
	settle  time.Duration
	padding float64
}

func newCapturer(cfg config) (*capturer, error) {
	if strings.TrimSpace(cfg.screenshotDir) == "" {
		return nil, nil
	}
	dir, err := resolveCaptureDir(cfg.screenshotDir)
	if err != nil {
		return nil, fmt.Errorf("resolve screenshot dir: %w", err)
	}
	return &capturer{
		dir:     dir,
		settle:  cfg.screenshotSettle,
		padding: cfg.screenshotPad,
	}, nil
}

func capturePhase(c *capturer, win *axuiautomation.Element, cycle, step int, label, phase string) error {
	if c == nil {
		return nil
	}
	if c.settle > 0 {
		time.Sleep(c.settle)
	}
	path := filepath.Join(c.dir, captureFileName(cycle, step, label, phase))
	if err := captureWindowFrame(win.Frame(), c.padding, path); err != nil {
		return fmt.Errorf("capture %s: %w", filepath.Base(path), err)
	}
	fmt.Printf("saved %s\n", path)
	return nil
}

func resolveCaptureDir(raw string) (string, error) {
	dir, err := filepath.Abs(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func captureFileName(cycle, step int, label, phase string) string {
	base := fmt.Sprintf("cycle-%03d", cycle)
	if step > 0 {
		base = fmt.Sprintf("%s-step-%02d", base, step)
	}
	base = base + "-" + sanitizeFileToken(phase)
	if step > 0 && label != "" {
		base = base + "-" + sanitizeFileToken(label)
	}
	return base + ".png"
}

func sanitizeFileToken(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return "unnamed"
	}
	switch s {
	case "+/-":
		return "plus-minus"
	case "=":
		return "equals"
	case "%":
		return "percent"
	case ".":
		return "point"
	case "+":
		return "plus"
	case "-":
		return "minus"
	case "×", "*":
		return "times"
	case "÷", "/":
		return "divide"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unnamed"
	}
	return out
}

func splitWait(wait time.Duration) (time.Duration, time.Duration) {
	if wait <= 0 {
		return 0, 0
	}
	first := wait / 2
	return first, wait - first
}

func captureWindowFrame(frame axuiautomation.Rect, padding float64, path string) error {
	rectArg := screenCaptureRectArg(frame, padding)
	cmd := exec.Command("screencapture", "-x", "-R", rectArg, "-t", "png", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("screencapture %s: %v: %s", rectArg, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func captureRect(frame axuiautomation.Rect, padding float64) axuiautomation.Rect {
	minX := math.Max(0, frame.Origin.X-padding)
	minY := math.Max(0, frame.Origin.Y-padding)
	maxX := frame.Origin.X + frame.Size.Width + padding
	maxY := frame.Origin.Y + frame.Size.Height + padding
	minX = math.Floor(minX)
	minY = math.Floor(minY)
	maxX = math.Ceil(maxX)
	maxY = math.Ceil(maxY)
	return axuiautomation.Rect{
		Origin: axuiautomation.Point{X: minX, Y: minY},
		Size: axuiautomation.Size{
			Width:  maxX - minX,
			Height: maxY - minY,
		},
	}
}

func screenCaptureRectArg(frame axuiautomation.Rect, padding float64) string {
	rect := captureRect(frame, padding)
	return fmt.Sprintf("%.0f,%.0f,%.0f,%.0f", rect.Origin.X, rect.Origin.Y, rect.Size.Width, rect.Size.Height)
}
