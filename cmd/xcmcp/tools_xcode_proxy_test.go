//go:build darwin

package main

import "testing"

func TestShouldAttemptXcodeBridge(t *testing.T) {
	tests := []struct {
		name            string
		mcpXcodePID     string
		hasRunningXcode bool
		want            bool
	}{
		{
			name:        "explicit xcode pid",
			mcpXcodePID: "1234",
			want:        true,
		},
		{
			name:            "running xcode",
			hasRunningXcode: true,
			want:            true,
		},
		{
			name: "no xcode available",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldAttemptXcodeBridge(tt.mcpXcodePID, tt.hasRunningXcode)
			if got != tt.want {
				t.Fatalf("shouldAttemptXcodeBridge(%q, %t) = %t, want %t", tt.mcpXcodePID, tt.hasRunningXcode, got, tt.want)
			}
		})
	}
}
