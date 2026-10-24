package sse

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

type Event struct {
	ID       string
	HasID    bool
	Type     string
	Data     string
	HasData  bool
	Comment  []string
	Retry    time.Duration
	HasRetry bool
}

type Reader struct {
	scanner     *bufio.Scanner
	lastEventID string
	retry       time.Duration
}

func NewReader(reader io.Reader) *Reader {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	return &Reader{scanner: scanner}
}

func (r *Reader) Retry() time.Duration {
	return r.retry
}

func (r *Reader) Next() (Event, error) {
	var event Event
	var data []string
	for r.scanner.Scan() {
		line := r.scanner.Text()
		if line == "" {
			event.Data = strings.Join(data, "\n")
			event.HasData = len(data) > 0
			if event.HasData && !event.HasID {
				event.ID = r.lastEventID
			}
			if event.HasData || event.HasID || event.Type != "" || len(event.Comment) > 0 || event.HasRetry {
				return event, nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			event.Comment = append(event.Comment, strings.TrimPrefix(line, ":"))
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			value = ""
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "id":
			if !strings.ContainsRune(value, '\x00') {
				r.lastEventID = value
				event.ID = value
				event.HasID = true
			}
		case "event":
			event.Type = value
		case "data":
			data = append(data, value)
		case "retry":
			if retry, ok := parseRetry(value); ok {
				r.retry = retry
				event.Retry = retry
				event.HasRetry = true
			}
		}
	}
	if err := r.scanner.Err(); err != nil {
		return Event{}, err
	}
	return Event{}, io.EOF
}

func parseRetry(value string) (time.Duration, bool) {
	if value == "" {
		return 0, false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	milliseconds, err := strconv.ParseUint(value, 10, 64)
	if err != nil || milliseconds > uint64((1<<63-1)/int64(time.Millisecond)) {
		return 0, false
	}
	return time.Duration(milliseconds) * time.Millisecond, true
}

func WriteEvent(writer io.Writer, event Event) error {
	for _, comment := range event.Comment {
		if _, err := fmt.Fprintln(writer, ":"+comment); err != nil {
			return err
		}
	}
	if event.HasRetry {
		if _, err := fmt.Fprintf(writer, "retry: %d\n", event.Retry.Milliseconds()); err != nil {
			return err
		}
	}
	if event.HasID {
		if _, err := fmt.Fprintln(writer, "id: "+event.ID); err != nil {
			return err
		}
	}
	if event.Type != "" {
		if _, err := fmt.Fprintln(writer, "event: "+event.Type); err != nil {
			return err
		}
	}
	if event.HasData {
		for _, line := range strings.Split(event.Data, "\n") {
			if _, err := fmt.Fprintln(writer, "data: "+line); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintln(writer)
	return err
}
