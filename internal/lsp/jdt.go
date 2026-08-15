package lsp

import (
	"context"
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
func (j *JDT) CallHierarchy(ctx context.Context, repoID, root, file string, p Position, incoming bool) ([]CallHierarchyItem, error) {
	c, err := j.ensureOpen(ctx, repoID, root, file)
	if err != nil {
		return nil, err
	}
	var items []CallHierarchyItem
	if err = c.Call(ctx, "textDocument/prepareCallHierarchy", map[string]any{"textDocument": map[string]string{"uri": fileURI(file)}, "position": p}, &items); err != nil {
		return nil, err
	}
	out := []CallHierarchyItem{}
	for _, item := range items {
		var edges []struct {
			From CallHierarchyItem `json:"from"`
			To   CallHierarchyItem `json:"to"`
		}
		method := "callHierarchy/incomingCalls"
		if !incoming {
			method = "callHierarchy/outgoingCalls"
		}
		if err = c.Call(ctx, method, item, &edges); err != nil {
			return nil, err
		}
		for _, edge := range edges {
			if incoming {
				out = append(out, edge.From)
			} else {
				out = append(out, edge.To)
			}
		}
	}
	return out, nil
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
