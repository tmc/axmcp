package ui

import (
	"fmt"
	"strings"
	"sync"
)

type PermissionStatus string

const (
	PermissionStatusGranted    PermissionStatus = "granted"
	PermissionStatusMissing    PermissionStatus = "missing"
	PermissionStatusInProgress PermissionStatus = "in_progress"
)

type PermissionSnapshot struct {
	Accessibility   PermissionStatus
	ScreenRecording PermissionStatus
	Pending         bool
	Message         string
}

var permissionProgress struct {
	sync.RWMutex
	accessibility bool
	screenCapture bool
}

func setPermissionInProgress(service string, inProgress bool) {
	permissionProgress.Lock()
	defer permissionProgress.Unlock()
	switch normalizePermissionService(service) {
	case "Accessibility":
		permissionProgress.accessibility = inProgress
	case "ScreenCapture":
		permissionProgress.screenCapture = inProgress
	}
}

func permissionInProgress(service string) bool {
	permissionProgress.RLock()
	defer permissionProgress.RUnlock()
	switch normalizePermissionService(service) {
	case "Accessibility":
		return permissionProgress.accessibility
	case "ScreenCapture":
		return permissionProgress.screenCapture
	default:
		return false
	}
}

func normalizePermissionService(service string) string {
	switch strings.TrimSpace(service) {
	case "Accessibility":
		return "Accessibility"
	case "Screen Recording", "ScreenCapture":
		return "ScreenCapture"
	default:
		return strings.TrimSpace(service)
	}
}

func CurrentPermissionSnapshot() PermissionSnapshot {
	snapshot := PermissionSnapshot{
		Accessibility:   permissionStatus("Accessibility", IsTrusted()),
		ScreenRecording: permissionStatus("ScreenCapture", IsScreenRecordingTrusted()),
	}
	if snapshot.Accessibility == PermissionStatusGranted && snapshot.ScreenRecording == PermissionStatusGranted {
		return snapshot
	}

	snapshot.Pending = true
	var waiting []string
	var missing []string
	switch snapshot.Accessibility {
	case PermissionStatusInProgress:
		waiting = append(waiting, "Accessibility")
	case PermissionStatusMissing:
		missing = append(missing, "Accessibility")
	}
	switch snapshot.ScreenRecording {
	case PermissionStatusInProgress:
		waiting = append(waiting, "Screen Recording")
	case PermissionStatusMissing:
		missing = append(missing, "Screen Recording")
	}

	switch {
	case len(waiting) > 0 && len(missing) > 0:
		snapshot.Message = fmt.Sprintf(
			"permission onboarding in progress: waiting for %s; still need %s",
			strings.Join(waiting, " and "),
			strings.Join(missing, " and "),
		)
	case len(waiting) > 0:
		snapshot.Message = fmt.Sprintf("permission onboarding in progress: waiting for %s", strings.Join(waiting, " and "))
	default:
		snapshot.Message = fmt.Sprintf("permissions pending: grant %s and call get_app_state again", strings.Join(missing, " and "))
	}
	return snapshot
}

func permissionStatus(service string, granted bool) PermissionStatus {
	if granted {
		return PermissionStatusGranted
	}
	if permissionInProgress(service) {
		return PermissionStatusInProgress
	}
	return PermissionStatusMissing
}
