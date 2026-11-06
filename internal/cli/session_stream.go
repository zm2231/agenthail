package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/sse"
	"github.com/zm2231/agenthail/internal/surface"
)

type sessionStreamReader interface {
	OpenSessionStream(context.Context, *surface.Session, uint64) (sessionstream.Subscription, error)
}

type daemonSessionStreamClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func newDaemonSessionStreamClient() (sessionStreamReader, error) {
	baseURL, token, err := daemonEndpoint()
	if err != nil {
		return nil, err
	}
	return &daemonSessionStreamClient{baseURL: baseURL, token: token, httpClient: &http.Client{}}, nil
}

func (c *daemonSessionStreamClient) OpenSessionStream(ctx context.Context, session *surface.Session, after uint64) (sessionstream.Subscription, error) {
	if session == nil || session.ID == "" {
		return sessionstream.Subscription{}, errors.New("session is required")
	}
	streamCtx, cancel := context.WithCancel(ctx)
	response, err := c.request(streamCtx, session.ID, after)
	if err != nil {
		cancel()
		return sessionstream.Subscription{}, err
	}
	var mu sync.Mutex
	current := response
	events := make(chan sessionstream.Event, 64)
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			mu.Lock()
			_ = current.Body.Close()
			mu.Unlock()
		})
	}
	go func() {
		defer close(events)
		last := after
		rebuild := sessionstream.NewRebuild()
		for {
			err := decodeSessionSSE(streamCtx, response.Body, events, rebuild, &last)
			_ = response.Body.Close()
			if errors.Is(err, sessionstream.ErrResync) && streamCtx.Err() == nil {
				if response, err = c.request(streamCtx, session.ID, last); err == nil {
					rebuild = sessionstream.NewRebuild()
					mu.Lock()
					current = response
					mu.Unlock()
					continue
				}
			}
			if err != nil && streamCtx.Err() == nil {
				select {
				case events <- sessionstream.Event{Kind: "source-error", Reason: err.Error()}:
				case <-streamCtx.Done():
				}
			}
			return
		}
	}()
	return sessionstream.Subscription{Cursor: after, Events: events, Cancel: stop}, nil
}

func (c *daemonSessionStreamClient) request(ctx context.Context, sessionID string, after uint64) (*http.Response, error) {
	query := url.Values{"id": []string{sessionID}, "after": []string{strconv.FormatUint(after, 10)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.baseURL, "/")+"/api/v1/session-stream?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build daemon session stream request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("open daemon session stream: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return nil, fmt.Errorf("daemon session stream returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return response, nil
}

func decodeSessionSSE(ctx context.Context, reader io.Reader, events chan<- sessionstream.Event, rebuild *sessionstream.Rebuild, last *uint64) error {
	sseReader := sse.NewReader(reader)
	for {
		sseEvent, err := sseReader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if !sseEvent.HasData {
			continue
		}
		seq, _ := strconv.ParseUint(sseEvent.ID, 10, 64)
		event, err := rebuild.Apply(seq, []byte(sseEvent.Data))
		if err != nil {
			return err
		}
		if event.Seq > *last {
			*last = event.Seq
		}
		select {
		case events <- event:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
