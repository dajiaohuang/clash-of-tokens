// Package browserbidi implements bounded WebDriver BiDi operations for owned
// Firefox profiles. It never closes pre-existing tabs or the browser itself.
package browserbidi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"sync"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

const maxMessage = 2 << 20

type Client struct {
	conn         net.Conn
	reader       *wsutil.Reader
	mu           sync.Mutex
	writeMu      sync.Mutex
	lifecycleMu  sync.Mutex
	closed       bool
	id           uint64
	owned        map[string]bool
	userContexts []string
	tabUsers     map[string]string
	pending      map[uint64]chan wireResponse
	done         chan struct{}
	release      func()
	closeOnce    sync.Once
}

type wireResponse struct {
	ID     uint64          `json:"id"`
	Type   string          `json:"type"`
	Result json.RawMessage `json:"result"`
}

var profiles = struct {
	sync.Mutex
	active map[string]bool
}{active: map[string]bool{}}

func Connect(ctx context.Context, endpoint string) (*Client, error) {
	// Firefox permits one BiDi session. Keep control checks and generation from
	// racing on the same configured profile, including across generations.
	u, err := url.Parse(endpoint)
	if err != nil || u.Port() == "" {
		return nil, errors.New("invalid Firefox endpoint")
	}
	key := u.Port()
	profiles.Lock()
	if profiles.active[key] {
		profiles.Unlock()
		return nil, errors.New("Firefox profile already has an active operation")
	}
	profiles.active[key] = true
	profiles.Unlock()
	release := func() { profiles.Lock(); delete(profiles.active, key); profiles.Unlock() }
	client, err := connect(ctx, endpoint)
	if err != nil {
		release()
		return nil, err
	}
	client.release = release
	return client, nil
}

