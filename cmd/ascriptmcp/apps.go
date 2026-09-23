package main

import (
	"os"
	"path/filepath"
	"strings"
)

// appDirs returns the directories that hold user-visible applications, in
// search order. Apple's own apps, such as TextEdit, live under /System.
func appDirs() []string {
	dirs := []string{
		"/Applications",
		"/Applications/Utilities",
		"/System/Applications",
		"/System/Applications/Utilities",
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "Applications"))
	}
	return dirs
}

// coreServicesDir holds Finder and other system apps. It is searched when
// resolving a name but not listed: most of its hundreds of bundles are
// internal helpers.
const coreServicesDir = "/System/Library/CoreServices"

// listApps returns the paths of the .app bundles in dirs. Missing or
// unreadable directories are skipped.
func listApps(dirs []string) []string {
	var paths []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".app") {
				paths = append(paths, filepath.Join(dir, e.Name()))
			}
		}
	}
	return paths
}

// findApp returns the path of the application named name in dirs. It
// matches the bundle name without ".app", preferring an exact match to one
// that ignores case. The path uses the bundle's own spelling.
func findApp(dirs []string, name string) (string, bool) {
	apps := listApps(dirs)
	for _, path := range apps {
		if appBase(path) == name {
			return path, true
		}
	}
	for _, path := range apps {
		if strings.EqualFold(appBase(path), name) {
			return path, true
		}
	}
	return "", false
}

func appBase(path string) string {
	return strings.TrimSuffix(filepath.Base(path), ".app")
}

// resolveAppPath turns a short name like "Xcode" into the path of the
// installed application, such as "/Applications/Xcode.app". A path is
// returned unchanged. An unknown name resolves to where /Applications would
// hold it, so callers that only need the name still work.
func resolveAppPath(app string) string {
	if strings.Contains(app, "/") {
		return app
	}
	if path, ok := findApp(append(appDirs(), coreServicesDir), app); ok {
		return path
	}
	return filepath.Join("/Applications", app+".app")
}
