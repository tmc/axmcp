// Package sdef parses macOS scripting definition (sdef) XML and generates
// AppleScript runners from the parsed definitions.
package sdef

import (
	"encoding/xml"
	"fmt"
	"os/exec"
	"strings"
)

// Dictionary is the top-level sdef document.
type Dictionary struct {
	Suites []Suite `xml:"suite"`
}

// Suite groups related commands and classes.
type Suite struct {
	Name        string    `xml:"name,attr"`
	Code        string    `xml:"code,attr"`
	Description string    `xml:"description,attr"`
	Commands    []Command `xml:"command"`
	Classes     []Class   `xml:"class"`
}

// Command is a scriptable command.
type Command struct {
	Name            string      `xml:"name,attr"`
	Code            string      `xml:"code,attr"`
	Description     string      `xml:"description,attr"`
	Hidden          string      `xml:"hidden,attr"`
	DirectParameter *Parameter  `xml:"direct-parameter"`
	Parameters      []Parameter `xml:"parameter"`
	Result          *Result     `xml:"result"`
}

// Parameter is a named command parameter.
type Parameter struct {
	Name        string `xml:"name,attr"`
	Code        string `xml:"code,attr"`
	Type        string `xml:"type,attr"`
	Optional    string `xml:"optional,attr"`
	Description string `xml:"description,attr"`
}

// Result describes the return type of a command.
type Result struct {
	Type        string `xml:"type,attr"`
	Description string `xml:"description,attr"`
}

// Class is a scriptable object class.
type Class struct {
	Name        string     `xml:"name,attr"`
	Code        string     `xml:"code,attr"`
	Description string     `xml:"description,attr"`
	Inherits    string     `xml:"inherits,attr"`
	Properties  []Property `xml:"property"`
	Elements    []Element  `xml:"element"`
}

// Property is a readable/writable attribute of a class.
type Property struct {
	Name        string `xml:"name,attr"`
	Code        string `xml:"code,attr"`
	Type        string `xml:"type,attr"`
	Access      string `xml:"access,attr"`
	Description string `xml:"description,attr"`
}

// Element is a child-element collection of a class.
type Element struct {
	Type   string `xml:"type,attr"`
	Access string `xml:"access,attr"`
}

// Parse runs sdef on appPath and returns the parsed Dictionary.
func Parse(appPath string) (*Dictionary, error) {
	out, err := exec.Command("sdef", appPath).Output()
	if err != nil {
		return nil, fmt.Errorf("sdef %s: %w", appPath, err)
	}
	var d Dictionary
	if err := xml.Unmarshal(out, &d); err != nil {
		return nil, fmt.Errorf("parse sdef: %w", err)
	}
	return &d, nil
}

// AppName returns a short display name for an app path (e.g. "Xcode" from "/Applications/Xcode.app").
func AppName(appPath string) string {
	base := appPath
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, ".app")
	return base
}

// Commands returns all non-hidden commands across all suites.
func (d *Dictionary) Commands() []Command {
	var out []Command
	for _, s := range d.Suites {
		for _, c := range s.Commands {
			if c.Hidden == "yes" {
				continue
			}
			out = append(out, c)
		}
	}
	return out
}

// Classes returns all classes across all suites.
func (d *Dictionary) Classes() []Class {
	var out []Class
	for _, s := range d.Suites {
		out = append(out, s.Classes...)
	}
	return out
}

// ToolName returns a safe MCP tool name for an app+command pair.
// e.g. appName="Xcode", cmdName="build" → "xcode_build"
func ToolName(appName, cmdName string) string {
	app := strings.ToLower(appName)
	app = strings.ReplaceAll(app, " ", "_")
	cmd := strings.ToLower(cmdName)
	cmd = strings.ReplaceAll(cmd, " ", "_")
	return app + "_" + cmd
}

// RunScript executes an AppleScript string via osascript and returns stdout.
func RunScript(script string) (string, error) {
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	result := strings.TrimSpace(string(out))
	if err != nil {
		if result != "" {
			return "", fmt.Errorf("%s", result)
		}
		return "", err
	}
	return result, nil
}

// BuildScript constructs an AppleScript tell-block for a command invocation.
// appName is the AppleScript application name (e.g. "Xcode").
// cmdName is the AppleScript command (e.g. "build").
// params is a map of parameter-name → value string.
// directParam is the direct parameter value (empty string = omit).
func BuildScript(appName, cmdName, directParam string, params map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "tell application %q\n", appName)
	fmt.Fprintf(&b, "\t%s", cmdName)
	if directParam != "" {
		fmt.Fprintf(&b, " %s", directParam)
	}
	for k, v := range params {
		fmt.Fprintf(&b, " %s %s", k, v)
	}
	fmt.Fprintf(&b, "\nend tell")
	return b.String()
}

// GetPropertyScript returns an AppleScript to get a property of the application.
func GetPropertyScript(appName, objectExpr, propName string) string {
	if objectExpr == "" {
		return fmt.Sprintf("tell application %q\n\tget %s\nend tell", appName, propName)
	}
	return fmt.Sprintf("tell application %q\n\ttell %s\n\t\tget %s\n\tend tell\nend tell", appName, objectExpr, propName)
}