func connect(ctx context.Context, endpoint string) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() || u.Port() == "" {
		return nil, errors.New("BiDi requires an explicit loopback HTTP endpoint")
	}
	u.Scheme = "ws"
	u.Path = "/session"
	d := ws.Dialer{Timeout: 5 * time.Second}
	conn, buffer, _, err := d.Dial(ctx, u.String())
	if err != nil {
		return nil, errors.New("Firefox BiDi connection unavailable")
	}
	var input io.Reader = conn
	if buffer != nil {
		input = buffer
	}
	client := &Client{conn: conn, owned: map[string]bool{}, pending: map[uint64]chan wireResponse{}, done: make(chan struct{}), reader: &wsutil.Reader{Source: input, State: ws.StateClientSide, CheckUTF8: true, MaxFrameSize: maxMessage}}
	go client.readLoop()
	creation, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer stop()
	if err = client.Call(creation, "session.new", map[string]any{"capabilities": map[string]any{}}, nil); err != nil {
		conn.Close()
		return nil, err
	}
	if ctx.Err() != nil {
		client.Close()
		return nil, ctx.Err()
	}
	return client, nil
}
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		c.lifecycleMu.Lock()
		defer c.lifecycleMu.Unlock()
		c.closed = true
		defer func() {
			if c.release != nil {
				c.release()
			}
		}()
		c.mu.Lock()
		ids := make([]string, 0, len(c.owned))
		for id := range c.owned {
			ids = append(ids, id)
		}
		c.mu.Unlock()
		for _, id := range ids {
			c.CloseTab(id)
		}
		for _, id := range c.userContexts {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = c.Call(ctx, "browser.removeUserContext", map[string]any{"userContext": id}, nil)
			cancel()
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = c.Call(ctx, "session.end", map[string]any{}, nil)
		c.conn.Close()
		<-c.done
	})
}
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	c.mu.Lock()
	if len(c.pending) >= 32 {
		c.mu.Unlock()
		return errors.New("BiDi pending command capacity exhausted")
	}
	c.id++
	id := c.id
	response := make(chan wireResponse, 1)
	c.pending[id] = response
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	body, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil || len(body) > maxMessage {
		return errors.New("BiDi command exceeds limit")
	}
	c.writeMu.Lock()
	deadline, _ := ctx.Deadline()
	c.conn.SetWriteDeadline(minTime(deadline, time.Now().Add(2*time.Second)))
	err = wsutil.WriteClientText(c.conn, body)
	c.writeMu.Unlock()
	if err != nil {
		return errors.New("BiDi command transport failed")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case value := <-response:
		if value.Type != "success" {
			return errors.New("Firefox rejected BiDi operation")
		}
		if out != nil && json.Unmarshal(value.Result, out) != nil {
			return errors.New("invalid BiDi result")
		}
		return nil
	case <-c.done:
		return errors.New("Firefox connection closed")
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// A dedicated bounded reader lets canceled calls return without disconnecting
// the session. Cleanup commands can still close the owned tab and end the
// session while Firefox is evaluating an abandoned asynchronous script.
func (c *Client) readLoop() {
	defer close(c.done)
	defer c.conn.Close()
	for events := 0; events < 1024; {
		header, err := c.reader.NextFrame()
		if err != nil {
			return
		}
		raw, err := io.ReadAll(io.LimitReader(c.reader, maxMessage+1))
		if err != nil || len(raw) > maxMessage {
			return
		}
		if header.OpCode == ws.OpPing {
			c.writeMu.Lock()
			c.conn.SetWriteDeadline(time.Now().Add(time.Second))
			_ = wsutil.WriteClientMessage(c.conn, ws.OpPong, raw)
			c.writeMu.Unlock()
			events++
			continue
		}
		if header.OpCode == ws.OpClose {
			return
		}
		if header.OpCode != ws.OpText {
			events++
			continue
		}
		var response wireResponse
		if json.Unmarshal(raw, &response) != nil {
			return
		}
		c.mu.Lock()
		waiting := c.pending[response.ID]
		delete(c.pending, response.ID)
		c.mu.Unlock()
		if waiting == nil {
			events++
			continue
		}
		events = 0
		waiting <- response
	}
}
func (c *Client) NewTab(ctx context.Context, private ...bool) (string, error) {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if c.closed || ctx.Err() != nil {
		return "", errors.New("Firefox session is closed or canceled")
	}
	c.mu.Lock()
	full := len(c.owned) >= 32 || len(c.userContexts) >= 32
	c.mu.Unlock()
	if full {
		return "", errors.New("Firefox owned context capacity exhausted")
	}
	var result struct {
		Context string `json:"context"`
	}
	creation, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	params := map[string]any{"type": "tab", "background": true}
	if len(private) > 0 && private[0] {
		var user struct {
			ID string `json:"userContext"`
		}
		if err := c.Call(creation, "browser.createUserContext", map[string]any{}, &user); err != nil {
			return "", err
		}
		if user.ID == "" {
			return "", errors.New("Firefox did not create an isolated user context")
		}
		c.mu.Lock()
		c.userContexts = append(c.userContexts, user.ID)
		c.mu.Unlock()
		params["userContext"] = user.ID
	}
	err := c.Call(creation, "browsingContext.create", params, &result)
	if result.Context == "" && err == nil {
		err = errors.New("Firefox did not create a tab")
	}
	if err == nil {
		c.mu.Lock()
		c.owned[result.Context] = true
		if c.tabUsers == nil {
			c.tabUsers = map[string]string{}
		}
		user, _ := params["userContext"].(string)
		if user == "" {
			user = "default"
		}
		c.tabUsers[result.Context] = user
		c.mu.Unlock()
		if ctx.Err() != nil {
			c.CloseTab(result.Context)
			return "", ctx.Err()
		}
	}
	return result.Context, err
}

func (c *Client) SetCookie(ctx context.Context, id string, cookie map[string]any) error {
	c.mu.Lock()
	user := c.tabUsers[id]
	c.mu.Unlock()
	if user == "" {
		return errors.New("cookie target is not an owned tab")
	}
	return c.Call(ctx, "storage.setCookie", map[string]any{"cookie": cookie, "partition": map[string]string{"type": "storageKey", "userContext": user}}, nil)
}
func (c *Client) CloseTab(id string) {
	c.mu.Lock()
	owned := c.owned[id]
	c.mu.Unlock()
	if !owned {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if c.Call(ctx, "browsingContext.close", map[string]any{"context": id}, nil) == nil {
		c.mu.Lock()
		delete(c.owned, id)
		delete(c.tabUsers, id)
		c.mu.Unlock()
	}
}
func (c *Client) Navigate(ctx context.Context, id, destination string) error {
	return c.Call(ctx, "browsingContext.navigate", map[string]any{"context": id, "url": destination, "wait": "complete"}, nil)
}

// EvaluateJSON serializes inside the page to avoid lossy RemoteValue conversion.
func (c *Client) EvaluateJSON(ctx context.Context, id, expression string, out any) error {
	var response struct {
		Type   string `json:"type"`
		Result struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"result"`
	}
	err := c.Call(ctx, "script.evaluate", map[string]any{"expression": "(async()=>{const value=await (" + expression + ");return JSON.stringify(value===undefined?null:value)})()", "target": map[string]string{"context": id}, "awaitPromise": true, "resultOwnership": "none"}, &response)
	if err != nil {
		return err
	}
	if response.Type != "success" || response.Result.Type != "string" || (out != nil && json.Unmarshal([]byte(response.Result.Value), out) != nil) {
		return errors.New("Firefox script result unavailable")
	}
	return nil
}
