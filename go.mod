module github.com/tmc/xcmcp

go 1.26

require (
	github.com/ebitengine/purego v0.10.0-alpha.4
	github.com/modelcontextprotocol/go-sdk v1.2.0
	github.com/spf13/cobra v1.10.2
	github.com/tmc/appledocs/generated v0.0.0-00010101000000-000000000000
	github.com/tmc/macgo v0.0.0-00010101000000-000000000000
	golang.org/x/image v0.34.0
)

require (
	github.com/google/jsonschema-go v0.3.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.30.0 // indirect
	golang.org/x/sys v0.39.0 // indirect
)

replace github.com/tmc/appledocs => ../../appledocs

replace github.com/tmc/appledocs/generated => ../../appledocs/generated

replace github.com/tmc/macgo => ../../macgo
