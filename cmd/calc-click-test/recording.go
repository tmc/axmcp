package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tmc/apple/avfoundation"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/apple/coremedia"
	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/screencapturekit"
	"github.com/tmc/apple/x/axuiautomation"
)

const recordingWarmup = 120 * time.Millisecond

type recorder struct {
	path              string
	stream            screencapturekit.SCStream
	screenOutput      screencapturekit.SCStreamOutputObject
	screenOutputQueue dispatch.Queue
	recording         screencapturekit.SCRecordingOutput
	streamDelegate    screencapturekit.SCStreamDelegateObject
	recordingDelegate screencapturekit.SCRecordingOutputDelegateObject
	started           chan struct{}
	finished          chan struct{}
	failed            chan error
}

type recordingTarget struct {
	filter          screencapturekit.SCContentFilter
	sourceRect      corefoundation.CGRect
	pointPixelScale float64
	widthPx         int
	heightPx        int
}

func newRecorder(cfg config, frame axuiautomation.Rect) (*recorder, error) {
	if !cfg.video {
		return nil, nil
	}
	debugf("newRecorder start frame=%+v", frame)
	if strings.TrimSpace(cfg.screenshotDir) == "" {
		return nil, fmt.Errorf("-video requires -screenshot-dir")
	}
	if cfg.videoFPS <= 0 {
		return nil, fmt.Errorf("-video-fps must be positive")
	}
	dir, err := resolveCaptureDir(cfg.screenshotDir)
	if err != nil {
		return nil, fmt.Errorf("resolve video dir: %w", err)
	}
	path := filepath.Join(dir, "run.mp4")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove stale %s: %w", path, err)
	}
	debugf("newRecorder path=%s", path)

	target, err := resolveRecordingTarget(captureRect(frame, cfg.screenshotPad))
	if err != nil {
		return nil, err
	}
	debugf("newRecorder target sourceRect=%+v scale=%.2f size=%dx%d", target.sourceRect, target.pointPixelScale, target.widthPx, target.heightPx)
	filter := target.filter

	streamCfg := screencapturekit.NewSCStreamConfiguration()
	debugf("newRecorder stream config allocated")
	streamCfg.SetWidth(uintptr(target.widthPx))
	streamCfg.SetHeight(uintptr(target.heightPx))
	streamCfg.SetSourceRect(target.sourceRect)
	streamCfg.SetCaptureResolution(screencapturekit.SCCaptureResolutionBest)
	streamCfg.SetScalesToFit(false)
	streamCfg.SetPreservesAspectRatio(true)
	streamCfg.SetShowsCursor(false)
	streamCfg.SetShowMouseClicks(false)
	streamCfg.SetCapturesAudio(false)
	streamCfg.SetCaptureMicrophone(false)
	streamCfg.SetQueueDepth(6)
	streamCfg.SetMinimumFrameInterval(coremedia.CMTimeMake(1, int32(cfg.videoFPS)))
	streamCfg.SetStreamName("calc-click-test")

	recCfg := screencapturekit.NewSCRecordingOutputConfiguration()
	debugf("newRecorder recording config allocated")
	recCfg.SetOutputURL(foundation.NewURLFileURLWithPath(path))
	fileType := choosePreferred(recCfg.AvailableOutputFileTypes(), avfoundation.AVFileTypes.MPEG4, avfoundation.AVFileTypes.QuickTimeMovie)
	if fileType == "" {
		return nil, fmt.Errorf("ScreenCaptureKit recording has no supported output file types")
	}
	recCfg.SetOutputFileType(foundation.NewStringWithString(fileType))
	if codec := choosePreferred(recCfg.AvailableVideoCodecTypes(), avfoundation.AVVideoCodecTypes.H264, avfoundation.AVVideoCodecTypes.HEVC); codec != "" {
		recCfg.SetVideoCodecType(foundation.NewStringWithString(codec))
	}

	r := &recorder{
		path:     path,
		started:  make(chan struct{}, 1),
		finished: make(chan struct{}, 1),
		failed:   make(chan error, 1),
	}
	r.streamDelegate = screencapturekit.NewSCStreamDelegate(screencapturekit.SCStreamDelegateConfig{
		StreamDidStopWithError: func(_ screencapturekit.SCStream, err foundation.NSError) {
			r.reportError(fmt.Errorf("ScreenCaptureKit stream stopped: %v", err))
		},
	})
	r.recordingDelegate = screencapturekit.NewSCRecordingOutputDelegate(screencapturekit.SCRecordingOutputDelegateConfig{
		RecordingOutputDidStartRecording: func(_ screencapturekit.SCRecordingOutput) {
			r.signal(r.started)
		},
		RecordingOutputDidFinishRecording: func(_ screencapturekit.SCRecordingOutput) {
			r.signal(r.finished)
		},
		RecordingOutputDidFailWithError: func(_ screencapturekit.SCRecordingOutput, err foundation.NSError) {
			r.reportError(fmt.Errorf("ScreenCaptureKit recording failed: %v", err))
		},
	})
	r.stream = screencapturekit.NewStreamWithFilterConfigurationDelegate(filter, streamCfg, r.streamDelegate)
	if r.stream.GetID() == 0 {
		return nil, fmt.Errorf("create ScreenCaptureKit stream")
	}
	debugf("newRecorder stream created")
	r.screenOutput, err = newNoopStreamOutput()
	if err != nil {
		return nil, fmt.Errorf("create ScreenCaptureKit screen output: %w", err)
	}
	debugf("newRecorder screen output created")
	r.screenOutputQueue = dispatch.QueueCreate("dev.tmc.calc-click-test.recording-output")
	if _, err := r.stream.AddStreamOutputTypeSampleHandlerQueueError(r.screenOutput, screencapturekit.SCStreamOutputTypeScreen, r.screenOutputQueue); err != nil {
		return nil, fmt.Errorf("attach ScreenCaptureKit screen output: %w", err)
	}
	debugf("newRecorder screen output attached")
	r.recording = screencapturekit.NewRecordingOutputWithConfigurationDelegate(recCfg, r.recordingDelegate)
	if r.recording.GetID() == 0 {
		return nil, fmt.Errorf("create ScreenCaptureKit recording output")
	}
	debugf("newRecorder recording output created")
	if _, err := r.stream.AddRecordingOutputError(r.recording); err != nil {
		return nil, fmt.Errorf("attach recording output: %w", err)
	}
	debugf("newRecorder recording output attached")
	return r, nil
}

