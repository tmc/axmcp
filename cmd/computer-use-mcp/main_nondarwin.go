//go:build !darwin

package main

import (
	"context"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	rt, err := newRuntimeState()
	if err != nil {
		log.Fatalf("runtime: %v", err)
	}
	if err := newComputerUseServer(rt).Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server: %v", err)
	}
}
