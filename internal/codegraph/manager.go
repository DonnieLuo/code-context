package codegraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/example/code-context/internal/config"
	"github.com/example/code-context/internal/repository"
	"github.com/example/code-context/internal/runner"
)

var ErrUnavailable = errors.New("codegraph unavailable")
var ErrBusy = errors.New("codegraph busy")
var ErrCapability = errors.New("unsupported codegraph capability")

type metadata struct {
	Revision  string    `json:"revision"`
	IndexedAt time.Time `json:"indexed_at"`
}
type Status struct {
	RepoID                string     `json:"repo_id"`
	RepoRevision          string     `json:"repo_revision"`
	IndexRevision         *string    `json:"index_revision"`
	IndexState            string     `json:"index_state"`
	IndexedAt             *time.Time `json:"indexed_at,omitempty"`
	WorkingTreeDirty      bool       `json:"working_tree_dirty"`
	ToolSchemaFingerprint string     `json:"tool_schema_fingerprint,omitempty"`
}
type session struct {
	mu        sync.Mutex
	startMu   sync.Mutex
	indexMu   sync.Mutex
	client    *Client
	sem       chan struct{}
	meta      metadata
	verified  bool
	building  bool
	nextRetry time.Time
	failures  int
}
type Manager struct {
	cfg          config.Config
	repos        *repository.Manager
	dataRoot     string
	mu           sync.Mutex
	sessions     map[string]*session
	closed       bool
	processSlots chan struct{}
}

