// Package unixjson sends one JSON request over a Unix socket and reads one
// JSON reply.
package unixjson

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"time"
)

// NotSentError reports a failure before any byte of the request was written.
type NotSentError struct{ Err error }

func (e *NotSentError) Error() string { return e.Err.Error() }
func (e *NotSentError) Unwrap() error { return e.Err }

// Call gives up after timeout, or earlier at ctx's deadline. Any error other
// than a NotSentError may come after the request was written.
func Call(ctx context.Context, path string, timeout time.Duration, replyLimit int64, request, reply any) error {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return &NotSentError{Err: err}
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline := time.Now().Add(timeout)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return &NotSentError{Err: err}
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return err
	}
	return json.NewDecoder(io.LimitReader(conn, replyLimit)).Decode(reply)
}
