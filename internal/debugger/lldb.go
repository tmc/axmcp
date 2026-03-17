package debugger

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type process interface {
	Run(context.Context, string) (string, error)
	Close() error
}

type lldbProcess struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	lines   chan string
	done    chan error
	mu      sync.Mutex
	counter atomic.Int64
}

func newLLDBProcess(ctx context.Context, pid int) (*lldbProcess, string, error) {
	cmd := exec.Command("/usr/bin/xcrun", "lldb", "--no-lldbinit")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, "", err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, "", err
	}
	cmd.WaitDelay = 2 * time.Second

	p := &lldbProcess{
		cmd:   cmd,
		stdin: stdin,
		lines: make(chan string, 256),
		done:  make(chan error, 1),
	}
	if err := cmd.Start(); err != nil {
		return nil, "", err
	}
	go p.readPipe(stdout)
	go p.readPipe(stderr)
	go func() {
		p.done <- cmd.Wait()
		close(p.done)
		close(p.lines)
	}()

	bootstrap := bootstrapCommands(pid)
	var out strings.Builder
	for _, command := range bootstrap {
		text, err := p.Run(ctx, command)
		if err != nil {
			_ = p.Close()
			return nil, out.String(), err
		}
		if text != "" {
			if out.Len() > 0 {
				out.WriteByte('\n')
			}
			out.WriteString(text)
		}
	}
	return p, out.String(), nil
}

func (p *lldbProcess) Run(ctx context.Context, command string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	sentinel := fmt.Sprintf("__XCMCP_DONE_%d__", p.counter.Add(1))
	if _, err := io.WriteString(p.stdin, command+"\n"); err != nil {
		return "", err
	}
	if _, err := io.WriteString(p.stdin, fmt.Sprintf("script print(%q)\n", sentinel)); err != nil {
		return "", err
	}

	var lines []string
	for {
		select {
		case <-ctx.Done():
			return cleanLLDBOutput(lines), ctx.Err()
		case err, ok := <-p.done:
			if ok && err != nil {
				return cleanLLDBOutput(lines), err
			}
			return cleanLLDBOutput(lines), errors.New("lldb process exited")
		case line, ok := <-p.lines:
			if !ok {
				return cleanLLDBOutput(lines), errors.New("lldb output closed")
			}
			if strings.TrimSpace(line) == sentinel {
				out := cleanLLDBOutput(lines)
				if isErrorOutput(out) {
					return out, errors.New(out)
				}
				return out, nil
			}
			lines = append(lines, line)
		}
	}
}

func (p *lldbProcess) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stdin != nil {
		_, _ = io.WriteString(p.stdin, "quit\n")
		_ = p.stdin.Close()
		p.stdin = nil
	}
	return nil
}

func (p *lldbProcess) readPipe(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		p.lines <- scanner.Text()
	}
}

func cleanLLDBOutput(lines []string) string {
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "(lldb)") {
			continue
		}
		filtered = append(filtered, line)
	}
	return strings.TrimSpace(strings.Join(filtered, "\n"))
}

func isErrorOutput(out string) bool {
	out = strings.TrimSpace(out)
	if out == "" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(out), "error:") ||
		strings.Contains(strings.ToLower(out), "\nerror:")
}

func bootstrapCommands(pid int) []string {
	return []string{
		"settings set auto-confirm true",
		"settings set use-color false",
		fmt.Sprintf("process attach --pid %d", pid),
		"thread list",
	}
}
