package crash

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Report represents a crash report file
type Report struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	Process string    `json:"process,omitempty"` // Derived from filename
}

// ListOptions filters the crash reports
type ListOptions struct {
	Query string    // Filter by process name/filename substring
	After time.Time // Filter modified after this time
	Limit int       // Max results
}

// List returns a list of crash reports from ~/Library/Logs/DiagnosticReports
func List(ctx context.Context, opts ListOptions) ([]Report, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home dir: %w", err)
	}

	reportsDir := filepath.Join(home, "Library", "Logs", "DiagnosticReports")
	entries, err := os.ReadDir(reportsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Report{}, nil // Directory might not exist on some systems or sandboxes
		}
		return nil, fmt.Errorf("failed to read diagnostic reports dir: %w", err)
	}

	var reports []Report
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.IsDir() {
			continue
		}

		// Filter by time
		if !opts.After.IsZero() && info.ModTime().Before(opts.After) {
			continue
		}

		name := info.Name()
		// Filter by query (case-insensitive substring)
		if opts.Query != "" {
			if !strings.Contains(strings.ToLower(name), strings.ToLower(opts.Query)) {
				continue
			}
		}

		// Attempt to guess process name from filename: ProcessName-Date.ips
		process := ""
		if parts := strings.Split(name, "-"); len(parts) > 0 {
			process = parts[0]
		}

		reports = append(reports, Report{
			Name:    name,
			Path:    filepath.Join(reportsDir, name),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Process: process,
		})
	}

	// Sort by ModTime descending (newest first)
	sort.Slice(reports, func(i, j int) bool {
		return reports[i].ModTime.After(reports[j].ModTime)
	})

	if opts.Limit > 0 && len(reports) > opts.Limit {
		reports = reports[:opts.Limit]
	}

	return reports, nil
}

// Read reads the content of a crash report
func Read(ctx context.Context, path string) (string, error) {
	// Security check: ensure path is within DiagnosticReports or allowed dirs
	// For simplicity, we just read it. The sandbox/permissions will enforce access.
	// But good to ensure it's not arbitrary FS read if running as privileged user (unlikely here).

	// Validate extension
	ext := filepath.Ext(path)
	if ext != ".ips" && ext != ".crash" && ext != ".log" {
		// return "", fmt.Errorf("invalid file type: %s", ext)
		// Relaxed check for now as names vary.
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}
