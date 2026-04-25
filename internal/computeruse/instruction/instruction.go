package instruction

import (
	"strings"

	"github.com/tmc/axmcp/internal/computeruse"
)

const (
	browserGuidance = `Use the page before the chrome around it.

- Prefer the active page content over toolbar icons when both can do the job.
- Re-read the page after navigation, tab changes, or dialogs before acting again.
- If the page scrolls, use scroll rather than drag gestures to move through content.`

	clockGuidance = `Prefer the app's dedicated timer and alarm controls.

- Use the visible tabs to switch between alarms, timers, stopwatch, and world clock.
- Enter timer values in the app's expected fields rather than typing free-form prose.
- Reject timer durations longer than 23:59:59 because the app does not accept them.`

	iphoneMirroringGuidance = `Treat the mirrored phone as a remote device inside a desktop window.

- Use keyboard shortcuts only when the mirrored device clearly supports them.
- Prefer scroll for vertical movement inside the phone view; do not use drag as a substitute for scrolling.
- Re-read the mirrored screen after each action because transitions and animations can hide the next target briefly.`

	musicGuidance = `Work from the sidebar and search field first.

- Use the library/sidebar to anchor navigation before drilling into albums or playlists.
- After changing views, wait for the main content list to refresh before selecting an item.
- Trial or upgrade prompts can obscure the home view; if that happens, navigate with the sidebar or search instead of retrying the same card.`

	notionGuidance = `Treat the page as a block editor, not a plain document.

- Cursor movement and editing happen inside blocks, so confirm the caret is in the intended block before typing.
- Slash commands, markdown-like shortcuts, and block handles can change the layout after each action; re-read the page when that happens.
- When a block action fails, prefer a smaller corrective step over repeating the same long input.`

	numbersGuidance = `Be deliberate about selection versus editing.

- A single click selects a cell; enter edit mode only when you intend to change its contents.
- Recheck the active sheet and selection before typing because focus is easy to lose during navigation.
- Use the formula bar or confirmed edit mode for longer values instead of assuming plain typing will land in the right cell.`

	spotifyGuidance = `Playback changes can lag behind the visible UI.

- After play, pause, next, or previous actions, give the window a moment and verify the current track again.
- Search and queue changes can refresh asynchronously, so re-read the list before selecting another result.
- If a control appears stale, refresh state instead of repeating the click immediately.`
)

var byBundleID = map[string]string{
	"com.apple.music":                     musicGuidance,
	"com.apple.clock":                     clockGuidance,
	"notion.id":                           notionGuidance,
	"com.apple.iwork.numbers":             numbersGuidance,
	"com.spotify.client":                  spotifyGuidance,
	"com.apple.iphonemirroring":           iphoneMirroringGuidance,
	"com.apple.safari":                    browserGuidance,
	"com.google.chrome":                   browserGuidance,
	"com.google.chrome.canary":            browserGuidance,
	"com.microsoft.edgemac":               browserGuidance,
	"company.thebrowser.browser":          browserGuidance,
	"org.mozilla.firefox":                 browserGuidance,
	"org.mozilla.firefoxdeveloperedition": browserGuidance,
	"com.brave.browser":                   browserGuidance,
	"com.operasoftware.opera":             browserGuidance,
	"com.vivaldi.vivaldi":                 browserGuidance,
}

var byName = map[string]string{
	"music":            musicGuidance,
	"clock":            clockGuidance,
	"notion":           notionGuidance,
	"numbers":          numbersGuidance,
	"spotify":          spotifyGuidance,
	"iphone mirroring": iphoneMirroringGuidance,
	"safari":           browserGuidance,
	"google chrome":    browserGuidance,
	"chrome":           browserGuidance,
	"brave browser":    browserGuidance,
	"brave":            browserGuidance,
	"arc":              browserGuidance,
	"firefox":          browserGuidance,
	"microsoft edge":   browserGuidance,
	"edge":             browserGuidance,
	"opera":            browserGuidance,
	"vivaldi":          browserGuidance,
	"browser":          browserGuidance,
}

var browserTokens = []string{
	"browser",
	"chrome",
	"safari",
	"firefox",
	"edge",
	"brave",
	"arc",
	"opera",
	"vivaldi",
}

// Provider returns built-in app guidance derived from reverse-engineering.
type Provider struct{}

var _ computeruse.InstructionProvider = (*Provider)(nil)

// New returns a provider with the built-in instruction catalog.
func New() *Provider {
	return &Provider{}
}

// Instructions reports built-in guidance for app.
func (p *Provider) Instructions(app computeruse.AppInfo) string {
	if p == nil {
		return ""
	}
	if text, ok := byBundleID[normalize(app.BundleID)]; ok {
		return text
	}
	if text, ok := byName[normalize(app.Name)]; ok {
		return text
	}
	if looksLikeBrowser(app) {
		return browserGuidance
	}
	return ""
}

func looksLikeBrowser(app computeruse.AppInfo) bool {
	name := normalize(app.Name)
	bundleID := normalize(app.BundleID)
	for _, token := range browserTokens {
		if strings.Contains(name, token) || strings.Contains(bundleID, token) {
			return true
		}
	}
	return false
}

func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
}
