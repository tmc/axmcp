package instruction

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestLoadCatalog(t *testing.T) {
	catalog, err := loadCatalog(testCatalogFS())
	if err != nil {
		t.Fatalf("loadCatalog() error = %v", err)
	}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "bundle id",
			got:  catalog.byBundleID[normalize("com.apple.music")],
			want: "music instructions",
		},
		{
			name: "bundle id browser",
			got:  catalog.byBundleID[normalize("org.mozilla.firefox")],
			want: "browser instructions",
		},
		{
			name: "name",
			got:  catalog.byName[normalize("Spotify")],
			want: "spotify instructions",
		},
		{
			name: "name browser",
			got:  catalog.byName[normalize("Arc")],
			want: "browser instructions",
		},
	}

	for _, tt := range tests {
		if tt.got != tt.want {
			t.Fatalf("%s lookup = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

func TestLoadCatalogMissingBundleFile(t *testing.T) {
	fsys := testCatalogFS()
	delete(fsys, "bundles/music.md")

	_, err := loadCatalog(fsys)
	if err == nil {
		t.Fatal("loadCatalog() error = nil, want missing file")
	}
	if !strings.Contains(err.Error(), "bundles/music.md") {
		t.Fatalf("loadCatalog() error = %q, want missing file path", err)
	}
}

func TestBuildLookupMissingBundle(t *testing.T) {
	_, err := buildLookup(map[string]string{"music": musicBundle}, map[string]string{})
	if err == nil {
		t.Fatal("buildLookup() error = nil, want missing bundle")
	}
	if !strings.Contains(err.Error(), musicBundle) {
		t.Fatalf("buildLookup() error = %q, want bundle id", err)
	}
}

func testCatalogFS() fstest.MapFS {
	return fstest.MapFS{
		"bundles/browser.md":         {Data: []byte("browser instructions")},
		"bundles/clock.md":           {Data: []byte("clock instructions")},
		"bundles/iphonemirroring.md": {Data: []byte("iphone instructions")},
		"bundles/music.md":           {Data: []byte("music instructions")},
		"bundles/notion.md":          {Data: []byte("notion instructions")},
		"bundles/numbers.md":         {Data: []byte("numbers instructions")},
		"bundles/spotify.md":         {Data: []byte("spotify instructions")},
	}
}
