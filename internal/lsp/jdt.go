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

type CallGraphEdge struct {
	From       CallHierarchyItem `json:"from"`
	To         CallHierarchyItem `json:"to"`
	FromRanges []Range           `json:"fromRanges"`
}

type TypeHierarchyItem struct {
	Name           string          `json:"name"`
	Kind           int             `json:"kind"`
	URI            string          `json:"uri"`
	Range          Range           `json:"range"`
	SelectionRange Range           `json:"selectionRange"`
	Data           json.RawMessage `json:"data,omitempty"`
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
	params := map[string]any{"processId": nil, "rootUri": rootURI, "workspaceFolders": []map[string]string{{"uri": rootURI, "name": repoID}}, "capabilities": map[string]any{"workspace": map[string]any{"workspaceFolders": true}, "textDocument": map[string]any{"definition": map[string]any{}, "implementation": map[string]any{}, "references": map[string]any{}, "callHierarchy": map[string]any{}, "typeHierarchy": map[string]any{}, "documentSymbol": map[string]any{}, "hover": map[string]any{}}}}
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

// Warm starts and initializes the language-server session before traffic is accepted.
func (j *JDT) Warm(ctx context.Context, repoID, root string) error {
	_, err := j.client(ctx, repoID, root)
	return err
}

func (j *JDT) TypeHierarchy(ctx context.Context, repoID, root, file string, p Position, direction string, depth, maxResults int) ([]TypeHierarchyItem, bool, error) {
	c, err := j.ensureOpen(ctx, repoID, root, file)
	if err != nil {
		return nil, false, err
	}
	var roots []TypeHierarchyItem
	if err = c.Call(ctx, "textDocument/prepareTypeHierarchy", map[string]any{"textDocument": map[string]string{"uri": fileURI(file)}, "position": p}, &roots); err != nil {
		return nil, false, fmt.Errorf("prepare type hierarchy: %w", err)
	}
	if depth <= 0 {
		depth = 1
	}
	if maxResults <= 0 {
		maxResults = 100
	}
	method := "typeHierarchy/subtypes"
	if direction == "supertypes" {
		method = "typeHierarchy/supertypes"
	}
	seen := map[string]bool{}
	frontier := roots
	out := []TypeHierarchyItem{}
	for level := 0; level < depth && len(frontier) > 0; level++ {
		next := []TypeHierarchyItem{}
		for _, item := range frontier {
			var related []TypeHierarchyItem
			if err := c.Call(ctx, method, map[string]any{"item": item}, &related); err != nil {
				return nil, false, err
			}
			for _, child := range related {
				key := fmt.Sprintf("%s:%d:%d", child.URI, child.SelectionRange.Start.Line, child.SelectionRange.Start.Character)
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, child)
				if len(out) >= maxResults {
					return out, true, nil
				}
				next = append(next, child)
			}
		}
		frontier = next
	}
	return out, false, nil
}

func (j *JDT) CallGraph(ctx context.Context, repoID, root, file string, p Position, direction string, depth, maxResults int) ([]CallGraphEdge, bool, error) {
	c, roots, err := j.callHierarchyRoots(ctx, repoID, root, file, p)
	if err != nil {
		return nil, false, err
	}
	if depth <= 0 {
		depth = 1
	}
	if maxResults <= 0 {
		maxResults = 100
	}
	seenNodes := map[string]bool{}
	frontier := roots
	out := []CallGraphEdge{}
	for level := 0; level < depth && len(frontier) > 0; level++ {
		next := []CallHierarchyItem{}
		for _, item := range frontier {
			var raw []struct {
				From       CallHierarchyItem `json:"from"`
				To         CallHierarchyItem `json:"to"`
				FromRanges []Range           `json:"fromRanges"`
			}
			method := "callHierarchy/outgoingCalls"
			if direction == "incoming" {
				method = "callHierarchy/incomingCalls"
			}
			if err := c.Call(ctx, method, map[string]any{"item": item}, &raw); err != nil {
				return nil, false, fmt.Errorf("%s: %w", method, err)
			}
			for _, edge := range raw {
				out = append(out, CallGraphEdge{From: edge.From, To: edge.To, FromRanges: edge.FromRanges})
				if len(out) >= maxResults {
					return out, true, nil
				}
				child := edge.To
				if direction == "incoming" {
					child = edge.From
				}
				key := callHierarchyKey(child)
				if !seenNodes[key] {
					seenNodes[key] = true
					next = append(next, child)
				}
			}
		}
		frontier = next
	}
	return out, false, nil
}

func (j *JDT) CallHierarchyRoots(ctx context.Context, repoID, root, file string, p Position) ([]CallHierarchyItem, error) {
	_, roots, err := j.callHierarchyRoots(ctx, repoID, root, file, p)
	return roots, err
}

func (j *JDT) callHierarchyRoots(ctx context.Context, repoID, root, file string, p Position) (*Client, []CallHierarchyItem, error) {
	c, err := j.ensureOpen(ctx, repoID, root, file)
	if err != nil {
		return nil, nil, err
	}
	var roots []CallHierarchyItem
	if err = c.Call(ctx, "textDocument/prepareCallHierarchy", map[string]any{"textDocument": map[string]string{"uri": fileURI(file)}, "position": p}, &roots); err != nil {
		return nil, nil, fmt.Errorf("prepare call hierarchy: %w", err)
	}
	return c, roots, nil
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

// Shutdown closes every active JDT LS session. Clients that do not honor the
// LSP shutdown request before ctx expires are forcibly terminated.
func (j *JDT) Shutdown(ctx context.Context) error {
	j.mu.Lock()
	clients := make([]*Client, 0, len(j.clients))
	for _, client := range j.clients {
		clients = append(clients, client)
	}
	j.clients = map[string]*Client{}
	j.opened = map[string]map[string]bool{}
	j.mu.Unlock()

	var first error
	for _, client := range clients {
		if err := client.Shutdown(ctx); err != nil && first == nil {
			first = err
		}
	}
	return first
}
func (j *JDT) Active(repoID string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.clients[repoID] != nil
}
func fileURI(path string) string { return (&url.URL{Scheme: "file", Path: path}).String() }
