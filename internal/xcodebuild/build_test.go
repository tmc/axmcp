package xcodebuild

import "testing"

func TestProductsFromSettings(t *testing.T) {
	products := productsFromSettings([]buildSettingEntry{
		{
			Target: "Mesh",
			BuildSettings: map[string]string{
				"TARGET_NAME":               "Mesh",
				"TARGET_BUILD_DIR":          "/tmp/Derived/Build/Products/Debug",
				"BUILT_PRODUCTS_DIR":        "/tmp/Derived/Build/Products/Debug",
				"WRAPPER_NAME":              "Mesh.app",
				"EXECUTABLE_PATH":           "Mesh.app/Contents/MacOS/Mesh",
				"PRODUCT_BUNDLE_IDENTIFIER": "dev.tmc.Mesh",
				"PRODUCT_NAME":              "Mesh",
				"PLATFORM_NAME":             "macosx",
				"SDK_NAME":                  "macosx",
				"CONFIGURATION":             "Debug",
				"PRODUCT_TYPE":              "com.apple.product-type.application",
			},
		},
	}, BuildOptions{Scheme: "Mesh"})

	if len(products) != 1 {
		t.Fatalf("len(products) = %d, want 1", len(products))
	}
	product := products[0]
	if product.BundlePath != "/tmp/Derived/Build/Products/Debug/Mesh.app" {
		t.Fatalf("BundlePath = %q", product.BundlePath)
	}
	if product.ExecutablePath != "/tmp/Derived/Build/Products/Debug/Mesh.app/Contents/MacOS/Mesh" {
		t.Fatalf("ExecutablePath = %q", product.ExecutablePath)
	}
	if product.BundleID != "dev.tmc.Mesh" {
		t.Fatalf("BundleID = %q", product.BundleID)
	}
	if product.Platform != "macosx" {
		t.Fatalf("Platform = %q", product.Platform)
	}
}

func TestParseDiagnosticsGroupsByFile(t *testing.T) {
	summary := parseDiagnostics(`/tmp/Mesh/App.swift:12:4: warning: old preview
/tmp/Mesh/App.swift:18:7: error: failed build
/tmp/Mesh/Other.swift:3:1: warning: another warning`)

	if summary.ErrorCount != 1 {
		t.Fatalf("ErrorCount = %d, want 1", summary.ErrorCount)
	}
	if summary.WarningCount != 2 {
		t.Fatalf("WarningCount = %d, want 2", summary.WarningCount)
	}
	if len(summary.Files) != 2 {
		t.Fatalf("len(Files) = %d, want 2", len(summary.Files))
	}
	if len(summary.Files[0].Warnings) != 1 || len(summary.Files[0].Errors) != 1 {
		t.Fatalf("first file diagnostics = %+v", summary.Files[0])
	}
}

func TestPrimaryAppProduct(t *testing.T) {
	product := PrimaryAppProduct([]BuildProduct{
		{Name: "MeshTests", BundlePath: "/tmp/MeshTests.xctest"},
		{Name: "Mesh", BundlePath: "/tmp/Mesh.app"},
	})
	if product == nil {
		t.Fatal("PrimaryAppProduct returned nil")
	}
	if product.Name != "Mesh" {
		t.Fatalf("PrimaryAppProduct.Name = %q, want Mesh", product.Name)
	}
}