func NewManager(cfg config.Config, repos *repository.Manager) (*Manager, error) {
	root, err := filepath.Abs(cfg.CodeGraph.DataRoot)
	if err != nil {
		return nil, err
	}
	if cfg.CodeGraph.Enabled {
		if err = os.MkdirAll(root, 0700); err != nil {
			return nil, err
		}
	}
	return &Manager{cfg: cfg, repos: repos, dataRoot: root, sessions: map[string]*session{}, processSlots: make(chan struct{}, cfg.CodeGraph.MaxProcesses)}, nil
}
func (m *Manager) repoDataDir(id string) string {
	return filepath.Join(m.dataRoot, fingerprint([]byte(id))[:24])
}
func (m *Manager) slot(id string) *session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[id]; s != nil {
		return s
	}
	s := &session{sem: make(chan struct{}, m.cfg.CodeGraph.MaxConcurrentPerRepo)}
	b, err := os.ReadFile(filepath.Join(m.repoDataDir(id), "index.json"))
	if err == nil {
		_ = json.Unmarshal(b, &s.meta)
	}
	m.sessions[id] = s
	return s
}
func (m *Manager) connect(ctx context.Context, repo repository.Repository, s *session) (*Client, error) {
	if !m.cfg.CodeGraph.Enabled {
		return nil, ErrUnavailable
	}
	s.startMu.Lock()
	defer s.startMu.Unlock()
	s.mu.Lock()
	cached := s.client
	retryAt := s.nextRetry
	s.mu.Unlock()
	if cached != nil && cached.Alive() {
		return cached, nil
	}
	if time.Now().Before(retryAt) {
		return nil, ErrUnavailable
	}
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return nil, ErrUnavailable
	}
	select {
	case m.processSlots <- struct{}{}:
	default:
		return nil, ErrBusy
	}
	args := append([]string{}, m.cfg.CodeGraph.Args...)
	// The wrapper starts MCP mode itself. Direct engine users configure --mcp.
	startCtx, cancel := context.WithTimeout(ctx, m.cfg.CodeGraph.StartupTimeout)
	defer cancel()
	c, err := Start(startCtx, m.cfg.CodeGraph.Command, args, repo.Path, m.repoDataDir(repo.ID), m.cfg.CodeGraph.Telemetry)
	if err != nil {
		<-m.processSlots
		s.mu.Lock()
		s.failures++
		delay := time.Second * time.Duration(1<<min(s.failures, 6))
		s.nextRetry = time.Now().Add(delay)
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		c.Close()
		<-m.processSlots
		return nil, ErrUnavailable
	}
	s.mu.Lock()
	s.client = c
	s.failures = 0
	s.nextRetry = time.Time{}
	s.mu.Unlock()
	m.mu.Unlock()
	go func() { <-c.done; <-m.processSlots }()
	return c, nil
}
func (m *Manager) Call(ctx context.Context, id, name string, args map[string]any) (CallResult, error) {
	repo, err := m.repos.Get(id)
	if err != nil {
		return CallResult{}, err
	}
	s := m.slot(id)
	s.mu.Lock()
	building := s.building
	s.mu.Unlock()
	if building && name != "codegraph_reindex_workspace" {
		return CallResult{}, ErrBusy
	}
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	default:
		return CallResult{}, ErrBusy
	}
	c, err := m.connect(ctx, repo, s)
	if err != nil {
		return CallResult{}, err
	}
	if _, ok := c.Tools()[name]; !ok {
		return CallResult{}, fmt.Errorf("%w: %s", ErrCapability, name)
	}
	callCtx, cancel := context.WithTimeout(ctx, m.cfg.CodeGraph.CallTimeout)
	defer cancel()
	return c.Call(callCtx, name, args)
}
func (m *Manager) Tool(ctx context.Context, id, name string) (Tool, error) {
	repo, err := m.repos.Get(id)
	if err != nil {
		return Tool{}, err
	}
	c, err := m.connect(ctx, repo, m.slot(id))
	if err != nil {
		return Tool{}, err
	}
	t, ok := c.Tools()[name]
	if !ok {
		return Tool{}, fmt.Errorf("%w: %s", ErrCapability, name)
	}
	return t, nil
}
func (m *Manager) Snapshot(ctx context.Context, id string) (Status, error) {
	repo, err := m.repos.Get(id)
	if err != nil {
		return Status{}, err
	}
	head, err := runner.Run(ctx, repo.Path, "git", "rev-parse", "HEAD")
	if err != nil {
		return Status{}, err
	}
	dirty, err := runner.Run(ctx, repo.Path, "git", "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return Status{}, err
	}
	s := m.slot(id)
	out := Status{RepoID: id, RepoRevision: strings.TrimSpace(string(head)), WorkingTreeDirty: len(dirty) > 0, IndexState: "unknown"}
	s.mu.Lock()
	meta := s.meta
	verified := s.verified
	building := s.building
	c := s.client
	s.mu.Unlock()
	if meta.Revision != "" {
		v := meta.Revision
		out.IndexRevision = &v
		t := meta.IndexedAt
		out.IndexedAt = &t
	}
	if c != nil && c.Alive() {
		out.ToolSchemaFingerprint = c.Fingerprint()
	}
	switch {
	case !m.cfg.CodeGraph.Enabled:
		out.IndexState = "unavailable"
	case building:
		out.IndexState = "building"
	case c != nil && !c.Alive():
		out.IndexState = "unavailable"
	case meta.Revision == "" || !verified:
		out.IndexState = "unknown"
	case out.WorkingTreeDirty || meta.Revision != out.RepoRevision:
		out.IndexState = "stale"
	default:
		out.IndexState = "ready"
	}
	return out, nil
}
func (m *Manager) Refresh(ctx context.Context, id string) (Status, error) {
	repo, err := m.repos.Get(id)
	if err != nil {
		return Status{}, err
	}
	s := m.slot(id)
	s.indexMu.Lock()
	defer s.indexMu.Unlock()
	s.mu.Lock()
	s.building = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.building = false; s.mu.Unlock() }()
	before, err := m.Snapshot(ctx, id)
	if err != nil {
		return Status{}, err
	}
	tool, err := m.Tool(ctx, id, "codegraph_reindex_workspace")
	if err != nil {
		return Status{}, err
	}
	if len(tool.InputSchema.Required) > 0 {
		return Status{}, fmt.Errorf("%w: reindex tool requires unsupported input", ErrCapability)
	}
	if _, err = m.Call(ctx, id, "codegraph_reindex_workspace", map[string]any{}); err != nil {
		return Status{}, err
	}
	after, err := m.Snapshot(ctx, id)
	if err != nil {
		return Status{}, err
	}
	if before.RepoRevision != after.RepoRevision || before.WorkingTreeDirty || after.WorkingTreeDirty {
		return after, fmt.Errorf("index completed but worktree or HEAD changed; revision cannot be certified")
	}
	newMeta := metadata{Revision: after.RepoRevision, IndexedAt: time.Now().UTC()}
	dir := m.repoDataDir(repo.ID)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return Status{}, err
	}
	b, _ := json.Marshal(newMeta)
	temp, err := os.CreateTemp(dir, "index-*.json")
	if err != nil {
		return Status{}, err
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(b); err != nil {
		temp.Close()
		return Status{}, err
	}
	if err = temp.Sync(); err != nil {
		temp.Close()
		return Status{}, err
	}
	if err = temp.Close(); err != nil {
		return Status{}, err
	}
	if err = os.Rename(temp.Name(), filepath.Join(dir, "index.json")); err != nil {
		return Status{}, err
	}
	s.mu.Lock()
	s.meta = newMeta
	s.verified = true
	s.building = false
	s.mu.Unlock()
	return m.Snapshot(ctx, id)
}
func (m *Manager) RefreshOnStart() {
	if !m.cfg.CodeGraph.Enabled || !m.cfg.CodeGraph.ReindexOnStart {
		return
	}
	for _, repo := range m.repos.List() {
		id := repo.ID
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			if _, err := m.Refresh(ctx, id); err != nil {
				slog.Warn("CodeGraph startup index failed", "repo_id", id, "error_type", fmt.Sprintf("%T", err))
			}
		}()
	}
}
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	sessions := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.mu.Unlock()
	for _, s := range sessions {
		s.mu.Lock()
		c := s.client
		s.client = nil
		s.mu.Unlock()
		if c != nil {
			c.Close()
		}
	}
}
