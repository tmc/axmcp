package instruction

import (
	"strings"

	"github.com/tmc/axmcp/internal/computeruse"
)

// Provider returns built-in app guidance derived from reverse-engineering.
type Provider struct {
	byBundleID map[string]string
	byName     map[string]string
}

var _ computeruse.InstructionProvider = (*Provider)(nil)

// New returns a provider with the built-in instruction catalog.
func New() *Provider {
	return NewProvider()
}

// NewProvider returns a provider with the built-in instruction catalog.
func NewProvider() *Provider {
	browser := strings.TrimSpace(`
Use the page before the chrome around it.

- Prefer the active page content over toolbar icons when both can do the job.
- Re-read the page after navigation, tab changes, or dialogs before acting again.
- If the page scrolls, use scroll rather than drag gestures to move through content.
`)

	music := strings.TrimSpace(`
Work from the sidebar and search field first.

- Use the library/sidebar to anchor navigation before drilling into albums or playlists.
- After changing views, wait for the main content list to refresh before selecting an item.
- Trial or upgrade prompts can obscure the home view; if that happens, navigate with the sidebar or search instead of retrying the same card.
`)

	clock := strings.TrimSpace(`
Prefer the app's dedicated timer and alarm controls.

- Use the visible tabs to switch between alarms, timers, stopwatch, and world clock.
- Enter timer values in the app's expected fields rather than typing free-form prose.
- Reject timer durations longer than 23:59:59 because the app does not accept them.
`)

	numbers := strings.TrimSpace(`
Be deliberate about selection versus editing.

- A single click selects a cell; enter edit mode only when you intend to change its contents.
- Recheck the active sheet and selection before typing because focus is easy to lose during navigation.
- Use the formula bar or confirmed edit mode for longer values instead of assuming plain typing will land in the right cell.
`)

	notion := strings.TrimSpace(`
Treat the page as a block editor, not a plain document.

- Cursor movement and editing happen inside blocks, so confirm the caret is in the intended block before typing.
- Slash commands, markdown-like shortcuts, and block handles can change the layout after each action; re-read the page when that happens.
- When a block action fails, prefer a smaller corrective step over repeating the same long input.
`)

	spotify := strings.TrimSpace(`
Playback changes can lag behind the visible UI.

- After play, pause, next, or previous actions, give the window a moment and verify the current track again.
- Search and queue changes can refresh asynchronously, so re-read the list before selecting another result.
- If a control appears stale, refresh state instead of repeating the click immediately.
`)

	iphoneMirroring := strings.TrimSpace(`
Treat the mirrored phone as a remote device inside a desktop window.

- Use keyboard shortcuts only when the mirrored device clearly supports them.
- Prefer scroll for vertical movement inside the phone view; do not use drag as a substitute for scrolling.
- Re-read the mirrored screen after each action because transitions and animations can hide the next target briefly.
`)

	return &Provider{
		byBundleID: map[string]string{
			"com.apple.music":                     music,
			"com.apple.clock":                     clock,
			"notion.id":                           notion,
			"com.apple.iwork.numbers":             numbers,
			"com.spotify.client":                  spotify,
			"com.apple.iphonemirroring":           iphoneMirroring,
			"com.apple.safari":                    browser,
			"com.google.chrome":                   browser,
			"com.google.chrome.canary":            browser,
			"com.microsoft.edgemac":               browser,
			"company.thebrowser.browser":          browser,
			"org.mozilla.firefox":                 browser,
			"org.mozilla.firefoxdeveloperedition": browser,
			"com.brave.browser":                   browser,
			"com.operasoftware.opera":             browser,
			"com.vivaldi.vivaldi":                 browser,
		},
		byName: map[string]string{
			"music":            music,
			"clock":            clock,
			"notion":           notion,
			"numbers":          numbers,
			"spotify":          spotify,
			"iphone mirroring": iphoneMirroring,
			"safari":           browser,
			"google chrome":    browser,
			"chrome":           browser,
			"brave browser":    browser,
			"brave":            browser,
			"arc":              browser,
			"firefox":          browser,
			"microsoft edge":   browser,
			"edge":             browser,
			"opera":            browser,
			"vivaldi":          browser,
			"browser":          browser,
		},
	}
}

// Instructions reports built-in guidance for app.
func (p *Provider) Instructions(app computeruse.AppInfo) string {
	if p == nil {
		return ""
	}

	if text, ok := p.byBundleID[normalize(app.BundleID)]; ok {
		return text
	}

	name := normalize(app.Name)
	if text, ok := p.byName[name]; ok {
		return text
	}
	if looksLikeBrowser(app) {
		if text, ok := p.byName["browser"]; ok {
			return text
		}
	}
	return ""
}

func looksLikeBrowser(app computeruse.AppInfo) bool {
	name := normalize(app.Name)
	for _, token := range []string{"browser", "chrome", "safari", "firefox", "edge", "brave", "arc", "opera", "vivaldi"} {
		if strings.Contains(name, token) {
			return true
		}
	}

	bundleID := normalize(app.BundleID)
	for _, token := range []string{"browser", "chrome", "safari", "firefox", "edge", "brave", "arc", "opera", "vivaldi"} {
		if strings.Contains(bundleID, token) {
			return true
		}
	}
	return false
}

func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
}
