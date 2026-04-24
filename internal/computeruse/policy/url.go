// Package policy enforces computer-use safety policies.
package policy

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/tmc/axmcp/internal/computeruse"
)

// URLPolicy blocks actions on configured browser domains.
type URLPolicy struct {
	blocked []string
}

// NewURLPolicy returns a URL policy. Domains are matched exactly or by
// subdomain suffix.
func NewURLPolicy(blockedDomains []string) *URLPolicy {
	p := &URLPolicy{}
	for _, domain := range blockedDomains {
		domain = normalizeDomain(domain)
		if domain != "" {
			p.blocked = append(p.blocked, domain)
		}
	}
	return p
}

// CheckState returns an error if state appears to be a browser on a blocked URL.
func (p *URLPolicy) CheckState(state computeruse.AppState) error {
	if p == nil || len(p.blocked) == 0 || !isBrowserApp(state.App) {
		return nil
	}
	raw := ActiveURL(state)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	host := normalizeDomain(u.Hostname())
	if host == "" {
		return nil
	}
	for _, blocked := range p.blocked {
		if host == blocked || strings.HasSuffix(host, "."+blocked) {
			return fmt.Errorf("computer-use action blocked on %s by URL policy", host)
		}
	}
	return nil
}

// ActiveURL extracts a likely active browser URL from an app state.
func ActiveURL(state computeruse.AppState) string {
	for _, node := range state.Tree {
		for _, value := range []string{node.Value, node.Title, node.Description} {
			value = strings.TrimSpace(value)
			if isURL(value) && looksLikeAddressField(node) {
				return value
			}
		}
	}
	return ""
}

func looksLikeAddressField(node computeruse.ElementNode) bool {
	role := strings.TrimSpace(node.Role)
	if role != "AXTextField" && role != "AXComboBox" {
		return false
	}
	text := strings.ToLower(strings.Join([]string{node.Title, node.Description, node.Identifier}, " "))
	return strings.Contains(text, "address") ||
		strings.Contains(text, "location") ||
		strings.Contains(text, "url") ||
		strings.Contains(text, "search")
}

func isURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != ""
}

func isBrowserApp(app computeruse.AppInfo) bool {
	text := strings.ToLower(app.Name + " " + app.BundleID)
	for _, term := range []string{"brave", "chrome", "chromium", "safari", "firefox", "arc"} {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func normalizeDomain(domain string) string {
	domain = strings.ToLower(strings.TrimSpace(domain))
	domain = strings.TrimPrefix(domain, "http://")
	domain = strings.TrimPrefix(domain, "https://")
	if i := strings.IndexByte(domain, '/'); i >= 0 {
		domain = domain[:i]
	}
	return strings.Trim(domain, ".")
}
