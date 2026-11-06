package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/zm2231/agenthail/internal/ledger"
)

type ledgerWriteArgs struct {
	positional []string
	values     map[string][]string
	bools      map[string]bool
}

var ledgerWriteValueFlags = map[string]bool{"--as": true, "--to": true, "--clause": true, "--type": true, "--source": true, "--session": true, "--project": true, "--from": true,
	"--recommendation": true, "--default": true, "--deadline": true, "--kind": true, "--reason": true, "--ref": true, "--condition": true,
	"--on": true, "--utterance": true, "--done": true, "--remaining": true, "--decisions": true, "--uncertain": true,
	"--covers": true, "--missing": true, "--in": true, "--through": true,
	"--priority": true, "--description": true, "--acceptance": true, "--label": true, "--unlabel": true, "--text": true, "--found-in": true, "--state": true, "--assignee": true, "--sort": true, "--limit": true, "--attach": true}

var ledgerRepeatableFlags = map[string]bool{"--clause": true, "--done": true, "--remaining": true, "--decisions": true, "--uncertain": true,
	"--covers": true, "--missing": true, "--in": true, "--label": true, "--unlabel": true}

var ledgerWriteBoolFlags = map[string]bool{"--adopt": true, "--reject": true, "--ack": true, "--json": true, "--keeps-breadth": true, "--mine": true, "--none": true, "--all": true, "--found": true, "--dry-run": true}

func parseLedgerWrite(args []string) (ledgerWriteArgs, error) {
	parsed := ledgerWriteArgs{values: map[string][]string{}, bools: map[string]bool{}}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			parsed.positional = append(parsed.positional, args[i+1:]...)
			return parsed, nil
		case ledgerWriteValueFlags[arg]:
			if i+1 >= len(args) {
				return parsed, fmt.Errorf("flag %s requires a value", arg)
			}
			if !ledgerRepeatableFlags[arg] && len(parsed.values[arg]) > 0 {
				return parsed, fmt.Errorf("flag %s may only be specified once", arg)
			}
			parsed.values[arg] = append(parsed.values[arg], args[i+1])
			i++
		case ledgerWriteBoolFlags[arg]:
			parsed.bools[arg] = true
		case strings.HasPrefix(arg, "--"):
			return parsed, fmt.Errorf("unknown flag %s for ledger", arg)
		default:
			parsed.positional = append(parsed.positional, arg)
		}
	}
	return parsed, nil
}

func (w ledgerWriteArgs) value(flag string) string {
	if v := w.values[flag]; len(v) > 0 {
		return v[0]
	}
	return ""
}

func (a *App) ledgerWriteContext(ctx context.Context, w ledgerWriteArgs) (string, string, error) {
	key := w.value("--project")
	if key == "" {
		var err error
		if key, err = ledgerProjectKey(a); err != nil {
			return "", "", err
		}
	}
	session, err := a.sourceSessionID(ctx, w.value("--from"))
	if err != nil {
		return "", "", err
	}
	if session == "" {
		return "", "", fmt.Errorf("ledger writes are made by a session: run this from inside one, or pass --from <session>")
	}
	return key, session, nil
}

func (a *App) ledgerPost(ctx context.Context, path string, payload, into any) error {
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	data, status, err := client.call(ctx, http.MethodPost, path, nil, payload, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return actionError(data)
	}
	return json.Unmarshal(data, into)
}

func (a *App) ledgerGoal(args []string) error {
	w, err := parseLedgerWrite(args)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(strings.Join(w.positional[1:], " "))
	if name == "" {
		return fmt.Errorf("usage: agenthail ledger goal <name>")
	}
	ctx := context.Background()
	key, session, err := a.ledgerWriteContext(ctx, w)
	if err != nil {
		return err
	}
	var answer struct {
		Goal    ledger.Goal `json:"goal"`
		Created bool        `json:"created"`
	}
	if err := a.ledgerPost(ctx, "/api/v1/ledger/goal", map[string]string{"projectKey": key, "sessionId": session, "name": name}, &answer); err != nil {
		return err
	}
	if w.bools["--json"] {
		return emit(answer)
	}
	verb := "exists"
	if answer.Created {
		verb = "created"
	}
	fmt.Printf("%s %s: %s\n", answer.Goal.ID, verb, answer.Goal.Name)
	return nil
}

func attachFields(w ledgerWriteArgs) ledger.AttachRequest {
	return ledger.AttachRequest{
		As: ledger.AttachAs(w.value("--as")), To: w.value("--to"), Clauses: w.values["--clause"],
		AskType: ledger.AskType(w.value("--type")), Source: ledger.ClauseSource(w.value("--source")),
		Adopt: w.bools["--adopt"], Reject: w.bools["--reject"], Ack: w.bools["--ack"], Delegate: w.value("--session"), Covers: w.values["--covers"],
	}
}

