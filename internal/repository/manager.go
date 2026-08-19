package repository

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/example/code-context/internal/config"
)

type Repository struct{ ID, Path string }
type Manager struct {
	repos map[string]Repository
	mu    sync.RWMutex
}

func New(cfg config.Config) *Manager {
	m := &Manager{repos: make(map[string]Repository, len(cfg.Repositories))}
	for id, repo := range cfg.Repositories {
		m.repos[id] = Repository{ID: id, Path: repo.Path}
	}
	return m
}
func (m *Manager) Get(id string) (Repository, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.repos[id]
	if !ok {
		return Repository{}, fmt.Errorf("unknown repository %q", id)
	}
	return r, nil
}

// List returns the repositories configured for this service.
func (m *Manager) List() []Repository {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Repository, 0, len(m.repos))
	for _, repo := range m.repos {
		out = append(out, repo)
	}
	return out
}

// File resolves a user-provided repository-relative path and rejects every escape attempt.
func (m *Manager) File(repo Repository, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return "", fmt.Errorf("path must be repository-relative")
	}
	clean := filepath.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes repository")
	}
	candidate := filepath.Join(repo.Path, clean)
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(repo.Path, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes repository")
	}
	if _, err := os.Stat(real); err != nil {
		return "", err
	}
	return real, nil
}
