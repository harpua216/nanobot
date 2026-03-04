package sandbox

import (
	"context"
	"fmt"
	"sync"

	"github.com/nanobot-ai/nanobot/pkg/log"
	"github.com/nanobot-ai/nanobot/pkg/reverseproxy"
	"github.com/nanobot-ai/nanobot/pkg/supervise"
)

// startReversePort sets up a reverse-proxy tunnel for a port inside an LXC
// container. The proxy listens on the host and forwards into the container via
// its network namespace using nsenter(1). No Docker sidecar is required.
//
// containerNS is the path to the container's network namespace, e.g.
// /proc/<pid>/ns/net or /run/lxc/ns/<name>/net. When empty the host netns is
// used (works for ephemeral containers with lxc.net.0.type = none).
func startReversePort(ctx context.Context, containerNS string, port int, cancel func()) error {
	server, err := reverseproxy.NewTLSServer(port)
	if err != nil {
		return fmt.Errorf("reverse port %d: create TLS server: %w", port, err)
	}

	targetPort, err := server.Start(ctx)
	if err != nil {
		return fmt.Errorf("reverse port %d: start TLS server: %w", port, err)
	}

	ca, err := server.GetCACertPEM()
	if err != nil {
		return fmt.Errorf("reverse port %d: get CA cert: %w", port, err)
	}

	cert, key, err := server.GenerateClientCert()
	if err != nil {
		return fmt.Errorf("reverse port %d: generate client cert: %w", port, err)
	}

	// Run the proxy helper inside the container's network namespace so it can
	// reach the container's loopback. nsenter is available on any Linux host.
	var nsenterArgs []string
	if containerNS != "" {
		nsenterArgs = append(nsenterArgs, "--net="+containerNS)
	}
	nsenterArgs = append(nsenterArgs, "--", "proxy")

	label := fmt.Sprintf("reverseport-%d", port)
	cmd := supervise.Cmd(ctx, "nsenter", nsenterArgs...)
	cmd.Env = append(cleanOSEnv(),
		fmt.Sprintf("LISTEN_PORT=%d", port),
		fmt.Sprintf("TARGET_PORT=%d", targetPort),
		fmt.Sprintf("CA_CERT=%s", ca),
		fmt.Sprintf("CLIENT_CERT=%s", cert),
		fmt.Sprintf("CLIENT_KEY=%s", key),
	)

	_, err = cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("reverse port %d: stdin pipe: %w", port, err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("reverse port %d: stdout pipe: %w", port, err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("reverse port %d: stderr pipe: %w", port, err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("reverse port %d: start: %w", port, err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		PipeOut(ctx, stdoutPipe, label)
	}()
	go func() {
		defer wg.Done()
		PipeOut(ctx, stderrPipe, label)
	}()
	go func() {
		wg.Wait()
		if err := cmd.Wait(); err != nil {
			log.Errorf(ctx, "reverse port %d exited with error: %v", port, err)
		}
		cancel()
	}()

	return nil
}
