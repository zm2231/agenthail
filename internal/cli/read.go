package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/zm2231/agenthail/internal/report"
	"github.com/zm2231/agenthail/internal/surface"
)

func (a *App) cmdReply(args []string) error {
	positional := stripFlags(args)
	if len(positional) != 1 {
		return fmt.Errorf("usage: agenthail reply <target> [--before cursor] [--json] [--max-bytes N | --report] [--timeout 30s]")
	}
	mode, err := readModeFlags(args)
	if err != nil {
		return err
	}
	timeout, err := commandTimeout(args, a.DefaultTimeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	sess, surf, remote, err := a.resolveReadTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if remote != nil {
		sess = remote.session()
	} else if !surface.CapabilitiesFor(surf, sess).Reply {
		return fmt.Errorf("%s does not support reply", surf.Name())
	}
	before, err := sessionReadBefore(args)
	if err != nil {
		return err
	}
	read, err := a.readSessionPage(ctx, surf, sess, surface.SessionReadRequest{Limit: 2, Before: before})
	if err != nil {
		return err
	}
	if read.UnavailableReason != "" && len(read.Exchanges) == 0 {
		return fmt.Errorf("session read unavailable from %s: %s", read.Source, read.UnavailableReason)
	}
	reply := read.Reply
	if reply == nil {
		return fmt.Errorf("%s returned an empty reply result", sess.Surface)
	}
	if reply.Error != "" {
		return fmt.Errorf("latest %s turn did not complete successfully: %s", sess.Surface, reply.Error)
	}
	if mode.report {
		parsed, err := report.Parse(reply.Text)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"surface": sess.Surface, "session": sess.ID, "source": read.Source, "done": reply.Done, "report": parsed, "turn": replyTurn(read.Exchanges, reply)})
	}
	text, cut := capBytes(reply.Text, mode.maxBytes)
	if hasFlag(args, "--json") {
		document := map[string]any{"surface": sess.Surface, "session": sess.ID, "text": text, "done": reply.Done, "source": read.Source, "journalSeq": read.JournalSeq, "nextBefore": read.NextBefore, "readError": read.UnavailableReason, "warning": read.Warning, "turn": replyTurn(read.Exchanges, reply)}
		if cut {
			document["truncated"], document["textBytes"] = true, len(reply.Text)
		}
		return json.NewEncoder(os.Stdout).Encode(document)
	} else {
		printSessionReadWarning(read)
		if read.Source != "" {
			fmt.Printf("[%s]\n", read.Source)
		}
		fmt.Println(text)
		if cut {
			fmt.Println(truncationNote(len(text), len(reply.Text)))
		}
		if read.NextBefore > 0 {
			fmt.Printf("older cursor: %d\n", read.NextBefore)
		}
	}
	return nil
}

