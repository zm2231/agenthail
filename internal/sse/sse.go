package sse

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var (
	keepalivePeriod = 15 * time.Second
	// writeWait bounds each write to a stream, so a client that stops
	// reading ends its stream instead of holding the handler forever.
	writeWait = time.Minute
)

func Cursor(r *http.Request) (uint64, error) {
	value := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if value == "" {
		value = strings.TrimSpace(r.URL.Query().Get("after"))
	}
	if value == "" {
		return 0, nil
	}
	return strconv.ParseUint(value, 10, 64)
}

func SetHeaders(header http.Header) {
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache, no-transform")
	header.Set("Connection", "keep-alive")
}

// Open writes a comment as soon as the stream starts. URLSession returns a
// streaming response only once body bytes arrive, so a client with nothing to
// replay would otherwise wait for the first keepalive.
func Open(w http.ResponseWriter) {
	SetHeaders(w.Header())
	ExtendWrite(w)
	fmt.Fprint(w, ": connected\n\n")
}

func ExtendWrite(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(writeWait))
}

// Flush sends what the stream has written and reports a client that is gone
// or did not take it before the write deadline.
func Flush(w http.ResponseWriter) error {
	return http.NewResponseController(w).Flush()
}

func Follow[T any](ctx context.Context, w http.ResponseWriter, wake <-chan T, onWake func(value T, open bool) bool, authorized func() bool) {
	keepalive := time.NewTicker(keepalivePeriod)
	defer keepalive.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case value, open := <-wake:
			ExtendWrite(w)
			if !onWake(value, open) {
				return
			}
		case <-keepalive.C:
			if !authorized() {
				return
			}
			ExtendWrite(w)
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil || Flush(w) != nil {
				return
			}
		}
	}
}
