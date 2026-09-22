package buildversion

import "testing"

func TestString(t *testing.T) {
	if got := String(); got == "" {
		t.Fatal("String() = \"\", want a version")
	}
}
