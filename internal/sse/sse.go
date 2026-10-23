package sse

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const KeepalivePeriod = 15 * time.Second

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
	fmt.Fprint(w, ": connected\n\n")
}

func Follow[T any](ctx context.Context, w http.ResponseWriter, flusher http.Flusher, wake <-chan T, onWake func(value T, open bool) bool, authorized func() bool) {
	keepalive := time.NewTicker(KeepalivePeriod)
	defer keepalive.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case value, open := <-wake:
			if !onWake(value, open) {
				return
			}
		case <-keepalive.C:
			if !authorized() {
				return
			}
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