func attachSegments(args []string) [][]string {
	segments := [][]string{{}}
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--":
			last := len(segments) - 1
			segments[last] = append(segments[last], args[i:]...)
			return segments
		case arg == "--and":
			segments = append(segments, []string{})
		case ledgerWriteValueFlags[arg] && i+1 < len(args):
			last := len(segments) - 1
			segments[last] = append(segments[last], arg, args[i+1])
			i++
		default:
			last := len(segments) - 1
			segments[last] = append(segments[last], arg)
		}
	}
	return segments
}

const attachUsage = `usage: agenthail ledger attach <utterance_id> --as <new-ask|extends|constrains|refines|reacts|delegates|decides|conversation> [--to <goal|ask|clause|item|project>] [--clause "..."] [--and <utterance_id> --as ... ]...`

func (a *App) ledgerAttach(args []string, propose bool) error {
	segments := attachSegments(args)
	if len(segments) > 1 {
		if propose {
			return fmt.Errorf("propose records one fragment; --and is for attach")
		}
		return a.ledgerAttachEach(segments)
	}
	w, err := parseLedgerWrite(args)
	if err != nil {
		return err
	}
	req := attachFields(w)
	switch {
	case propose && len(w.positional) > 1:
		return fmt.Errorf("usage: agenthail ledger propose --to <ask|goal|project> --clause \"...\" [--as extends|new-ask|constrains]")
	case propose:
		if req.Source != "" {
			return fmt.Errorf("propose records proposed clauses; use attach for --source %s", req.Source)
		}
		req.Source = ledger.SourceProposed
		if req.As == "" {
			req.As = ledger.AsExtends
		}
	case len(w.positional) != 2:
		return fmt.Errorf("%s", attachUsage)
	default:
		req.UtteranceID = w.positional[1]
	}
	if req.As == "" {
		return fmt.Errorf("--as is required")
	}
	ctx := context.Background()
	if req.ProjectKey, req.SessionID, err = a.ledgerWriteContext(ctx, w); err != nil {
		return err
	}
	var result ledger.AttachResult
	if err := a.ledgerPost(ctx, "/api/v1/ledger/attach", req, &result); err != nil {
		return err
	}
	if w.bools["--json"] {
		return emit(result)
	}
	fmt.Println(result.Readback)
	return nil
}

func (a *App) ledgerAttachEach(segments [][]string) error {
	first, err := parseLedgerWrite(segments[0])
	if err != nil {
		return err
	}
	fragments := make([]ledger.AttachRequest, len(segments))
	for i, segment := range segments {
		w, utterance := first, ""
		if i == 0 {
			if len(w.positional) != 2 {
				return fmt.Errorf("%s", attachUsage)
			}
			utterance = w.positional[1]
		} else {
			if w, err = parseLedgerWrite(segment); err != nil {
				return err
			}
			for _, flag := range []string{"--project", "--from", "--json"} {
				if len(w.values[flag]) > 0 || w.bools[flag] {
					return fmt.Errorf("%s applies to the whole call; give it before the first --and", flag)
				}
			}
			if len(w.positional) != 1 {
				return fmt.Errorf("each --and starts with the utterance it attaches\n%s", attachUsage)
			}
			utterance = w.positional[0]
		}
		fragments[i] = attachFields(w)
		fragments[i].UtteranceID = utterance
		if fragments[i].As == "" {
			return fmt.Errorf("fragment %d (%s) needs --as", i+1, utterance)
		}
	}
	ctx := context.Background()
	key, session, err := a.ledgerWriteContext(ctx, first)
	if err != nil {
		return err
	}
	var result ledger.AttachEachResult
	if err := a.ledgerPost(ctx, "/api/v1/ledger/attach", map[string]any{"projectKey": key, "sessionId": session, "fragments": fragments}, &result); err != nil {
		return err
	}
	if first.bools["--json"] {
		return emit(result)
	}
	refused := 0
	for i, outcome := range result.Fragments {
		if outcome.Refused != "" {
			refused++
			fmt.Printf("%s: refused: %s\n", fragments[i].UtteranceID, outcome.Refused)
			continue
		}
		fmt.Println(outcome.Readback)
	}
	if refused > 0 {
		return fmt.Errorf("%d of %d fragments were refused; the others are attached", refused, len(fragments))
	}
	return nil
}
