//go:build darwin

package main

import (
	"embed"
	"strings"
)

//go:embed skills/*.md
var skillsFS embed.FS

// readSkill reads a skill file from the embedded skills directory.
func readSkill(name string) (string, error) {
	data, err := skillsFS.ReadFile("skills/" + name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// mustReadSkill reads a skill file or returns an empty string on error.
func mustReadSkill(name string) string {
	s, _ := readSkill(name)
	return s
}
