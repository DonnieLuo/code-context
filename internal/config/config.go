package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Server struct {
		Listen           string        `json:"listen"`
		RequestTimeout   time.Duration `json:"request_timeout"`
		MaxResults       int           `json:"max_results"`
		MaxReadBytes     int64         `json:"max_read_bytes"`
		MaxDiffBytes     int64         `json:"max_diff_bytes"`
		MaxBatchRequests int           `json:"max_batch_requests"`
		MaxCallDepth     int           `json:"max_call_depth"`
	} `json:"server"`
	JDTLS struct {
		Command       string   `json:"command"`
		Args          []string `json:"args"`
		WorkspaceRoot string   `json:"workspace_root"`
	} `json:"jdtls"`
	Repositories map[string]Repository `json:"repositories"`
}
type Repository struct {
	Path string `json:"path"`
}

func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return LoadBytes(b)
}

// LoadBytes parses configuration embedded in the executable or supplied by a
// caller without requiring a configuration file at runtime.
func LoadBytes(b []byte) (Config, error) {
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		if c, err = parseYAMLSubset(b); err != nil {
			return Config{}, fmt.Errorf("parse config (JSON or supported YAML): %w", err)
		}
	}
	if c.Server.Listen == "" {
		c.Server.Listen = "127.0.0.1:8080"
	}
	if c.Server.RequestTimeout == 0 {
		c.Server.RequestTimeout = 30 * time.Second
	}
	if c.Server.MaxResults <= 0 {
		c.Server.MaxResults = 100
	}
	if c.Server.MaxReadBytes <= 0 {
		c.Server.MaxReadBytes = 2 << 20
	}
	if c.Server.MaxDiffBytes <= 0 {
		c.Server.MaxDiffBytes = 4 << 20
	}
	if c.Server.MaxBatchRequests <= 0 {
		c.Server.MaxBatchRequests = 20
	}
	if c.Server.MaxCallDepth <= 0 {
		c.Server.MaxCallDepth = 10
	}
	if c.JDTLS.WorkspaceRoot == "" {
		c.JDTLS.WorkspaceRoot = ".code-context/jdtls"
	}
	if len(c.Repositories) == 0 {
		return Config{}, fmt.Errorf("repositories must not be empty")
	}
	for id, repo := range c.Repositories {
		if id == "" || repo.Path == "" {
			return Config{}, fmt.Errorf("repository id and path are required")
		}
		real, err := filepath.EvalSymlinks(repo.Path)
		if err != nil {
			return Config{}, fmt.Errorf("repository %q: %w", id, err)
		}
		info, err := os.Stat(real)
		if err != nil || !info.IsDir() {
			return Config{}, fmt.Errorf("repository %q is not a directory", id)
		}
		repo.Path = real
		c.Repositories[id] = repo
	}
	return c, nil
}

// parseYAMLSubset accepts the documented configuration shape. JSON remains
// supported for generated configuration without adding runtime dependencies.
func parseYAMLSubset(b []byte) (Config, error) {
	var c Config
	c.Repositories = map[string]Repository{}
	section, currentRepo := "", ""
	s := bufio.NewScanner(strings.NewReader(string(b)))
	for lineNo := 1; s.Scan(); lineNo++ {
		raw := s.Text()
		line := strings.TrimSpace(strings.SplitN(raw, "#", 2)[0])
		if line == "" {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		if indent == 0 {
			if !strings.HasSuffix(line, ":") {
				return Config{}, fmt.Errorf("line %d: expected section", lineNo)
			}
			section = strings.TrimSuffix(line, ":")
			currentRepo = ""
			continue
		}
		if section == "repositories" && indent == 2 && strings.HasSuffix(line, ":") {
			currentRepo = strings.TrimSuffix(line, ":")
			c.Repositories[currentRepo] = Repository{}
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return Config{}, fmt.Errorf("line %d: expected key: value", lineNo)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), "\"'")
		switch section {
		case "server":
			switch key {
			case "listen":
				c.Server.Listen = value
			case "request_timeout":
				d, e := time.ParseDuration(value)
				if e != nil {
					return Config{}, e
				}
				c.Server.RequestTimeout = d
			case "max_results":
				n, e := strconv.Atoi(value)
				if e != nil {
					return Config{}, e
				}
				c.Server.MaxResults = n
			case "max_read_bytes":
				n, e := strconv.ParseInt(value, 10, 64)
				if e != nil {
					return Config{}, e
				}
				c.Server.MaxReadBytes = n
			case "max_diff_bytes":
				n, e := strconv.ParseInt(value, 10, 64)
				if e != nil {
					return Config{}, e
				}
				c.Server.MaxDiffBytes = n
			case "max_batch_requests":
				n, e := strconv.Atoi(value)
				if e != nil {
					return Config{}, e
				}
				c.Server.MaxBatchRequests = n
			case "max_call_depth":
				n, e := strconv.Atoi(value)
				if e != nil {
					return Config{}, e
				}
				c.Server.MaxCallDepth = n
			}
		case "jdtls":
			if key == "command" {
				c.JDTLS.Command = value
			} else if key == "workspace_root" {
				c.JDTLS.WorkspaceRoot = value
			}
		case "repositories":
			if currentRepo == "" || key != "path" {
				return Config{}, fmt.Errorf("line %d: unsupported repository setting", lineNo)
			}
			r := c.Repositories[currentRepo]
			r.Path = value
			c.Repositories[currentRepo] = r
		}
	}
	return c, s.Err()
}
