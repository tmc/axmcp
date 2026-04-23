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

// New returns a provider with the built-in instruction catalog.
func New() *Provider {
	return NewProvider()
}

// NewProvider returns a provider with the built-in instruction catalog.
func NewProvider() *Provider {
	catalog, err := loadCatalog(bundleFS)
	if err != nil {
		return &Provider{}
	}
	return newProvider(catalog)
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
	for _, token := range browserTokens {
		if strings.Contains(name, token) {
			return true
		}
	}

	bundleID := normalize(app.BundleID)
	for _, token := range browserTokens {
		if strings.Contains(bundleID, token) {
			return true
		}
	}
	return false
}

func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
}

func newProvider(catalog *catalog) *Provider {
	if catalog == nil {
		return &Provider{}
	}
	return &Provider{
		byBundleID: cloneLookup(catalog.byBundleID),
		byName:     cloneLookup(catalog.byName),
	}
}
