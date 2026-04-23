package instruction

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

const (
	browserBundle         = "browser"
	clockBundle           = "clock"
	iphoneMirroringBundle = "iphonemirroring"
	musicBundle           = "music"
	notionBundle          = "notion"
	numbersBundle         = "numbers"
	spotifyBundle         = "spotify"
)

type catalog struct {
	byBundleID map[string]string
	byName     map[string]string
}

type bundleSpec struct {
	id   string
	path string
}

//go:embed bundles/*.md
var bundleFS embed.FS

var bundleSpecs = []bundleSpec{
	{id: browserBundle, path: "bundles/browser.md"},
	{id: clockBundle, path: "bundles/clock.md"},
	{id: iphoneMirroringBundle, path: "bundles/iphonemirroring.md"},
	{id: musicBundle, path: "bundles/music.md"},
	{id: notionBundle, path: "bundles/notion.md"},
	{id: numbersBundle, path: "bundles/numbers.md"},
	{id: spotifyBundle, path: "bundles/spotify.md"},
}

var bundleIDs = map[string]string{
	"com.apple.music":                     musicBundle,
	"com.apple.clock":                     clockBundle,
	"notion.id":                           notionBundle,
	"com.apple.iwork.numbers":             numbersBundle,
	"com.spotify.client":                  spotifyBundle,
	"com.apple.iphonemirroring":           iphoneMirroringBundle,
	"com.apple.safari":                    browserBundle,
	"com.google.chrome":                   browserBundle,
	"com.google.chrome.canary":            browserBundle,
	"com.microsoft.edgemac":               browserBundle,
	"company.thebrowser.browser":          browserBundle,
	"org.mozilla.firefox":                 browserBundle,
	"org.mozilla.firefoxdeveloperedition": browserBundle,
	"com.brave.browser":                   browserBundle,
	"com.operasoftware.opera":             browserBundle,
	"com.vivaldi.vivaldi":                 browserBundle,
}

var appNames = map[string]string{
	"music":            musicBundle,
	"clock":            clockBundle,
	"notion":           notionBundle,
	"numbers":          numbersBundle,
	"spotify":          spotifyBundle,
	"iphone mirroring": iphoneMirroringBundle,
	"safari":           browserBundle,
	"google chrome":    browserBundle,
	"chrome":           browserBundle,
	"brave browser":    browserBundle,
	"brave":            browserBundle,
	"arc":              browserBundle,
	"firefox":          browserBundle,
	"microsoft edge":   browserBundle,
	"edge":             browserBundle,
	"opera":            browserBundle,
	"vivaldi":          browserBundle,
	"browser":          browserBundle,
}

func loadCatalog(fsys fs.FS) (*catalog, error) {
	bundles, err := loadBundles(fsys)
	if err != nil {
		return nil, err
	}

	byBundleID, err := buildLookup(bundleIDs, bundles)
	if err != nil {
		return nil, err
	}
	byName, err := buildLookup(appNames, bundles)
	if err != nil {
		return nil, err
	}

	return &catalog{
		byBundleID: byBundleID,
		byName:     byName,
	}, nil
}

func loadBundles(fsys fs.FS) (map[string]string, error) {
	bundles := make(map[string]string, len(bundleSpecs))
	for _, spec := range bundleSpecs {
		text, err := loadBundle(fsys, spec.path)
		if err != nil {
			return nil, err
		}
		bundles[spec.id] = text
	}
	return bundles, nil
}

func loadBundle(fsys fs.FS, path string) (string, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	text := strings.TrimSpace(string(data))
	if text == "" {
		return "", fmt.Errorf("read %s: empty instruction bundle", path)
	}
	return text, nil
}

func buildLookup(index map[string]string, bundles map[string]string) (map[string]string, error) {
	lookup := make(map[string]string, len(index))
	for key, bundleID := range index {
		text, ok := bundles[bundleID]
		if !ok {
			return nil, fmt.Errorf("missing instruction bundle %q", bundleID)
		}
		lookup[normalize(key)] = text
	}
	return lookup, nil
}

func cloneLookup(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}

	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
