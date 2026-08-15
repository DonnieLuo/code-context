package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

type Client struct {
	cmd       *exec.Cmd
	in        io.WriteCloser
	out       *bufio.Reader
	mu        sync.Mutex
	pending   map[int64]chan response
	next      atomic.Int64
	closeOnce sync.Once
}
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type response struct {
	result json.RawMessage
	err    error
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func Start(command string, args ...string) (*Client, error) {
	cmd := exec.Command(command, args...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &Client{cmd: cmd, in: in, out: bufio.NewReader(out), pending: map[int64]chan response{}}
	go c.readLoop()
	go func() { _ = cmd.Wait(); c.failAll(fmt.Errorf("language server exited")) }()
	return c, nil
}
func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	id := c.next.Add(1)
	ch := make(chan response, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.write(message{JSONRPC: "2.0", ID: json.RawMessage(strconv.FormatInt(id, 10)), Method: method, Params: mustJSON(params)}); err != nil {
		c.remove(id)
		return err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if result != nil {
			return json.Unmarshal(r.result, result)
		}
		return nil
	case <-ctx.Done():
		c.remove(id)
		_ = c.Notify("$/cancelRequest", map[string]any{"id": id})
		return ctx.Err()
	}
}
func (c *Client) Notify(method string, params any) error {
	return c.write(message{JSONRPC: "2.0", Method: method, Params: mustJSON(params)})
}
func (c *Client) Close() (err error) { c.closeOnce.Do(func() { err = c.in.Close() }); return err }
func (c *Client) write(v message) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = fmt.Fprintf(c.in, "Content-Length: %d\r\n\r\n%s", len(b), b)
	return err
}
func (c *Client) readLoop() {
	for {
		b, err := readFrame(c.out)
		if err != nil {
			c.failAll(err)
			return
		}
		var m message
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		if len(m.ID) > 0 && m.Method == "" {
			id, _ := strconv.ParseInt(string(m.ID), 10, 64)
			c.mu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if ch != nil {
				if m.Error != nil {
					ch <- response{err: fmt.Errorf("lsp %d: %s", m.Error.Code, m.Error.Message)}
				} else {
					ch <- response{result: m.Result}
				}
			}
			continue
		}
		if len(m.ID) > 0 && m.Method != "" {
			_ = c.write(message{JSONRPC: "2.0", ID: m.ID, Result: json.RawMessage("null")})
		}
	}
}
func (c *Client) remove(id int64) { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }
func (c *Client) failAll(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.pending {
		ch <- response{err: err}
		delete(c.pending, id)
	}
}
func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func readFrame(r *bufio.Reader) ([]byte, error) {
	n := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			n, _ = strconv.Atoi(strings.TrimSpace(strings.SplitN(line, ":", 2)[1]))
		}
	}
	if n <= 0 {
		return nil, fmt.Errorf("invalid LSP frame")
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}
