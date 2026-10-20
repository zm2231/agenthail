package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/report"
	"github.com/zm2231/agenthail/internal/surface"
)

type readMode struct {
	maxBytes int
	report   bool
}

func readModeFlags(args []string) (readMode, error) {
	mode := readMode{report: hasFlag(args, "--report")}
	if hasFlag(args, "--max-bytes") {
		n, err := strconv.Atoi(flagVal(args, "--max-bytes"))
		if err != nil || n < 1 {
			return readMode{}, errors.New("--max-bytes must be a positive integer")
		}
		mode.maxBytes = n
	}
	if mode.report && mode.maxBytes > 0 {
		return readMode{}, errors.New("--report and --max-bytes cannot be combined: a report is never cut")
	}
	return mode, nil
}

// capBytes cuts text to at most limit bytes without splitting a UTF-8
// sequence, so the result is never invalid where the input was valid.
func capBytes(text string, limit int) (string, bool) {
	if limit <= 0 || len(text) <= limit {
		return text, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut], true
}

func truncationNote(shown, total int) string {
	return fmt.Sprintf("[truncated: %d of %d bytes]", shown, total)
}

type cappedExchange struct {
	surface.Exchange
	UserTruncated      bool `json:"userTruncated,omitempty"`
	UserBytes          int  `json:"userBytes,omitempty"`
	AssistantTruncated bool `json:"assistantTruncated,omitempty"`
	AssistantBytes     int  `json:"assistantBytes,omitempty"`
}

func capExchange(exchange surface.Exchange, limit int) cappedExchange {
	capped := cappedExchange{Exchange: exchange}
	if text, cut := capBytes(exchange.User, limit); cut {
		capped.User, capped.UserTruncated, capped.UserBytes = text, true, len(exchange.User)
	}
	if text, cut := capBytes(exchange.Assistant, limit); cut {
		capped.Assistant, capped.AssistantTruncated, capped.AssistantBytes = text, true, len(exchange.Assistant)
	}
	return capped
}

type exchangeReport struct {
	Timestamp   time.Time      `json:"timestamp"`
	Report      *report.Report `json:"report,omitempty"`
	ReportError string         `json:"reportError,omitempty"`
}

func readExchangeReport(exchange surface.Exchange) (exchangeReport, error) {
	read := exchangeReport{Timestamp: exchange.Timestamp}
	parsed, err := report.Parse(exchange.Assistant)
	if err != nil {
		read.ReportError = err.Error()
		return read, err
	}
	read.Report = &parsed
	return read, nil
}

// printExchangeReports prints each exchange's report and fails only on the
// newest one: earlier exchanges are often questions or nudges with none.
func printExchangeReports(session *surface.Session, read *surface.SessionReadResult, exchanges []surface.Exchange) error {
	reports := make([]exchangeReport, len(exchanges))
	newest := errors.New("the session has no exchanges to read a report from")
	for i, exchange := range exchanges {
		reports[i], newest = readExchangeReport(exchange)
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"surface": session.Surface, "session": session.ID, "source": read.Source, "journalSeq": read.JournalSeq, "nextBefore": read.NextBefore, "readError": read.UnavailableReason, "warning": read.Warning, "exchanges": reports}); err != nil {
		return err
	}
	if newest != nil && len(exchanges) > 0 {
		return fmt.Errorf("newest exchange: %w", newest)
	}
	return newest
}
