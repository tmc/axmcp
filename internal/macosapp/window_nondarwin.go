//go:build !darwin

package macosapp

func findReadyWindow(RunningApp, WaitOptions) (WindowInfo, bool) {
	return WindowInfo{}, false
}
