package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
)

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}
type Symbol struct {
	Name          string   `json:"name"`
	Kind          int      `json:"kind"`
	Location      Location `json:"location"`
	ContainerName string   `json:"containerName"`
}
type CallHierarchyItem struct {
	Name           string `json:"name"`
	Kind           int    `json:"kind"`
	URI            string `json:"uri"`
	Range          Range  `json:"range"`
	SelectionRange Range  `json:"selectionRange"`
	// JDT LS puts an opaque handle here; it must be sent back unchanged in
	// incomingCalls/outgoingCalls requests.
	Data  json.RawMessage `json:"data,omitempty"`
	Depth int             `json:"-"`
}

type JDT struct {
	command   string
	args      []string
	workspace string
	clients   map[string]*Client
	opened    map[string]map[string]bool
	mu        sync.Mutex
}

func NewJDT(command string, args []string, workspace string) *JDT {
	return &JDT{command: command, args: args, workspace: workspace, clients: map[string]*Client{}, opened: map[string]map[string]bool{}}
}

func (j *JDT) client(ctx context.Context, repoID, root string) (*Client, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if c := j.clients[repoID]; c != nil {
		return c, nil
	}
	if j.command == "" {
		return nil, fmt.Errorf("jdtls command is not configured")
	}
	data := filepath.Join(j.workspace, repoID)
	if err := os.MkdirAll(data, 0755); err != nil {
		return nil, err
	}
	args := append([]string{}, j.args...)
	args = append(args, "-data", data)
	c, err := Start(j.command, args...)
	if err != nil {
		return nil, err
	}
	rootURI := fileURI(root)
	params := map[string]any{"processId": nil, "rootUri": rootURI, "workspaceFolders": []map[string]string{{"uri": rootURI, "name": repoID}}, "capabilities": map[string]any{"workspace": map[string]any{"workspaceFolders": true}, "textDocument": map[string]any{"definition": map[string]any{}, "implementation": map[string]any{}, "references": map[string]any{}, "callHierarchy": map[string]any{}, "documentSymbol": map[string]any{}, "hover": map[string]any{}}}}
	var initialized map[string]any
	if err := c.Call(ctx, "initialize", params, &initialized); err != nil {
		_ = c.Close()
		return nil, err
	}
	if err := c.Notify("initialized", map[string]any{}); err != nil {
		_ = c.Close()
		return nil, err
	}
	j.clients[repoID] = c
	j.opened[repoID] = map[string]bool{}
	return c, nil
}
func (j *JDT) ensureOpen(ctx context.Context, repoID, root, file string) (*Client, error) {
	c, err := j.client(ctx, repoID, root)
	if err != nil {
		return nil, err
	}
	j.mu.Lock()
	opened := j.opened[repoID][file]
	if !opened {
		j.opened[repoID][file] = true
	}
	j.mu.Unlock()
	if !opened {
		text, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		if err := c.Notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": fileURI(file), "languageId": "java", "version": 1, "text": string(text)}}); err != nil {
			return nil, err
		}
	}
	return c, nil
}
func (j *JDT) Locations(ctx context.Context, repoID, root, file, method string, p Position) ([]Location, error) {
	c, err := j.ensureOpen(ctx, repoID, root, file)
	if err != nil {
		return nil, err
	}
	var out []Location
	err = c.Call(ctx, method, map[string]any{"textDocument": map[string]string{"uri": fileURI(file)}, "position": p, "context": map[string]bool{"includeDeclaration": true}}, &out)
	return out, err
}
func (j *JDT) Symbols(ctx context.Context, repoID, root, query string) ([]Symbol, error) {
	c, err := j.client(ctx, repoID, root)
	if err != nil {
		return nil, err
	}
	var out []Symbol
	err = c.Call(ctx, "workspace/symbol", map[string]string{"query": query}, &out)
	return out, err
}
func (j *JDT) Hover(ctx context.Context, repoID, root, file string, p Position) (map[string]any, error) {
	c, err := j.ensureOpen(ctx, repoID, root, file)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = c.Call(ctx, "textDocument/hover", map[string]any{"textDocument": map[string]string{"uri": fileURI(file)}, "position": p}, &out)
	return out, err
}
func (j *JDT) FileSymbols(ctx context.Context, repoID, root, file string) (any, error) {
	c, err := j.ensureOpen(ctx, repoID, root, file)
	if err != nil {
		return nil, err
	}
	var out any
	err = c.Call(ctx, "textDocument/documentSymbol", map[string]any{"textDocument": map[string]string{"uri": fileURI(file)}}, &out)
	return out, err
}
func (j *JDT) CallHierarchy(ctx context.Context, repoID, root, file string, p Position, incoming bool, depth, maxResults int) ([]CallHierarchyItem, bool, error) {
	c, err := j.ensureOpen(ctx, repoID, root, file)
	if err != nil {
		return nil, false, err
	}
	var items []CallHierarchyItem
	if err = c.Call(ctx, "textDocument/prepareCallHierarchy", map[string]any{"textDocument": map[string]string{"uri": fileURI(file)}, "position": p}, &items); err != nil {
		return nil, false, fmt.Errorf("prepare call hierarchy: %w", err)
	}
	return traverseCallHierarchy(ctx, items, depth, maxResults, func(item CallHierarchyItem) ([]CallHierarchyItem, error) {
		var edges []struct {
			From CallHierarchyItem `json:"from"`
			To   CallHierarchyItem `json:"to"`
		}
		method := "callHierarchy/incomingCalls"
		if !incoming {
			method = "callHierarchy/outgoingCalls"
		}
		if err = c.Call(ctx, method, map[string]any{"item": item}, &edges); err != nil {
			return nil, fmt.Errorf("%s: %w", method, err)
		}
		related := make([]CallHierarchyItem, 0, len(edges))
		for _, edge := range edges {
			if incoming {
				related = append(related, edge.From)
			} else {
				related = append(related, edge.To)
			}
		}
		return related, nil
	})
}

