package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

const maxLine = 64 << 20

var ErrClosed = errors.New("the agent connection closed")

type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("agent error %d: %s", e.Code, e.Message) }

const (
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603
	CodeAuthRequired   = -32000
)

type Handler interface {
	Request(ctx context.Context, method string, params json.RawMessage) (any, error)
	Notification(method string, params json.RawMessage)
}

type Receiver interface {
	Received(method string, params json.RawMessage)
}

type Acknowledged struct {
	Result  any
	Written func(error)
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type reply struct {
	result json.RawMessage
	err    error
}

type Conn struct {
	r       io.Reader
	w       io.Writer
	handler Handler
	writeMu sync.Mutex

	mu      sync.Mutex
	next    int64
	waiting map[string]chan reply
	err     error
	done    chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
}

func NewConn(r io.Reader, w io.Writer, handler Handler) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	return &Conn{r: r, w: w, handler: handler, waiting: map[string]chan reply{}, done: make(chan struct{}), ctx: ctx, cancel: cancel}
}

func (c *Conn) Start() { go c.read(c.r) }

func (c *Conn) Done() <-chan struct{} { return c.done }

func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Conn) Request(ctx context.Context, method string, params, result any) error {
	pending, err := c.Send(method, params)
	if err != nil {
		return err
	}
	return pending.Wait(ctx, result)
}

type Pending struct {
	conn *Conn
	id   string
	wait chan reply
}

func (c *Conn) Send(method string, params any) (*Pending, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return nil, err
	}
	c.next++
	id := strconv.FormatInt(c.next, 10)
	wait := make(chan reply, 1)
	c.waiting[id] = wait
	c.mu.Unlock()
	if err := c.write(message{JSONRPC: "2.0", ID: json.RawMessage(id), Method: method, Params: raw}); err != nil {
		c.forget(id)
		return nil, err
	}
	return &Pending{conn: c, id: id, wait: wait}, nil
}

func (p *Pending) Wait(ctx context.Context, result any) error {
	select {
	case got := <-p.wait:
		if got.err != nil {
			return got.err
		}
		if result == nil || len(got.result) == 0 || string(got.result) == "null" {
			return nil
		}
		return json.Unmarshal(got.result, result)
	case <-ctx.Done():
		p.conn.forget(p.id)
		return ctx.Err()
	}
}

func (c *Conn) Notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.write(message{JSONRPC: "2.0", Method: method, Params: raw})
}

func (c *Conn) forget(id string) {
	c.mu.Lock()
	delete(c.waiting, id)
	c.mu.Unlock()
}

func (c *Conn) write(m message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.Err(); err != nil {
		return err
	}
	_, err = c.w.Write(data)
	return err
}

func (c *Conn) read(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64<<10), maxLine)
	for scanner.Scan() {
		var m message
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			continue
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			if receiver, ok := c.handler.(Receiver); ok {
				receiver.Received(m.Method, m.Params)
			}
			go c.serve(m)
		case m.Method != "":
			if c.handler != nil {
				c.handler.Notification(m.Method, m.Params)
			}
		case len(m.ID) > 0:
			c.deliver(m)
		}
	}
	err := scanner.Err()
	if err == nil {
		err = ErrClosed
	} else {
		err = fmt.Errorf("%w: %v", ErrClosed, err)
	}
	c.close(err)
}

func (c *Conn) deliver(m message) {
	key := idKey(m.ID)
	c.mu.Lock()
	wait := c.waiting[key]
	delete(c.waiting, key)
	c.mu.Unlock()
	if wait == nil {
		return
	}
	if m.Error != nil {
		wait <- reply{err: m.Error}
		return
	}
	wait <- reply{result: m.Result}
}

func (c *Conn) serve(m message) {
	var result any
	err := &Error{Code: CodeMethodNotFound, Message: "method not found"}
	if c.handler != nil {
		value, handlerErr := c.handler.Request(c.ctx, m.Method, m.Params)
		result, err = value, nil
		if handlerErr != nil {
			var typed *Error
			if !errors.As(handlerErr, &typed) {
				typed = &Error{Code: CodeInternal, Message: handlerErr.Error()}
			}
			err = typed
		}
	}
	written := func(error) {}
	if acknowledged, ok := result.(Acknowledged); ok {
		result, written = acknowledged.Result, acknowledged.Written
	}
	if err != nil {
		_ = c.write(message{JSONRPC: "2.0", ID: m.ID, Error: err})
		return
	}
	raw, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		written(marshalErr)
		_ = c.write(message{JSONRPC: "2.0", ID: m.ID, Error: &Error{Code: CodeInternal, Message: marshalErr.Error()}})
		return
	}
	written(c.write(message{JSONRPC: "2.0", ID: m.ID, Result: raw}))
}

func (c *Conn) close(err error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	c.err = err
	waiting := c.waiting
	c.waiting = map[string]chan reply{}
	c.mu.Unlock()
	c.cancel()
	for _, wait := range waiting {
		wait <- reply{err: err}
	}
	close(c.done)
}

func idKey(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return string(raw)
}