func (a *App) cmdLast(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 1 || len(positional) > 2 {
		return fmt.Errorf("usage: agenthail last <target> [count] [--before cursor] [--full] [--json] [--max-bytes N | --report] [--timeout 30s]")
	}
	mode, err := readModeFlags(args)
	if err != nil {
		return err
	}
	timeout, err := commandTimeout(args, a.DefaultTimeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	sess, surf, remote, err := a.resolveReadTarget(ctx, positional[0])
	if err != nil {
		return err
	}
	if remote != nil {
		sess = remote.session()
	}
	n := 1
	if len(positional) > 1 {
		v, parseErr := strconv.Atoi(positional[1])
		if parseErr != nil || v < 1 || v > surface.MaxExchangePage {
			return fmt.Errorf("count must be an integer from 1 to %d", surface.MaxExchangePage)
		}
		n = v
	}
	before, err := sessionReadBefore(args)
	if err != nil {
		return err
	}
	read, err := a.readSessionPage(ctx, surf, sess, surface.SessionReadRequest{Limit: n, Before: before})
	if err != nil {
		return err
	}
	exchanges := append([]surface.Exchange{}, read.Exchanges...)
	for pages := 1; len(exchanges) < n && read.NextBefore > 0 && pages < 10; pages++ {
		older, olderErr := a.readSessionPage(ctx, surf, sess, surface.SessionReadRequest{Limit: n - len(exchanges), Before: read.NextBefore})
		if olderErr != nil {
			break
		}
		exchanges = append(append([]surface.Exchange{}, older.Exchanges...), exchanges...)
		read.NextBefore = older.NextBefore
	}
	if len(exchanges) > n {
		exchanges = exchanges[len(exchanges)-n:]
	}
	if read.UnavailableReason != "" && len(exchanges) == 0 {
		return fmt.Errorf("session read unavailable from %s: %s", read.Source, read.UnavailableReason)
	}
	if mode.report {
		return printExchangeReports(sess, read, exchanges)
	}
	capped := make([]cappedExchange, len(exchanges))
	for i, exchange := range exchanges {
		capped[i] = capExchange(exchange, mode.maxBytes)
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"surface": sess.Surface, "session": sess.ID, "source": read.Source, "journalSeq": read.JournalSeq, "nextBefore": read.NextBefore, "readError": read.UnavailableReason, "warning": read.Warning, "exchanges": capped})
	}
	printSessionReadWarning(read)
	if len(exchanges) == 0 {
		fmt.Println("(no conversation history)")
		return nil
	}
	label := a.resolveDisplay(sess.ID)
	if remote != nil {
		label = remote.label()
	}
	source := read.Source
	if source != "" {
		fmt.Printf("── %s [%s] ──\n", label, source)
	} else {
		fmt.Printf("── %s ──\n", label)
	}
	full := hasFlag(args, "--full") || mode.maxBytes > 0
	for _, ex := range capped {
		if !ex.Timestamp.IsZero() {
			fmt.Printf("%s\n", ex.Timestamp.Local().Format("2006-01-02 15:04:05"))
		}
		if ex.User != "" || ex.UserTruncated {
			if full {
				fmt.Printf("%s ▸ %s\n", promptLabel(ex.Exchange), ex.User)
				if ex.UserTruncated {
					fmt.Println(truncationNote(len(ex.User), ex.UserBytes))
				}
			} else {
				fmt.Printf("%s ▸ %s\n", promptLabel(ex.Exchange), truncate(strings.ReplaceAll(ex.User, "\n", " "), 200))
			}
		}
		if ex.Assistant != "" || ex.AssistantTruncated {
			if full {
				fmt.Printf(" ai  ▸ %s\n", ex.Assistant)
				if ex.AssistantTruncated {
					fmt.Println(truncationNote(len(ex.Assistant), ex.AssistantBytes))
				}
			} else {
				fmt.Printf(" ai  ▸ %s\n", truncate(strings.ReplaceAll(ex.Assistant, "\n", " "), 200))
			}
		}
		fmt.Println()
	}
	if read.NextBefore > 0 {
		fmt.Printf("older cursor: %d\n", read.NextBefore)
	}
	return nil
}

func promptLabel(exchange surface.Exchange) string {
	switch exchange.Origin {
	case "":
		return "you "
	case "peer":
		if exchange.Sender != "" {
			return "from " + exchange.Sender
		}
		return "agent"
	case "task-notification":
		return "task notification"
	case "auto-continuation":
		return "continued"
	case "agenthail-notice":
		return "agenthail"
	}
	return exchange.Origin
}

func printSessionReadWarning(read *surface.SessionReadResult) {
	if read.Warning != "" {
		fmt.Fprintf(os.Stderr, "warning: %s\n", read.Warning)
	}
	if read.UnavailableReason != "" {
		fmt.Fprintf(os.Stderr, "warning: %s\n", read.UnavailableReason)
	}
}

func readSessionWithContext(ctx context.Context, adapter surface.Surface, session *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	type result struct {
		read *surface.SessionReadResult
		err  error
	}
	completed := make(chan result, 1)
	go func() {
		read, err := surface.ReadSession(ctx, adapter, session, request)
		completed <- result{read: read, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case outcome := <-completed:
		return outcome.read, outcome.err
	}
}

const offlineSessionPageItems = 40

func (a *App) readSessionPage(ctx context.Context, adapter surface.Surface, session *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	if a.daemonIsRunning() {
		var reader sessionPageReader
		var err error
		if a.daemonSessionPageReader != nil {
			reader, err = a.daemonSessionPageReader()
		} else {
			reader, err = newDaemonSessionPageClient()
		}
		if err != nil {
			return nil, fmt.Errorf("read active daemon session page: %w", err)
		}
		if reader == nil {
			return nil, errors.New("read active daemon session page: reader is unavailable")
		}
		read, err := reader.ReadSession(ctx, session, request)
		if err != nil {
			return nil, err
		}
		if read == nil {
			return nil, errors.New("read active daemon session page: empty response")
		}
		return surface.BoundSessionRead(session, read), nil
	}
	request.Limit = offlineSessionPageItems
	read, err := readSessionWithContext(ctx, adapter, session, request)
	if err != nil {
		return nil, err
	}
	if read != nil && read.Source == "" {
		read.Source = string(adapter.Name()) + "-offline-provider"
	}
	return read, nil
}