func traverseCallHierarchy(ctx context.Context, roots []CallHierarchyItem, depth, maxResults int, related func(CallHierarchyItem) ([]CallHierarchyItem, error)) ([]CallHierarchyItem, bool, error) {
	if depth <= 0 {
		depth = 1
	}
	if maxResults <= 0 {
		maxResults = 100
	}
	seen := make(map[string]bool, len(roots))
	frontier := make([]CallHierarchyItem, 0, len(roots))
	for _, root := range roots {
		root.Depth = 0
		key := callHierarchyKey(root)
		if !seen[key] {
			seen[key] = true
			frontier = append(frontier, root)
		}
	}
	out := []CallHierarchyItem{}
	for level := 1; level <= depth && len(frontier) > 0; level++ {
		next := []CallHierarchyItem{}
		for _, item := range frontier {
			edges, err := related(item)
			if err != nil {
				return nil, false, err
			}
			for _, edge := range edges {
				key := callHierarchyKey(edge)
				if seen[key] {
					continue
				}
				seen[key] = true
				edge.Depth = level
				out = append(out, edge)
				if len(out) >= maxResults {
					return out, true, nil
				}
				if level < depth {
					next = append(next, edge)
				}
			}
		}
		frontier = next
	}
	return out, false, nil
}

func callHierarchyKey(item CallHierarchyItem) string {
	r := item.SelectionRange
	return fmt.Sprintf("%s:%d:%d:%d:%d", item.URI, r.Start.Line, r.Start.Character, r.End.Line, r.End.Character)
}
func (j *JDT) Refresh(repoID string) {
	j.mu.Lock()
	c := j.clients[repoID]
	delete(j.clients, repoID)
	delete(j.opened, repoID)
	j.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}
func (j *JDT) Active(repoID string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.clients[repoID] != nil
}
func fileURI(path string) string { return (&url.URL{Scheme: "file", Path: path}).String() }
