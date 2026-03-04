// Package zfs provides ZFS dataset management for nanobot context memory.
//
// Instead of a vector-database RAG pipeline, nanobot can use ZFS datasets as
// its context-memory backing store. Each agent session maps to a ZFS dataset,
// giving you:
//
//   - Instant clones for new agent personas (zfs clone)
//   - Memory checkpoints via snapshots (zfs snapshot)
//   - Point-in-time rollback of agent context (zfs rollback)
//   - Per-account resource quotas (zfs set quota=)
//   - ZFS send/receive for migrating agent state across Proxmox nodes
//   - Transparent compression (lz4) reducing context storage overhead
//
// Dataset layout on the ZFS pool:
//
//	<pool>/<base>/
//	  sessions/<sessionID>/         ← live session state (SQLite + files)
//	  templates/<templateName>/     ← agent persona templates
//	  accounts/<accountID>/         ← per-account namespaced datasets
//	    sessions/<sessionID>/
//	    resources/
package zfs

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nanobot-ai/nanobot/pkg/log"
)

// Manager manages ZFS datasets for nanobot context storage.
// It wraps the zfs(8) CLI and requires the zfsutils-linux package
// (or equivalent) to be installed on the Proxmox host.
type Manager struct {
	// Pool is the ZFS pool name (e.g. "tank", "rpool").
	Pool string
	// BaseDataset is the dataset under Pool where nanobot data lives
	// (e.g. "nanobot"). The full base path becomes <Pool>/<BaseDataset>.
	BaseDataset string
	// MountBase is the root path where datasets are mounted.
	// If empty, the default ZFS mountpoint is used.
	MountBase string
	// Compression sets the ZFS compression algorithm for new datasets.
	// Defaults to "lz4".
	Compression string
}

// NewManager creates a Manager with sensible defaults.
// pool is the ZFS pool (e.g. "tank"), baseDataset is the sub-dataset
// (e.g. "nanobot"), and mountBase is the filesystem mount root
// (e.g. "/mnt/nanobot"). If mountBase is empty the ZFS default mountpoint
// is inherited.
func NewManager(pool, baseDataset, mountBase string) *Manager {
	if pool == "" {
		pool = "tank"
	}
	if baseDataset == "" {
		baseDataset = "nanobot"
	}
	return &Manager{
		Pool:        pool,
		BaseDataset: baseDataset,
		MountBase:   mountBase,
		Compression: "lz4",
	}
}

// base returns the root dataset path: <pool>/<baseDataset>.
func (m *Manager) base() string {
	return m.Pool + "/" + m.BaseDataset
}

// datasetPath builds a full dataset path under the manager base.
func (m *Manager) datasetPath(parts ...string) string {
	return m.base() + "/" + strings.Join(parts, "/")
}

// MountPath returns the filesystem path where a dataset is (or will be)
// mounted. If MountBase is set the path is constructed as
// <MountBase>/<suffix…>; otherwise the ZFS default /<pool>/<dataset>
// path is returned.
func (m *Manager) MountPath(parts ...string) string {
	if m.MountBase != "" {
		return filepath.Join(append([]string{m.MountBase}, parts...)...)
	}
	// Default ZFS mountpoint mirrors the dataset hierarchy under /.
	return "/" + m.datasetPath(parts...)
}

// SessionDatasetPath returns the ZFS dataset name for a session.
func (m *Manager) SessionDatasetPath(sessionID string) string {
	return m.datasetPath("sessions", sessionID)
}

// AccountSessionDatasetPath returns the dataset for a session scoped to an account.
func (m *Manager) AccountSessionDatasetPath(accountID, sessionID string) string {
	return m.datasetPath("accounts", accountID, "sessions", sessionID)
}

// TemplateDatasetPath returns the dataset for an agent persona template.
func (m *Manager) TemplateDatasetPath(templateName string) string {
	return m.datasetPath("templates", templateName)
}

// Init creates the base dataset hierarchy if it does not exist.
// Safe to call multiple times (idempotent).
func (m *Manager) Init(ctx context.Context) error {
	for _, path := range []string{
		m.base(),
		m.datasetPath("sessions"),
		m.datasetPath("templates"),
		m.datasetPath("accounts"),
	} {
		if err := m.createIfNotExists(ctx, path); err != nil {
			return err
		}
	}
	log.Infof(ctx, "zfs: initialised base datasets under %s", m.base())
	return nil
}

