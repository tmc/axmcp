package project

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "App.xcodeproj")
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}

	p, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if p.Path != path || p.Name != "App" || p.Type != TypeProject {
		t.Fatalf("Open = %+v, want path %q name App type project", p, path)
	}
}

func TestOpenRejectsUnsupportedType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "App")
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(path); err == nil {
		t.Fatal("Open returned nil error for unsupported directory")
	}
}

func TestDiscoverSkipsBundleContentsAndIgnoredDirs(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		"App.xcodeproj",
		"App.xcodeproj/project.xcworkspace",
		"App.xcworkspace",
		"Pods/Pod.xcodeproj",
		".hidden/Hidden.xcodeproj",
		"Sources/Package.xcodeproj",
	} {
		if err := os.MkdirAll(filepath.Join(root, path), 0755); err != nil {
			t.Fatal(err)
		}
	}

	projects, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	want := []Project{
		{Path: filepath.Join(root, "App.xcodeproj"), Name: "App", Type: TypeProject},
		{Path: filepath.Join(root, "App.xcworkspace"), Name: "App", Type: TypeWorkspace},
		{Path: filepath.Join(root, "Sources", "Package.xcodeproj"), Name: "Package", Type: TypeProject},
	}
	if !reflect.DeepEqual(projects, want) {
		t.Fatalf("Discover = %#v, want %#v", projects, want)
	}
}

func TestParseSchemes(t *testing.T) {
	output := `
Information about project "TestProject":
    Targets:
        TestProject
        TestProjectTests

    Build Configurations:
        Debug
        Release

    Schemes:
        TestScheme
        AnotherScheme
`
	want := []string{"TestScheme", "AnotherScheme"}
	got := parseSchemes(output)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseSchemes() = %v, want %v", got, want)
	}
}

func TestParseSchemesEmpty(t *testing.T) {
	output := `
Information about project "Empty":
    Schemes:
`
	var want []string
	got := parseSchemes(output)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseSchemes() = %v, want %v", got, want)
	}
}
