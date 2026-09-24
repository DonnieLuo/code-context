package codegraph

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// MCP stdio uses one JSON-RPC message per line. Only the request surface needed
// by this service is implemented; unknown server notifications are ignored.
type Client struct {
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	mu          sync.Mutex
	writeMu     sync.Mutex
	pending     map[int64]chan rpcMessage
	done        chan struct{}
	closed      atomic.Bool
	seq         atomic.Int64
	tools       map[string]Tool
	fingerprint string
}
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  any             `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
type Tool struct {
	Name        string `json:"name"`
	InputSchema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	} `json:"inputSchema"`
}
type CallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent"`
	IsError           bool            `json:"isError"`
}

func Start(ctx context.Context, command string, args []string, repoPath, dataDir string, telemetry bool) (*Client, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}
	home, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(command, args...)
	cmd.Dir = repoPath
	cmd.Env = append(os.Environ(), "HOME="+home, "XDG_DATA_HOME="+filepath.Join(home, "xdg"))
	if !telemetry {
		cmd.Env = append(cmd.Env, "CODEGRAPH_TELEMETRY=off")
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	c := &Client{cmd: cmd, stdin: stdin, pending: map[int64]chan rpcMessage{}, done: make(chan struct{}), tools: map[string]Tool{}}
	go c.read(stdout)
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	go func() { _ = cmd.Wait(); c.failPending(); close(c.done) }()
	var init json.RawMessage
	if err = c.request(ctx, "initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "code-context", "version": "2"}}, &init); err != nil {
		c.Close()
		return nil, fmt.Errorf("initialize: %w", err)
	}
	if err = c.notify("notifications/initialized", map[string]any{}); err != nil {
		c.Close()
		return nil, err
	}
	var listed struct {
		Tools      []Tool `json:"tools"`
		NextCursor string `json:"nextCursor"`
	}
	cursor := ""
	for {
		args := map[string]any{}
		if cursor != "" {
			args["cursor"] = cursor
		}
		if err = c.request(ctx, "tools/list", args, &listed); err != nil {
			c.Close()
			return nil, fmt.Errorf("tools/list: %w", err)
		}
		for _, tool := range listed.Tools {
			c.tools[tool.Name] = tool
		}
		if listed.NextCursor == "" {
			break
		}
		cursor = listed.NextCursor
	}
	b, _ := json.Marshal(c.tools)
	// This fingerprint describes the effective schemas, not the binary version.
	c.fingerprint = fingerprint(b)
	return c, nil
}
func (c *Client) read(r io.Reader) {
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 4096), 32<<20)
	for scan.Scan() {
		var msg rpcMessage
		if json.Unmarshal(scan.Bytes(), &msg) != nil || len(msg.ID) == 0 {
			continue
		}
		var id int64
		if json.Unmarshal(msg.ID, &id) != nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch != nil {
			ch <- msg
		}
	}
	c.failPending()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
}
func (c *Client) failPending() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed.Store(true)
	for id, ch := range c.pending {
		delete(c.pending, id)
		close(ch)
	}
}
func (c *Client) send(v any) error {
	if c.closed.Load() {
		return errors.New("codegraph connection closed")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}
func (c *Client) notify(method string, params any) error {
	return c.send(rpcMessage{JSONRPC: "2.0", Method: method, Params: params})
}
func (c *Client) request(ctx context.Context, method string, params any, out any) error {
	id := c.seq.Add(1)
	ch := make(chan rpcMessage, 1)
	c.mu.Lock()
	if c.closed.Load() {
		c.mu.Unlock()
		return errors.New("codegraph connection closed")
	}
	c.pending[id] = ch
	c.mu.Unlock()
	err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case msg, ok := <-ch:
		if !ok {
			return errors.New("codegraph connection closed")
		}
		if msg.Error != nil {
			return fmt.Errorf("mcp %d: %s", msg.Error.Code, msg.Error.Message)
		}
		if out != nil {
			return json.Unmarshal(msg.Result, out)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		go func() {
			_ = c.notify("notifications/cancelled", map[string]any{"requestId": id, "reason": "request cancelled"})
		}()
		return ctx.Err()
	}
}
func (c *Client) Tools() map[string]Tool {
	out := make(map[string]Tool, len(c.tools))
	for k, v := range c.tools {
		out[k] = v
	}
	return out
}
func (c *Client) Fingerprint() string { return c.fingerprint }
func (c *Client) Alive() bool         { return !c.closed.Load() }
func (c *Client) Call(ctx context.Context, name string, args map[string]any) (CallResult, error) {
	if _, ok := c.tools[name]; !ok {
		return CallResult{}, fmt.Errorf("unsupported capability: %s", name)
	}
	var out CallResult
	err := c.request(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, &out)
	if err != nil {
		return out, err
	}
	if out.IsError {
		if len(out.Content) > 0 {
			return out, fmt.Errorf("codegraph tool error: %s", out.Content[0].Text)
		}
		return out, errors.New("codegraph tool error")
	}
	return out, nil
}
func (c *Client) Close() {
	c.closed.Store(true)
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
	}
}
