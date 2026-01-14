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

	// Schemes are loaded lazily or explicitly via Schemes() method to avoid slow startup?
	// The design doc signature implies Schemes() is a method on Project.
	// But Open is expected to return a *Project.
	// We'll populate schemes if requested, but for Open we might want to keep it lightweight
	// or populate it if it's cheap.
	// For now, let's stick to the struct in the design doc which has Schemes []string.
	// But the Interface says `func (p *Project) Schemes() ([]string, error)`.
	// If Schemes is a field, we might populate it eagerly.
    // The design doc struct has `Schemes []string`.
    // The design doc method has `func (p *Project) Schemes() ([]string, error)`.
    // This is slightly ambiguous. I will make `Schemes()` a method that populates the field if empty/nil, 
    // or just fetches them.
    // Given the `mcp Tool` example: 
    // `projects, err := project.Discover(args.Path)`
    // `out.Projects[i] = ProjectInfo{..., Schemes: p.Schemes}`
    // It suggests `Discover` (and likely Open) should populate `Schemes`.
    // However, `xcodebuild -list` is slow.
    // I'll implement `Schemes()` method to fetch them, and `Open` won't populate them by default unless we change the design.
    // Wait, the MCP tool example uses `p.Schemes` (field access).
    // Raising `xcodebuild -list` for every project in a directory could be very slow.
    // I will stick to the method `Schemes()` for fetching, but the struct has the field. 
    // I will leave the field empty in Open/Discover for performance, and let consumers call Schemes() if they need it.
    // Or I'll populate it if it's a single `Open`. `Discover` might want to avoid it.
    // Let's implement the method `Schemes()` in `schemes.go`.

	return p, nil
}

// Discover finds all .xcodeproj and .xcworkspace in a directory tree.
// It does NOT recurse deep into subdirectories to avoid finding embedded projects (e.g. in Pods),
// unless we want to. For a "root" style discover, usually we look at top level or 1-2 levels deep.
// The design doc says "Discover finds all ... in a directory tree". 
// I'll implement a simple Walk but skip typical ignored dirs.
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
		// Skip hidden dirs and common build dirs
		if strings.HasPrefix(name, ".") || name == "derived_data" || name == "build" || name == "Pods" || name == "Carthage" {
			return filepath.SkipDir
		}

		if strings.HasSuffix(name, ".xcodeproj") {
			projects = append(projects, Project{
				Path: path,
				Name: strings.TrimSuffix(name, ".xcodeproj"),
				Type: TypeProject,
			})
			return filepath.SkipDir // Don't look inside xcodeproj
		}
		if strings.HasSuffix(name, ".xcworkspace") {
             // For workspaces, we might want to check if it's the internal project.xcworkspace
             // which is inside .xcodeproj. We should usually skip those as "standalone" workspaces
             // if we already found the project.
             // But valid workspaces also exist.
             // Heuristic: if parent is .xcodeproj, skip.
             dir := filepath.Dir(path)
             if strings.HasSuffix(dir, ".xcodeproj") {
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