// CreateSessionDataset creates a ZFS dataset for a session.
// Returns the filesystem mount path where session data should be stored.
func (m *Manager) CreateSessionDataset(ctx context.Context, sessionID string) (string, error) {
	dataset := m.SessionDatasetPath(sessionID)
	if err := m.createDataset(ctx, dataset); err != nil {
		return "", fmt.Errorf("zfs: create session dataset %s: %w", dataset, err)
	}
	mountPath := m.MountPath("sessions", sessionID)
	log.Infof(ctx, "zfs: created session dataset %s → %s", dataset, mountPath)
	return mountPath, nil
}

// EnsureSessionDataset creates the session dataset if it does not exist and
// returns the mount path.
func (m *Manager) EnsureSessionDataset(ctx context.Context, sessionID string) (string, error) {
	dataset := m.SessionDatasetPath(sessionID)
	exists, err := m.datasetExists(ctx, dataset)
	if err != nil {
		return "", err
	}
	if exists {
		return m.MountPath("sessions", sessionID), nil
	}
	return m.CreateSessionDataset(ctx, sessionID)
}

// DestroySessionDataset destroys a session dataset and all its snapshots.
func (m *Manager) DestroySessionDataset(ctx context.Context, sessionID string) error {
	dataset := m.SessionDatasetPath(sessionID)
	return m.destroyDataset(ctx, dataset)
}

// SnapshotSession creates a named snapshot of a session dataset.
// Snapshot name can be anything meaningful, e.g. "before-tool-call" or a timestamp.
// Returns the full snapshot name: <dataset>@<snapName>.
func (m *Manager) SnapshotSession(ctx context.Context, sessionID, snapName string) (string, error) {
	snap := m.SessionDatasetPath(sessionID) + "@" + snapName
	if err := m.snapshot(ctx, snap); err != nil {
		return "", fmt.Errorf("zfs: snapshot session %s@%s: %w", sessionID, snapName, err)
	}
	log.Infof(ctx, "zfs: snapshot %s created", snap)
	return snap, nil
}

// RollbackSession rolls back a session dataset to the named snapshot.
// All changes made after the snapshot are discarded.
func (m *Manager) RollbackSession(ctx context.Context, sessionID, snapName string) error {
	snap := m.SessionDatasetPath(sessionID) + "@" + snapName
	return m.rollback(ctx, snap)
}

// ListSessionSnapshots returns all snapshot names for a session dataset.
func (m *Manager) ListSessionSnapshots(ctx context.Context, sessionID string) ([]string, error) {
	dataset := m.SessionDatasetPath(sessionID)
	return m.listSnapshots(ctx, dataset)
}

// CreateTemplate creates a persona-template dataset.
// After populating it with the desired base state, call SnapshotTemplate
// to seal it before cloning.
func (m *Manager) CreateTemplate(ctx context.Context, templateName string) (string, error) {
	dataset := m.TemplateDatasetPath(templateName)
	if err := m.createDataset(ctx, dataset); err != nil {
		return "", fmt.Errorf("zfs: create template %s: %w", templateName, err)
	}
	return m.MountPath("templates", templateName), nil
}

// SnapshotTemplate seals a template by creating a snapshot called "base".
// Call this once after the template dataset is fully populated.
func (m *Manager) SnapshotTemplate(ctx context.Context, templateName string) error {
	snap := m.TemplateDatasetPath(templateName) + "@base"
	return m.snapshot(ctx, snap)
}

// CloneTemplateToSession creates a new session dataset as a ZFS clone of a
// template's "base" snapshot. This is the fast-path for spinning up a new
// agent persona — no data needs to be copied.
//
// After cloning, the new session dataset is independent and can be snapshot/
// rolled back without affecting the template.
func (m *Manager) CloneTemplateToSession(ctx context.Context, templateName, sessionID string) (string, error) {
	srcSnap := m.TemplateDatasetPath(templateName) + "@base"
	dstDataset := m.SessionDatasetPath(sessionID)
	mountPath := m.MountPath("sessions", sessionID)

	args := []string{"clone"}
	if m.MountBase != "" {
		args = append(args, "-o", "mountpoint="+mountPath)
	}
	args = append(args, srcSnap, dstDataset)

	out, err := exec.CommandContext(ctx, "zfs", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("zfs clone %s → %s: %w\noutput: %s", srcSnap, dstDataset, err, out)
	}
	log.Infof(ctx, "zfs: cloned %s → %s", srcSnap, dstDataset)
	return mountPath, nil
}

