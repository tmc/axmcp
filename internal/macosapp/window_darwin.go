//go:build darwin

package macosapp

import (
	"strings"
	"time"

	"github.com/tmc/apple/x/axuiautomation"
)

func findReadyWindow(app RunningApp, opts WaitOptions) (WindowInfo, bool) {
	axuiautomation.SpinRunLoop(100 * time.Millisecond)
	axApp := axuiautomation.NewApplicationFromPID(int32(app.PID))
	if axApp == nil {
		return WindowInfo{}, false
	}
	defer axApp.Close()

	var windows []WindowInfo
	for _, win := range axApp.WindowList() {
		if win == nil {
			continue
		}
		x, y := win.Position()
		width, height := win.Size()
		windows = append(windows, WindowInfo{
			Title:      strings.TrimSpace(win.Title()),
			X:          x,
			Y:          y,
			Width:      width,
			Height:     height,
			ChildCount: len(win.Children()),
		})
	}
	return ChooseReadyWindow(windows, opts)
}
