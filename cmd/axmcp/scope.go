package main

import (
	"fmt"

	"github.com/tmc/apple/x/axuiautomation"
)

func resolveSearchRoot(app *axuiautomation.Application, window string) (*axuiautomation.Element, string, error) {
	if app == nil {
		return nil, "", fmt.Errorf("no app in context")
	}
	if window == "" {
		return app.Root(), fmt.Sprintf("app %q", app.BundleID()), nil
	}
	win, desc, err := resolveWindow(app, window)
	if err != nil {
		return nil, "", err
	}
	return win, desc, nil
}
