package resources

import (
	"context"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestProjectRootForRequestUsesFallbackWithoutSessionRoots(t *testing.T) {
	if got := projectRootForRequest(context.Background(), nil, "/tmp/project"); got != "/tmp/project" {
		t.Fatalf("projectRootForRequest fallback = %q, want /tmp/project", got)
	}
	if got := projectRootForRequest(context.Background(), nil, ""); got != "." {
		t.Fatalf("projectRootForRequest empty fallback = %q, want .", got)
	}
}

func TestSessionFileRootsNilSession(t *testing.T) {
	if got := sessionFileRoots(context.Background(), nil); got != nil {
		t.Fatalf("sessionFileRoots nil session = %#v, want nil", got)
	}
}

func TestFileRootsFromListRootsResult(t *testing.T) {
	got := fileRootsFromListRootsResult(&mcp.ListRootsResult{Roots: []*mcp.Root{
		nil,
		{URI: "file:///tmp/z/../z"},
		{URI: "https://example.com/project"},
		{URI: "file://"},
		{URI: "file:///tmp/a"},
		{URI: "%zz"},
	}})
	want := []string{"/tmp/a", "/tmp/z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fileRootsFromListRootsResult = %#v, want %#v", got, want)
	}
}
