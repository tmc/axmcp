package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Type int

const (
	TypeProject Type = iota
	TypeWorkspace
)

func (t Type) String() string {
	switch t {
	case TypeProject:
		return "project"
	case TypeWorkspace:
		return "workspace"
	default:
		return "unknown"
	}
}

type Project struct {
	Path    string
	Name    string
	Type    Type
	Schemes []string
}

// Open parses a project or workspace at the given path.
func Open(path string) (*Project, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("path %s is not a directory", path)
	}

	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	p := &Project{
		Path: path,
		Name: name,
	}

	ext := filepath.Ext(path)
	switch ext {
	case ".xcodeproj":
		p.Type = TypeProject
	case ".xcworkspace":
		p.Type = TypeWorkspace
	default:
		return nil, fmt.Errorf("unsupported project type: %s", ext)
	}

	return p, nil
}

// Discover finds all .xcodeproj and .xcworkspace in a directory tree.
// It skips hidden directories, common dependency/build directories, and bundle
// contents.
func Discover(root string) ([]Project, error) {
	var projects []Project

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}

		name := d.Name()
		if shouldSkipDir(name) {
			return filepath.SkipDir
		}

		switch filepath.Ext(name) {
		case ".xcodeproj":
			projects = append(projects, Project{
				Path: path,
				Name: strings.TrimSuffix(name, ".xcodeproj"),
				Type: TypeProject,
			})
			return filepath.SkipDir
		case ".xcworkspace":
			if filepath.Ext(filepath.Dir(path)) == ".xcodeproj" {
				return filepath.SkipDir
			}
			projects = append(projects, Project{
				Path: path,
				Name: strings.TrimSuffix(name, ".xcworkspace"),
				Type: TypeWorkspace,
			})
			return filepath.SkipDir
		}
		return nil
	})

	return projects, err
}

func shouldSkipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "derived_data", "build", "Pods", "Carthage":
		return true
	default:
		return false
	}
}
