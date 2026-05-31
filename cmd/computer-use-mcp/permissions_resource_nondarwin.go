//go:build !darwin

package main

import "github.com/modelcontextprotocol/go-sdk/mcp"

func registerPermissionResource(*mcp.Server) {}