// SetSessionQuota sets a byte quota on a session dataset to prevent runaway
// context growth. size is in bytes (e.g. 1<<30 for 1 GiB).
func (m *Manager) SetSessionQuota(ctx context.Context, sessionID string, sizeBytes int64) error {
	dataset := m.SessionDatasetPath(sessionID)
	quota := fmt.Sprintf("%d", sizeBytes)
	out, err := exec.CommandContext(ctx, "zfs", "set", "quota="+quota, dataset).CombinedOutput()
	if err != nil {
		return fmt.Errorf("zfs set quota on %s: %w\noutput: %s", dataset, err, out)
	}
	return nil
}

// DBPath returns the path to the SQLite database file for a session.
// The DB lives inside the ZFS dataset so it inherits snapshot/rollback
// semantics along with all other session context files.
func (m *Manager) DBPath(sessionID string) string {
	return filepath.Join(m.MountPath("sessions", sessionID), "session.db")
}

// DSN returns a SQLite DSN string pointing at the session's ZFS-backed DB.
func (m *Manager) DSN(sessionID string) string {
	return "sqlite:" + m.DBPath(sessionID)
}

// --- low-level helpers ---

func (m *Manager) createIfNotExists(ctx context.Context, dataset string) error {
	exists, err := m.datasetExists(ctx, dataset)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return m.createDataset(ctx, dataset)
}

func (m *Manager) createDataset(ctx context.Context, dataset string) error {
	args := []string{"create", "-p"}
	if m.Compression != "" {
		args = append(args, "-o", "compression="+m.Compression)
	}
	if m.MountBase != "" {
		// Construct mountpoint from dataset suffix relative to pool/base.
		rel := strings.TrimPrefix(dataset, m.Pool+"/")
		mountpoint := filepath.Join(m.MountBase, strings.TrimPrefix(rel, m.BaseDataset+"/"))
		args = append(args, "-o", "mountpoint="+mountpoint)
	}
	args = append(args, dataset)
	out, err := exec.CommandContext(ctx, "zfs", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("zfs create %s: %w\noutput: %s", dataset, err, out)
	}
	return nil
}

func (m *Manager) destroyDataset(ctx context.Context, dataset string) error {
	out, err := exec.CommandContext(ctx, "zfs", "destroy", "-r", dataset).CombinedOutput()
	if err != nil {
		return fmt.Errorf("zfs destroy %s: %w\noutput: %s", dataset, err, out)
	}
	return nil
}

func (m *Manager) snapshot(ctx context.Context, snapName string) error {
	out, err := exec.CommandContext(ctx, "zfs", "snapshot", snapName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("zfs snapshot %s: %w\noutput: %s", snapName, err, out)
	}
	return nil
}

func (m *Manager) rollback(ctx context.Context, snapName string) error {
	out, err := exec.CommandContext(ctx, "zfs", "rollback", "-r", snapName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("zfs rollback %s: %w\noutput: %s", snapName, err, out)
	}
	return nil
}

func (m *Manager) listSnapshots(ctx context.Context, dataset string) ([]string, error) {
	out, err := exec.CommandContext(ctx, "zfs", "list", "-H", "-t", "snapshot",
		"-o", "name", "-r", dataset).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("zfs list snapshots for %s: %w\noutput: %s", dataset, err, out)
	}
	var snaps []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Strip the dataset prefix, return only the snapshot name.
		if idx := strings.LastIndex(line, "@"); idx >= 0 {
			snaps = append(snaps, line[idx+1:])
		}
	}
	return snaps, nil
}

func (m *Manager) datasetExists(ctx context.Context, dataset string) (bool, error) {
	err := exec.CommandContext(ctx, "zfs", "list", "-H", "-o", "name", dataset).Run()
	if err != nil {
		// exit status 1 means "not found" — not a real error.
		return false, nil
	}
	return true, nil
}
