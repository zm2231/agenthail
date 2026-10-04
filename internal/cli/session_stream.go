package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/sessionstream"
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
	config, err := daemon.LoadDashboardConfig()
	if err != nil {
		return nil, err
	}
	if !config.Enabled {
		return nil, errors.New("dashboard is disabled")
	}
	tokenBytes, err := os.ReadFile(daemon.DashboardTokenPath())
	if err != nil {
		return nil, fmt.Errorf("read dashboard token: %w", err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return nil, errors.New("dashboard token is empty")
	}
	return &daemonSessionStreamClient{baseURL: "http://" + config.Listen, token: token, httpClient: &http.Client{}}, nil
}

func (c *daemonSessionStreamClient) OpenSessionStream(ctx context.Context, session *surface.Session, after uint64) (sessionstream.Subscription, error) {
	if session == nil || session.ID == "" {
		return sessionstream.Subscription{}, errors.New("session is required")
	}
	streamCtx, cancel := context.WithCancel(ctx)
	query := url.Values{"id": []string{session.ID}, "after": []string{strconv.FormatUint(after, 10)}}
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, strings.TrimRight(c.baseURL, "/")+"/api/v1/session-stream?"+query.Encode(), nil)
	if err != nil {
		cancel()
		return sessionstream.Subscription{}, fmt.Errorf("build daemon session stream request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.httpClient.Do(req)
	if err != nil {
		cancel()
		return sessionstream.Subscription{}, fmt.Errorf("open daemon session stream: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		cancel()
		return sessionstream.Subscription{}, fmt.Errorf("daemon session stream returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	events := make(chan sessionstream.Event, 64)
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			_ = response.Body.Close()
		})
	}
	go func() {
		defer close(events)
		defer response.Body.Close()
		if err := decodeSessionSSE(streamCtx, response.Body, events); err != nil && streamCtx.Err() == nil {
			select {
			case events <- sessionstream.Event{Kind: "source-error", Reason: err.Error()}:
			case <-streamCtx.Done():
			}
		}
	}()
	return sessionstream.Subscription{Cursor: after, Events: events, Cancel: stop}, nil
}

func decodeSessionSSE(ctx context.Context, reader io.Reader, events chan<- sessionstream.Event) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	var id string
	var data []string
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		var envelope struct {
			Seq  uint64          `json:"seq"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(strings.Join(data, "\n")), &envelope); err != nil {
			return err
		}
		seq := envelope.Seq
		if seq == 0 && id != "" {
			seq, _ = strconv.ParseUint(id, 10, 64)
		}
		event, err := sessionstream.DecodePayload(seq, envelope.Data)
		if err != nil {
			return err
		}
		select {
		case events <- event:
		case <-ctx.Done():
			return ctx.Err()
		}
		id = ""
		data = data[:0]
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				return err
			}
		case strings.HasPrefix(line, "id:"):
			id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(line, "data:"))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}
