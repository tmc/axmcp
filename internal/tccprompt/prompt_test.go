package tccprompt

import "testing"

func TestNormalizeText(t *testing.T) {
	got := normalizeText("  axmcp.app\x00  needs   Screen Recording  ")
	want := "axmcp.app needs Screen Recording"
	if got != want {
		t.Fatalf("normalizeText(...) = %q, want %q", got, want)
	}
}

func TestContainsFold(t *testing.T) {
	if !containsFold("Request Permission", "request") {
		t.Fatalf("containsFold did not match request")
	}
	if containsFold("Request Permission", "deny") {
		t.Fatalf("containsFold matched deny unexpectedly")
	}
}

func TestMatchesPrompt(t *testing.T) {
	prompt := Prompt{
		Title:   "Screen Recording",
		Texts:   []string{`"axmcp.app" would like to record this screen.`},
		Buttons: []string{"Deny", "Open System Settings"},
	}
	if !matchesPrompt(prompt, "axmcp.app") {
		t.Fatalf("matchesPrompt returned false, want true")
	}
	if matchesPrompt(prompt, "other.app") {
		t.Fatalf("matchesPrompt returned true for other.app")
	}
}
