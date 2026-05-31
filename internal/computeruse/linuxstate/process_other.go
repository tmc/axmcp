//go:build !linux

package linuxstate

func processName(int) string {
	return ""
}
