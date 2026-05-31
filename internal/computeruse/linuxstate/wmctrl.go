package linuxstate

import (
	"fmt"
	"strconv"
	"strings"
)

func parseWMCTRL(out []byte) ([]Window, error) {
	var windows []Window
	for lineNo, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		win, err := parseWMCTRLLine(line)
		if err != nil {
			return nil, fmt.Errorf("parse wmctrl line %d: %w", lineNo+1, err)
		}
		windows = append(windows, win)
	}
	return windows, nil
}

func parseWMCTRLLine(line string) (Window, error) {
	fields := strings.Fields(line)
	if len(fields) < 8 {
		return Window{}, fmt.Errorf("expected at least 8 fields")
	}
	pid, err := strconv.Atoi(fields[2])
	if err != nil {
		return Window{}, fmt.Errorf("parse pid %q: %w", fields[2], err)
	}
	x, err := strconv.Atoi(fields[3])
	if err != nil {
		return Window{}, fmt.Errorf("parse x %q: %w", fields[3], err)
	}
	y, err := strconv.Atoi(fields[4])
	if err != nil {
		return Window{}, fmt.Errorf("parse y %q: %w", fields[4], err)
	}
	width, err := strconv.Atoi(fields[5])
	if err != nil {
		return Window{}, fmt.Errorf("parse width %q: %w", fields[5], err)
	}
	height, err := strconv.Atoi(fields[6])
	if err != nil {
		return Window{}, fmt.Errorf("parse height %q: %w", fields[6], err)
	}
	title := ""
	if len(fields) > 8 {
		title = strings.Join(fields[8:], " ")
	}
	return Window{
		ID:          fields[0],
		Desktop:     fields[1],
		PID:         pid,
		Title:       title,
		ProcessName: processName(pid),
		X:           x,
		Y:           y,
		Width:       width,
		Height:      height,
	}, nil
}
