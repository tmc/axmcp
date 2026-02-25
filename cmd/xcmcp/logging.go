package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// initFileLog sets up slog with a MultiHandler writing to both stderr and
// ~/.xcmcp/xcmcp.log. Also bridges the standard log package to slog.
func initFileLog() {
	dir := filepath.Join(os.Getenv("HOME"), ".xcmcp")
	if err := os.MkdirAll(dir, 0750); err != nil {
		fmt.Fprintf(os.Stderr, "xcmcp: create log dir: %v\n", err)
		return
	}
	path := filepath.Join(dir, "xcmcp.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0640)
	if err != nil {
		fmt.Fprintf(os.Stderr, "xcmcp: open log file: %v\n", err)
		return
	}

	opts := &slog.HandlerOptions{Level: slog.LevelDebug, AddSource: true}
	multi := slog.NewMultiHandler(
		slog.NewTextHandler(os.Stderr, opts),
		slog.NewTextHandler(f, opts),
	)
	slog.SetDefault(slog.New(multi))
	log.SetFlags(0)
	log.SetOutput(os.Stderr)
	slog.Info("xcmcp starting", "log", path)
}

// broadcastLog sends a logging notification to all active MCP sessions
// and logs via slog.
func broadcastLog(s *mcp.Server, level mcp.LoggingLevel, logger, msg string) {
	slog.Info(msg, "logger", logger, "level", level)

	for session := range s.Sessions() {
		err := session.Log(context.Background(), &mcp.LoggingMessageParams{
			Level:  level,
			Logger: logger,
			Data:   msg,
		})
		if err != nil {
			slog.Warn("failed to send log to session", "err", err)
		}
	}
}
