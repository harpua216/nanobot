package sandbox

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// Command describes how to run an MCP server process.
// All containerized execution uses LXC — there is no Docker support.
type Command struct {
	PublishPorts []string
	ReversePorts []int
	Roots        []Root
	Command      string
	Workdir      string
	Args         []string
	Env          []string
}

type Root struct {
	Name string
	Path string
}

type Cmd struct {
	*exec.Cmd
	cancel    func()
	postStart func() error
}

func (c *Cmd) Wait() error {
	if c.cancel != nil {
		defer c.cancel()
	}
	return c.Cmd.Wait()
}

func (c *Cmd) Start() error {
	if err := c.Cmd.Start(); err != nil {
		return err
	}
	if c.postStart == nil {
		return nil
	}
	if err := c.postStart(); err != nil {
		c.cancel()
		_ = c.Wait()
		return fmt.Errorf("post-start hook failed: %w", err)
	}
	return nil
}

// NewCmd creates a Cmd that runs the MCP server inside an LXC container.
// cfg holds the LXC-specific options for this server.
func NewCmd(ctx context.Context, cmd Command, cfg LXCConfig) (*Cmd, error) {
	return NewLXCCmd(ctx, cmd, cfg)
}

// PipeOut copies output from r to stderr, prefixed with serverName.
func PipeOut(ctx context.Context, r io.Reader, serverName string) {
	buf := make([]byte, 4096)
	var partial []byte
	for {
		n, err := r.Read(buf)
		if n > 0 {
			partial = append(partial, buf[:n]...)
			for {
				idx := -1
				for i, b := range partial {
					if b == '\n' {
						idx = i
						break
					}
				}
				if idx < 0 {
					break
				}
				_, _ = fmt.Fprintf(os.Stderr, "[%s] %s\n", serverName, string(partial[:idx]))
				partial = partial[idx+1:]
			}
		}
		if err != nil {
			if len(partial) > 0 {
				_, _ = fmt.Fprintf(os.Stderr, "[%s] %s\n", serverName, string(partial))
			}
			return
		}
	}
}

// allowedEnv is the minimal set of OS env vars passed to container processes.
var allowedEnv = map[string]bool{
	"PATH": true,
	"HOME": true,
	"USER": true,
}

func cleanOSEnv() []string {
	cleaned := make([]string, 0, len(allowedEnv))
	for _, e := range os.Environ() {
		k := e
		for i := range len(e) {
			if e[i] == '=' {
				k = e[:i]
				break
			}
		}
		if allowedEnv[k] {
			cleaned = append(cleaned, e)
		}
	}
	return cleaned
}
