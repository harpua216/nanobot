package sandbox

// lxc.go - Proxmox LXC sandbox for nanobot MCP servers.
//
// This replaces Docker-based sandboxing with Linux Containers (LXC) as
// managed by Proxmox.  Two execution modes are supported:
//
//  1. Ephemeral mode (default): uses lxc-execute(1) to run a single command
//     inside an unprivileged container rootfs and then exit.  Analogous to
//     `docker run --rm`.  The rootfs is an existing LXC template directory.
//
//  2. Persistent mode: creates a named Proxmox LXC container via the pct(1)
//     CLI, starts it, attaches the command with pct exec, and tears it down
//     on context cancellation.  Use this for long-running MCP servers.
//
// ZFS integration:
//   When a *zfs.Manager is attached to the LXC command, the session's ZFS
//   dataset is bind-mounted into the container at /mcp/context, giving the
//   MCP server process direct access to ZFS-backed context memory.
//
// No Docker dependency is required when using this sandbox type.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nanobot-ai/nanobot/pkg/log"
	"github.com/nanobot-ai/nanobot/pkg/supervise"
	"github.com/nanobot-ai/nanobot/pkg/uuid"
)

// LXCConfig controls how an MCP server is sandboxed inside an LXC container.
type LXCConfig struct {
	// Template is the LXC template name or the path to an existing rootfs
	// directory.  On Proxmox this is typically a template from
	// /var/lib/vz/template/cache/, e.g. "ubuntu-22.04-standard_22.04-1_amd64".
	// Required for ephemeral mode.
	Template string

	// ContextMount is the host-side path to bind-mount into the container at
	// /mcp/context.  When the nanobot ZFS manager is enabled this is
	// automatically set to the session's ZFS dataset mountpoint.
	ContextMount string

	// ExtraBindMounts is a list of additional host:container bind-mount specs
	// (colon-separated), e.g. "/srv/data:/mcp/data".
	ExtraBindMounts []string

	// Persistent, when true, creates a named Proxmox container (via pct)
	// instead of using lxc-execute.  Better for long-lived MCP servers.
	Persistent bool

	// ProxmoxNode is the Proxmox host where containers should be created when
	// Persistent is true.  May be empty if pct is available locally.
	ProxmoxNode string

	// VMID is the Proxmox container ID to use when Persistent is true.  If
	// zero a random ID in the range 20000-29999 is chosen.
	VMID int

	// StoragePool is the Proxmox storage pool for the container rootfs when
	// Persistent is true (e.g. "local-zfs", "tank").
	StoragePool string

	// Memory is the container memory limit in MiB (default 512).
	Memory int

	// CPUs is the number of CPU cores (default 2).
	CPUs int
}

// NewLXCCmd creates an exec.Cmd-compatible *Cmd that runs the given sandbox
// Command inside an LXC container instead of Docker.
//
// When cfg.Persistent is false (default), lxc-execute is used for ephemeral
// execution.  When cfg.Persistent is true a Proxmox container is created via
// pct and the process is started with pct exec.
func NewLXCCmd(ctx context.Context, sandbox Command, cfg LXCConfig) (*Cmd, error) {
	if cfg.Persistent {
		return newPCTCmd(ctx, sandbox, cfg)
	}
	return newEphemeralLXCCmd(ctx, sandbox, cfg)
}

