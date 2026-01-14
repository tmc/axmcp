package project

import (
	"reflect"
	"testing"
)

func TestParseSchemes(t *testing.T) {
	output := `
Information about project "TestProject":
    Targets:
        TestProject
        TestProjectTests

    Build Configurations:
        Debug
        Release

    Schemes:
        TestScheme
        AnotherScheme
`
	want := []string{"TestScheme", "AnotherScheme"}
	got := parseSchemes(output)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseSchemes() = %v, want %v", got, want)
	}
}

func TestParseSchemesEmpty(t *testing.T) {
	output := `
Information about project "Empty":
    Schemes:
`
	var want []string
	got := parseSchemes(output)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseSchemes() = %v, want %v", got, want)
	}
}