func (r *recorder) Start() error {
	if r == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.stream.StartCapture(ctx); err != nil {
		return fmt.Errorf("start ScreenCaptureKit recording: %w", err)
	}
	select {
	case err := <-r.failed:
		return err
	case <-r.started:
	case <-time.After(1500 * time.Millisecond):
	}
	time.Sleep(recordingWarmup)
	return r.pendingError()
}

func (r *recorder) Stop() error {
	if r == nil {
		return nil
	}
	var stopErr error
	recordStopError := func(err error) {
		if err != nil && stopErr == nil {
			stopErr = err
		}
	}
	if _, err := r.stream.RemoveRecordingOutputError(r.recording); err != nil {
		recordStopError(fmt.Errorf("detach ScreenCaptureKit recording output: %w", err))
	} else {
		select {
		case err := <-r.failed:
			recordStopError(err)
		case <-r.finished:
		case <-time.After(4 * time.Second):
		}
		recordStopError(r.pendingError())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.stream.StopCapture(ctx); err != nil {
		recordStopError(fmt.Errorf("stop ScreenCaptureKit stream: %w", err))
	}
	if r.screenOutput.GetID() != 0 {
		if _, err := r.stream.RemoveStreamOutputTypeError(r.screenOutput, screencapturekit.SCStreamOutputTypeScreen); err != nil {
			recordStopError(fmt.Errorf("detach ScreenCaptureKit screen output: %w", err))
		}
	}
	recordStopError(r.pendingError())
	if stopErr != nil {
		return stopErr
	}
	info, err := waitForVideoFile(r.path, 4*time.Second)
	if err != nil {
		return err
	}
	fmt.Printf("saved %s (%d bytes)\n", r.path, info.Size())
	return nil
}

func (r *recorder) pendingError() error {
	select {
	case err := <-r.failed:
		return err
	default:
		return nil
	}
}

func (r *recorder) reportError(err error) {
	select {
	case r.failed <- err:
	default:
	}
}

func (r *recorder) signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func waitForVideoFile(path string, timeout time.Duration) (os.FileInfo, error) {
	deadline := time.Now().Add(timeout)
	for {
		info, err := os.Stat(path)
		if err == nil && info.Size() > 0 {
			return info, nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return nil, fmt.Errorf("stat %s: %w", path, err)
			}
			return nil, fmt.Errorf("recorded empty video %s", path)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func resolveRecordingTarget(frame axuiautomation.Rect) (recordingTarget, error) {
	debugf("resolveRecordingTarget frame=%+v", frame)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	content, err := screencapturekit.GetSCShareableContentClass().GetShareableContent(ctx)
	if err != nil {
		return recordingTarget{}, fmt.Errorf("get shareable content: %w", err)
	}
	displays := content.Displays()
	if len(displays) == 0 {
		return recordingTarget{}, fmt.Errorf("ScreenCaptureKit found no displays")
	}

	capture := toCGRect(frame)
	display, displayRect, ok := bestDisplayForRect(displays, capture)
	if !ok {
		return recordingTarget{}, fmt.Errorf("no display overlaps capture rect")
	}
	debugf("resolveRecordingTarget displayRect=%+v", displayRect)
	filter := screencapturekit.NewContentFilterWithDisplayExcludingWindows(display, nil)
	debugf("resolveRecordingTarget filter created")
	debugf("resolveRecordingTarget contentRect=%+v pointScale=%.2f", filter.ContentRect(), float64(filter.PointPixelScale()))
	capture = intersectRect(capture, displayRect)
	if capture.Size.Width <= 0 || capture.Size.Height <= 0 {
		return recordingTarget{}, fmt.Errorf("capture rect does not intersect display")
	}
	scale := float64(filter.PointPixelScale())
	if scale <= 0 {
		scaleX, scaleY := displayPointPixelScale(display, displayRect)
		scale = math.Max(scaleX, scaleY)
	}
	if scale <= 0 {
		scale = 2
	}
	local := localSourceRect(capture, displayRect)

	return recordingTarget{
		filter:          filter,
		sourceRect:      local,
		pointPixelScale: scale,
		widthPx:         recordingDimension(local.Size.Width, scale),
		heightPx:        recordingDimension(local.Size.Height, scale),
	}, nil
}

func bestDisplayForRect(displays []screencapturekit.SCDisplay, rect corefoundation.CGRect) (screencapturekit.SCDisplay, corefoundation.CGRect, bool) {
	var best screencapturekit.SCDisplay
	var bestRect corefoundation.CGRect
	bestArea := -1.0
	for _, display := range displays {
		displayRect := display.Frame()
		area := rectArea(intersectRect(rect, displayRect))
		if area <= bestArea {
			continue
		}
		best = display
		bestRect = displayRect
		bestArea = area
	}
	if bestArea <= 0 {
		return screencapturekit.SCDisplay{}, corefoundation.CGRect{}, false
	}
	return best, bestRect, true
}

func choosePreferred(available []string, preferred ...string) string {
	for _, want := range preferred {
		for _, have := range available {
			if have == want {
				return have
			}
		}
	}
	if len(available) == 0 {
		return ""
	}
	return available[0]
}

func recordingDimension(points float64, pointPixelScale float64) int {
	if pointPixelScale <= 0 {
		pointPixelScale = 1
	}
	return maxInt(2, int(math.Round(points*pointPixelScale)))
}

func pointPixelScale(pixels float64, points float64) float64 {
	if pixels <= 0 || points <= 0 {
		return 0
	}
	return pixels / points
}

func displayPointPixelScale(display screencapturekit.SCDisplay, displayRect corefoundation.CGRect) (float64, float64) {
	mode := coregraphics.CGDisplayCopyDisplayMode(display.DisplayID())
	if mode == 0 {
		return 0, 0
	}
	defer coregraphics.CGDisplayModeRelease(mode)
	return pointPixelScale(float64(coregraphics.CGDisplayModeGetPixelWidth(mode)), displayRect.Size.Width),
		pointPixelScale(float64(coregraphics.CGDisplayModeGetPixelHeight(mode)), displayRect.Size.Height)
}

func localSourceRect(capture, contentRect corefoundation.CGRect) corefoundation.CGRect {
	local := capture
	local.Origin.X -= contentRect.Origin.X
	local.Origin.Y -= contentRect.Origin.Y
	return local
}

func toCGRect(frame axuiautomation.Rect) corefoundation.CGRect {
	return corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: frame.Origin.X, Y: frame.Origin.Y},
		Size:   corefoundation.CGSize{Width: frame.Size.Width, Height: frame.Size.Height},
	}
}

func intersectRect(a, b corefoundation.CGRect) corefoundation.CGRect {
	minX := math.Max(a.Origin.X, b.Origin.X)
	minY := math.Max(a.Origin.Y, b.Origin.Y)
	maxX := math.Min(a.Origin.X+a.Size.Width, b.Origin.X+b.Size.Width)
	maxY := math.Min(a.Origin.Y+a.Size.Height, b.Origin.Y+b.Size.Height)
	if maxX <= minX || maxY <= minY {
		return corefoundation.CGRect{}
	}
	return corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: minX, Y: minY},
		Size:   corefoundation.CGSize{Width: maxX - minX, Height: maxY - minY},
	}
}

