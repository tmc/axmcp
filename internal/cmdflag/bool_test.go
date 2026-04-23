package cmdflag

import "testing"

func TestBool(t *testing.T) {
	tests := []struct {
		name string
		args []string
		flag string
		def  bool
		want bool
	}{
		{
			name: "default true",
			flag: "--ghost-cursor",
			def:  true,
			want: true,
		},
		{
			name: "explicit true",
			args: []string{"--ghost-cursor=true"},
			flag: "--ghost-cursor",
			def:  false,
			want: true,
		},
		{
			name: "explicit false",
			args: []string{"--ghost-cursor=false"},
			flag: "--ghost-cursor",
			def:  true,
			want: false,
		},
		{
			name: "negated long form",
			args: []string{"--no-ghost-cursor"},
			flag: "--ghost-cursor",
			def:  true,
			want: false,
		},
		{
			name: "plain presence",
			args: []string{"--verbose"},
			flag: "--verbose",
			def:  false,
			want: true,
		},
		{
			name: "invalid value falls back",
			args: []string{"--ghost-cursor=maybe"},
			flag: "--ghost-cursor",
			def:  true,
			want: true,
		},
	}
	for _, tt := range tests {
		if got := Bool(tt.args, tt.flag, tt.def); got != tt.want {
			t.Fatalf("%s: Bool(%v, %q, %v) = %v, want %v", tt.name, tt.args, tt.flag, tt.def, got, tt.want)
		}
	}
}
