package cli

import (
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

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/surface"
)

// sessionPageReader is the one page-shaped read used by the CLI.  Both the
// daemon and offline paths return the same surface result to command output.
type sessionPageReader interface {
	ReadSession(context.Context, *surface.Session, surface.SessionReadRequest) (*surface.SessionReadResult, error)
}

type daemonSessionPageClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

type daemonSessionPageError struct {
	Status      int
	Code        string
	Message     string
	EarliestSeq uint64
	LatestSeq   uint64
}

func (e *daemonSessionPageError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("daemon session page: %s (%s)", e.Message, e.Code)
	}
	return fmt.Sprintf("daemon session page returned HTTP %d: %s", e.Status, e.Message)
}

func newDaemonSessionPageClient() (sessionPageReader, error) {
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
	return &daemonSessionPageClient{
		baseURL:    "http://" + config.Listen,
		token:      token,
		httpClient: &http.Client{},
	}, nil
}

func (c *daemonSessionPageClient) ReadSession(ctx context.Context, session *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	if session == nil || session.ID == "" {
		return nil, errors.New("session is required")
	}
	limit := request.Limit
	if limit < 4 {
		limit = 4
	}
	if limit > 40 {
		limit = 40
	}
	query := url.Values{}
	query.Set("id", session.ID)
	query.Set("timeline", "1")
	query.Set("limit", strconv.Itoa(limit))
	if request.Before > 0 {
		query.Set("timelineBefore", strconv.FormatInt(request.Before, 10))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.baseURL, "/")+"/api/v1/session?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build daemon session request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("read daemon session page: %w", err)
	}
	defer response.Body.Close()
	var payload daemonSessionPageResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode daemon session page: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, payload.pageError(response.StatusCode)
	}
	return payload.result(session), nil
}

type daemonSessionPageResponse struct {
	Session             surface.Session          `json:"session"`
	ReadSource          string                   `json:"readSource"`
	ReadError           string                   `json:"readError"`
	JournalSeq          uint64                   `json:"journalSeq"`
	Exchanges           []surface.Exchange       `json:"exchanges"`
	Timeline            *surface.SessionTimeline `json:"timeline"`
	TranscriptTruncated bool                     `json:"transcriptTruncated"`
	Error               struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	EarliestSeq uint64 `json:"earliestSeq"`
	LatestSeq   uint64 `json:"latestSeq"`
}

func (p daemonSessionPageResponse) pageError(status int) error {
	message := p.Error.Message
	if message == "" {
		message = p.ReadError
	}
	if message == "" {
		message = http.StatusText(status)
	}
	return &daemonSessionPageError{Status: status, Code: p.Error.Code, Message: message, EarliestSeq: p.EarliestSeq, LatestSeq: p.LatestSeq}
}

func (p daemonSessionPageResponse) result(session *surface.Session) *surface.SessionReadResult {
	result := &surface.SessionReadResult{
		JournalSeq:        p.JournalSeq,
		Items:             []surface.TimelineItem{},
		Exchanges:         p.Exchanges,
		Source:            p.ReadSource,
		UnavailableReason: p.ReadError,
		Truncated:         p.TranscriptTruncated,
	}
	if p.Timeline != nil {
		result.Items = p.Timeline.Items
		result.NextBefore = p.Timeline.NextBefore
		if result.Source == "" {
			result.Source = p.Timeline.Source
		}
		result.Truncated = result.Truncated || p.Timeline.Truncated
		if result.UnavailableReason == "" {
			result.UnavailableReason = p.Timeline.UnavailableReason
		}
	}
	boundSession := session
	if p.Session.ID != "" {
		boundSession = &p.Session
	}
	return surface.BoundSessionRead(boundSession, result)
}