func rectArea(rect corefoundation.CGRect) float64 {
	if rect.Size.Width <= 0 || rect.Size.Height <= 0 {
		return 0
	}
	return rect.Size.Width * rect.Size.Height
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var streamOutputClassCounter atomic.Uint64

func newNoopStreamOutput() (screencapturekit.SCStreamOutputObject, error) {
	className := fmt.Sprintf("GoCalcClickTestStreamOutput_%d", streamOutputClassCounter.Add(1))
	var protocols []*objc.Protocol
	if proto := objc.GetProtocol("SCStreamOutput"); proto != nil {
		protocols = append(protocols, proto)
	}
	cls, err := objc.RegisterClass(
		className,
		objc.GetClass("NSObject"),
		protocols,
		nil,
		[]objc.MethodDef{{
			Cmd: objc.RegisterName("stream:didOutputSampleBuffer:ofType:"),
			Fn: func(self objc.ID, _cmd objc.SEL, streamID objc.ID, sampleBuffer uintptr, type_ screencapturekit.SCStreamOutputType) {
			},
		}},
	)
	if err != nil {
		return screencapturekit.SCStreamOutputObject{}, err
	}
	instance := objc.ID(cls).Send(objc.RegisterName("alloc")).Send(objc.RegisterName("init"))
	return screencapturekit.SCStreamOutputObjectFromID(instance), nil
}
