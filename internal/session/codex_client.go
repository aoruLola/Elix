package session

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type rpcEnvelope struct {
	Method string          `json:"method,omitempty"`
	ID     any             `json:"id,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type rpcResult struct {
	result json.RawMessage
	err    *rpcError
}

type appServerClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	cancel context.CancelFunc

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]chan rpcResult
	closed  bool

	onNotification func(method string, params map[string]any)
	onRequest      func(idKey string, wireID any, method string, params map[string]any)
	onClose        func(error)
	onStderr       func(line string)
}

func newAppServerClient(bin string, args []string, workdir string) (*appServerClient, error) {
	childCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(childCtx, bin, args...)
	cmd.Dir = workdir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}

	c := &appServerClient{
		cmd:     cmd,
		stdin:   stdin,
		cancel:  cancel,
		pending: map[string]chan rpcResult{},
	}
	go c.readStdout(stdout)
	go c.readStderr(stderr)
	go c.waitExit()
	return c, nil
}

func (c *appServerClient) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := uuid.NewString()
	idKey := normalizeIDKey(id)
	ch := make(chan rpcResult, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("app-server client closed")
	}
	c.pending[idKey] = ch
	c.mu.Unlock()

	if err := c.writeEnvelope(rpcEnvelope{Method: method, ID: id, Params: mustMarshalRaw(params)}); err != nil {
		c.mu.Lock()
		delete(c.pending, idKey)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, idKey)
		c.mu.Unlock()
		return nil, ctx.Err()
	case out := <-ch:
		if out.err != nil {
			return nil, fmt.Errorf("rpc %s failed (%d): %s", method, out.err.Code, out.err.Message)
		}
		return out.result, nil
	}
}

func (c *appServerClient) Notify(method string, params any) error {
	return c.writeEnvelope(rpcEnvelope{Method: method, Params: mustMarshalRaw(params)})
}

func (c *appServerClient) ReplyResult(wireID any, result any) error {
	return c.writeEnvelope(rpcEnvelope{ID: wireID, Result: mustMarshalRaw(result)})
}

func (c *appServerClient) ReplyError(wireID any, code int, message string, data any) error {
	var raw json.RawMessage
	if data != nil {
		raw = mustMarshalRaw(data)
	}
	return c.writeEnvelope(rpcEnvelope{
		ID: wireID,
		Error: &rpcError{
			Code:    code,
			Message: message,
			Data:    raw,
		},
	})
}

func (c *appServerClient) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	for key, ch := range c.pending {
		delete(c.pending, key)
		ch <- rpcResult{err: &rpcError{Code: -1, Message: "client closed"}}
		close(ch)
	}
	c.mu.Unlock()

	c.cancel()
	_ = c.stdin.Close()
	return nil
}

func (c *appServerClient) writeEnvelope(env rpcEnvelope) error {
	payload, err := json.Marshal(env)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.stdin.Write(payload); err != nil {
		return err
	}
	if _, err := c.stdin.Write([]byte("\n")); err != nil {
		return err
	}
	return nil
}

func (c *appServerClient) waitExit() {
	err := c.cmd.Wait()
	if c.onClose != nil {
		c.onClose(err)
	}
	_ = c.Close()
}

func (c *appServerClient) readStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 128*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if c.onStderr != nil {
			c.onStderr(line)
		}
	}
}

func (c *appServerClient) readStdout(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 128*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			if c.onStderr != nil {
				c.onStderr("invalid json-rpc line: " + line)
			}
			continue
		}

		methodRaw, hasMethod := raw["method"]
		idRaw, hasID := raw["id"]
		if hasMethod {
			var method string
			_ = json.Unmarshal(methodRaw, &method)
			params := map[string]any{}
			if paramsRaw, ok := raw["params"]; ok && len(paramsRaw) > 0 {
				_ = json.Unmarshal(paramsRaw, &params)
			}
			if hasID {
				wireID := unmarshalWireID(idRaw)
				if c.onRequest != nil {
					c.onRequest(normalizeIDKey(wireID), wireID, method, params)
				}
			} else if c.onNotification != nil {
				c.onNotification(method, params)
			}
			continue
		}

		if hasID {
			wireID := unmarshalWireID(idRaw)
			idKey := normalizeIDKey(wireID)
			var out rpcResult
			if resultRaw, ok := raw["result"]; ok {
				out.result = resultRaw
			}
			if errRaw, ok := raw["error"]; ok {
				var rpcErr rpcError
				if err := json.Unmarshal(errRaw, &rpcErr); err == nil {
					out.err = &rpcErr
				} else {
					out.err = &rpcError{Code: -1, Message: string(errRaw)}
				}
			}
			c.mu.Lock()
			ch, ok := c.pending[idKey]
			if ok {
				delete(c.pending, idKey)
			}
			c.mu.Unlock()
			if ok {
				ch <- out
				close(ch)
			}
		}
	}
}

func mustMarshalRaw(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}

func unmarshalWireID(raw json.RawMessage) any {
	var id any
	_ = json.Unmarshal(raw, &id)
	return id
}

func normalizeIDKey(id any) string {
	switch v := id.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return fmt.Sprintf("%g", v)
	case int:
		return fmt.Sprintf("%d", v)
	case int64:
		return fmt.Sprintf("%d", v)
	case json.Number:
		return v.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

func decodeResultField(raw json.RawMessage, path ...string) string {
	if len(raw) == 0 {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	var cur any = obj
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[key]
	}
	s, _ := cur.(string)
	return s
}

func requestTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}
