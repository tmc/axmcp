package winstate

import (
	"reflect"
	"testing"
)

func TestParseWindowsKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want windowsKey
	}{
		{
			name: "letter",
			key:  "a",
			want: windowsKey{VK: 'A', Char: 'a'},
		},
		{
			name: "navigation",
			key:  "Return",
			want: windowsKey{VK: vkReturn},
		},
		{
			name: "modifier combo",
			key:  "ctrl+shift+c",
			want: windowsKey{Modifiers: []uint16{vkCtrl, vkShift}, VK: 'C', Char: 'c'},
		},
		{
			name: "super alias",
			key:  "super+space",
			want: windowsKey{Modifiers: []uint16{vkLWin}, VK: vkSpace, Char: ' '},
		},
	}
	for _, tt := range tests {
		got, err := parseWindowsKey(tt.key)
		if err != nil {
			t.Fatalf("%s: parseWindowsKey: %v", tt.name, err)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("%s: parseWindowsKey = %#v, want %#v", tt.name, got, tt.want)
		}
	}
}

func TestParseWindowsKeyRejectsUnknown(t *testing.T) {
	if _, err := parseWindowsKey("ctrl+unknown-key"); err == nil {
		t.Fatalf("parseWindowsKey unknown = nil, want error")
	}
	if _, err := parseWindowsKey("hyper+a"); err == nil {
		t.Fatalf("parseWindowsKey unknown modifier = nil, want error")
	}
}