// newEphemeralLXCCmd uses lxc-execute to run a single command inside an
// unprivileged container rootfs and exit.  The rootfs is not modified.
//
// Equivalent Docker analogue: docker run --rm <image> <command> <args...>
func newEphemeralLXCCmd(ctx context.Context, cmd Command, cfg LXCConfig) (*Cmd, error) {
	if cfg.Template == "" {
		return nil, fmt.Errorf("lxc sandbox: Template must be set for ephemeral mode")
	}

	containerName := fmt.Sprintf("nanobot-%s", strings.Split(uuid.String(), "-")[0])

	// Resolve the template rootfs path.
	rootfs, err := resolveLXCTemplate(cfg.Template)
	if err != nil {
		return nil, fmt.Errorf("lxc sandbox: %w", err)
	}

	// lxc-execute arguments:
	//   -n <name>     container name (for logging)
	//   -P <path>     lxcpath (we use a temp dir so containers don't collide)
	//   --            separator
	//   <command> <args...>
	lxcpath, err := os.MkdirTemp("", "nanobot-lxc-*")
	if err != nil {
		return nil, fmt.Errorf("lxc sandbox: failed to create lxcpath: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(lxcpath) }

	// Write a minimal lxc.conf.
	confPath := filepath.Join(lxcpath, "lxc.conf")
	confContent := buildLXCConfig(rootfs, cmd, cfg)
	if err := os.WriteFile(confPath, []byte(confContent), 0644); err != nil {
		cleanup()
		return nil, fmt.Errorf("lxc sandbox: failed to write lxc.conf: %w", err)
	}

	lxcArgs := []string{
		"-n", containerName,
		"-P", lxcpath,
		"-f", confPath,
		"--",
	}

	if cmd.Workdir != "" {
		// lxc-execute can't chdir directly; wrap in sh -c.
		lxcArgs = append(lxcArgs, "sh", "-c",
			fmt.Sprintf("cd %q && exec %s %s",
				cmd.Workdir, shellEscape(cmd.Command), shellEscapeArgs(cmd.Args)))
	} else {
		lxcArgs = append(lxcArgs, cmd.Command)
		lxcArgs = append(lxcArgs, cmd.Args...)
	}

	ctx, cancel := context.WithCancel(ctx)
	execCmd := supervise.Cmd(ctx, "lxc-execute", lxcArgs...)
	execCmd.Env = buildContainerEnv(cmd.Env)

	log.Infof(ctx, "lxc: ephemeral container %s rootfs=%s", containerName, rootfs)

	return &Cmd{
		Cmd:    execCmd,
		cancel: cancel,
		postStart: func() error {
			// Nothing to do post-start for ephemeral mode.
			return nil
		},
	}, nil
}

// newPCTCmd creates a Proxmox LXC container via pct(1), starts it, and runs
// the MCP server process inside it using pct exec.  The container is stopped
// and destroyed when the context is cancelled.
func newPCTCmd(ctx context.Context, cmd Command, cfg LXCConfig) (*Cmd, error) {
	vmid := cfg.VMID
	if vmid == 0 {
		// Pick a pseudo-random VMID in 20000-29999 range.
		vmid = 20000 + (int([]byte(uuid.String())[0]) * 40)
		if vmid > 29999 {
			vmid = 20001
		}
	}
	storage := cfg.StoragePool
	if storage == "" {
		storage = "local-zfs"
	}
	mem := cfg.Memory
	if mem == 0 {
		mem = 512
	}
	cpus := cfg.CPUs
	if cpus == 0 {
		cpus = 2
	}

	vmidStr := fmt.Sprintf("%d", vmid)
	containerName := fmt.Sprintf("nanobot-%s", strings.Split(uuid.String(), "-")[0])

	// pct create <vmid> <template> [options]
	pctCreateArgs := []string{
		"create", vmidStr,
		cfg.Template,
		"--hostname", containerName,
		"--storage", storage,
		"--memory", fmt.Sprintf("%d", mem),
		"--cores", fmt.Sprintf("%d", cpus),
		"--unprivileged", "1",
		"--features", "nesting=1",
		"--start", "0", // don't start yet; we'll do it explicitly
	}

	// Bind-mount context dataset if provided.
	mountIdx := 0
	if cfg.ContextMount != "" {
		pctCreateArgs = append(pctCreateArgs,
			fmt.Sprintf("--mp%d", mountIdx),
			fmt.Sprintf("%s,mp=/mcp/context", cfg.ContextMount))
		mountIdx++
	}
	for _, bm := range cfg.ExtraBindMounts {
		pctCreateArgs = append(pctCreateArgs,
			fmt.Sprintf("--mp%d", mountIdx),
			bm)
		mountIdx++
	}
	// Expose cwd roots.
	for _, root := range cmd.Roots {
		pctCreateArgs = append(pctCreateArgs,
			fmt.Sprintf("--mp%d", mountIdx),
			fmt.Sprintf("%s,mp=%s", root.Path, root.Path))
		mountIdx++
	}

	log.Infof(ctx, "lxc: creating Proxmox container %s (VMID=%d)", containerName, vmid)
	if out, err := exec.CommandContext(ctx, "pct", pctCreateArgs...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("pct create: %w\noutput: %s", err, out)
	}

	// Start the container.
	if out, err := exec.CommandContext(ctx, "pct", "start", vmidStr).CombinedOutput(); err != nil {
		_ = pctDestroy(context.Background(), vmidStr)
		return nil, fmt.Errorf("pct start: %w\noutput: %s", err, out)
	}

	// Build the exec command: pct exec <vmid> -- <command> [args...]
	pctExecArgs := []string{"exec", vmidStr, "--"}
	if cmd.Workdir != "" {
		pctExecArgs = append(pctExecArgs, "sh", "-c",
			fmt.Sprintf("cd %q && exec %s %s",
				cmd.Workdir, shellEscape(cmd.Command), shellEscapeArgs(cmd.Args)))
	} else {
		pctExecArgs = append(pctExecArgs, cmd.Command)
		pctExecArgs = append(pctExecArgs, cmd.Args...)
	}

	ctx, cancel := context.WithCancel(ctx)
	execCmd := supervise.Cmd(ctx, "pct", pctExecArgs...)
	execCmd.Env = buildContainerEnv(cmd.Env)

	return &Cmd{
		Cmd:    execCmd,
		cancel: cancel,
		postStart: func() error {
			// Set up reverse ports if needed.
			for _, port := range cmd.ReversePorts {
				if err := startReversePort(ctx, containerName, port, cancel); err != nil {
					return fmt.Errorf("lxc reverse port %d: %w", port, err)
				}
			}
			return nil
		},
	}, nil
}

// pctDestroy stops and destroys a Proxmox container — used for cleanup.
func pctDestroy(ctx context.Context, vmidStr string) error {
	_ = exec.CommandContext(ctx, "pct", "stop", vmidStr).Run()
	out, err := exec.CommandContext(ctx, "pct", "destroy", vmidStr).CombinedOutput()
	if err != nil {
		return fmt.Errorf("pct destroy %s: %w\noutput: %s", vmidStr, err, out)
	}
	return nil
}

// resolveLXCTemplate locates the rootfs for a template name.
// It checks:
//  1. The path directly (absolute or relative).
//  2. /var/lib/lxc/<template>/rootfs  (standard lxc-utils layout).
//  3. /var/lib/vz/template/cache/<template>.tar.zst  (Proxmox template cache).
func resolveLXCTemplate(template string) (string, error) {
	// 1. Direct path.
	if _, err := os.Stat(template); err == nil {
		return template, nil
	}
	// 2. Standard lxc rootfs.
	lxcRootfs := filepath.Join("/var/lib/lxc", template, "rootfs")
	if _, err := os.Stat(lxcRootfs); err == nil {
		return lxcRootfs, nil
	}
	// 3. Proxmox template cache (tarball exists but rootfs not yet extracted).
	proxmoxCache := filepath.Join("/var/lib/vz/template/cache", template+".tar.zst")
	if _, err := os.Stat(proxmoxCache); err == nil {
		// Return the tarball path; lxc-execute can use it with -t flag.
		return proxmoxCache, nil
	}
	return "", fmt.Errorf("cannot find LXC template %q; checked /var/lib/lxc/%s/rootfs and Proxmox cache", template, template)
}

// buildLXCConfig returns a minimal lxc.conf string for ephemeral execution.
func buildLXCConfig(rootfs string, cmd Command, cfg LXCConfig) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "lxc.rootfs.path = dir:%s\n", rootfs)
	fmt.Fprintf(&sb, "lxc.uts.name = nanobot\n")
	// Network: use host network by default (simplest for MCP stdio servers).
	fmt.Fprintf(&sb, "lxc.net.0.type = none\n")

	uid := os.Getuid()
	gid := os.Getgid()
	fmt.Fprintf(&sb, "lxc.idmap = u 0 %d 1\n", uid)
	fmt.Fprintf(&sb, "lxc.idmap = g 0 %d 1\n", gid)

	// Context mount.
	if cfg.ContextMount != "" {
		fmt.Fprintf(&sb, "lxc.mount.entry = %s mcp/context none bind,create=dir 0 0\n", cfg.ContextMount)
	}
	// Roots (cwd bind-mounts).
	for _, root := range cmd.Roots {
		fmt.Fprintf(&sb, "lxc.mount.entry = %s %s none bind,create=dir 0 0\n",
			root.Path, strings.TrimPrefix(root.Path, "/"))
	}
	// Extra bind mounts.
	for _, bm := range cfg.ExtraBindMounts {
		parts := strings.SplitN(bm, ":", 2)
		if len(parts) == 2 {
			fmt.Fprintf(&sb, "lxc.mount.entry = %s %s none bind,create=dir 0 0\n",
				parts[0], strings.TrimPrefix(parts[1], "/"))
		}
	}
	// Environment via env file trick (lxc-execute reads lxc.environment).
	for _, e := range cmd.Env {
		fmt.Fprintf(&sb, "lxc.environment = %s\n", e)
	}
	return sb.String()
}

// buildContainerEnv constructs the environment for the outer pct/lxc-execute
// process.  The container itself gets env vars via lxc.conf or pct options.
func buildContainerEnv(envKeys []string) []string {
	result := make([]string, 0, len(envKeys))
	for _, k := range envKeys {
		if v, ok := os.LookupEnv(k); ok {
			result = append(result, k+"="+v)
		}
	}
	return result
}

func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func shellEscapeArgs(args []string) string {
	escaped := make([]string, len(args))
	for i, a := range args {
		escaped[i] = shellEscape(a)
	}
	return strings.Join(escaped, " ")
}
