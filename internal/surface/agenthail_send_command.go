package surface

import (
	"encoding/json"
	"path"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

type OutgoingSend struct {
	Target  string
	Summary string
	Body    string
}

var shellToolNames = map[string]bool{"Bash": true, "exec_command": true, "functions.exec_command": true, "shell": true, "local_shell": true}

var sendValueFlags = map[string]bool{"--from": true, "--timeout": true}

var sendBoolFlags = map[string]bool{"--stream": true, "--reply": true, "--json": true}

func ParseOutgoingSend(tool, input string) (OutgoingSend, bool) {
	var fields map[string]any
	if json.Unmarshal([]byte(input), &fields) != nil {
		return OutgoingSend{}, false
	}
	if tool == "SendMessage" {
		return sendMessageCall(fields)
	}
	if !shellToolNames[tool] {
		return OutgoingSend{}, false
	}
	script, ok := shellScript(fields)
	if !ok {
		return OutgoingSend{}, false
	}
	return agenthailSendCommand(script)
}

func sendMessageCall(fields map[string]any) (OutgoingSend, bool) {
	target, _ := fields["to"].(string)
	body, _ := fields["message"].(string)
	if strings.TrimSpace(target) == "" {
		return OutgoingSend{}, false
	}
	summary, _ := fields["summary"].(string)
	return outgoingSend(target, summary, body), true
}

func outgoingSend(target, summary, body string) OutgoingSend {
	if strings.TrimSpace(summary) == "" {
		summary, _, _ = strings.Cut(strings.TrimSpace(body), "\n")
	}
	return OutgoingSend{Target: strings.TrimSpace(target), Summary: AgentSummary(summary), Body: body}
}

func shellScript(fields map[string]any) (string, bool) {
	for _, key := range []string{"command", "cmd"} {
		switch value := fields[key].(type) {
		case string:
			return value, true
		case []any:
			if len(value) == 3 {
				shell, _ := value[0].(string)
				flag, _ := value[1].(string)
				script, ok := value[2].(string)
				if ok && (path.Base(shell) == "bash" || path.Base(shell) == "sh" || path.Base(shell) == "zsh") && (flag == "-c" || flag == "-lc") {
					return script, true
				}
			}
		}
	}
	return "", false
}

func agenthailSendCommand(script string) (OutgoingSend, bool) {
	file, err := syntax.NewParser().Parse(strings.NewReader(script), "")
	if err != nil {
		return OutgoingSend{}, false
	}
	var leaves []*syntax.Stmt
	for _, stmt := range file.Stmts {
		if !andLeaves(stmt, &leaves) {
			return OutgoingSend{}, false
		}
	}
	var send *syntax.Stmt
	for _, stmt := range leaves {
		call, _ := stmt.Cmd.(*syntax.CallExpr)
		if call == nil {
			return OutgoingSend{}, false
		}
		if isAgenthailSend(call) {
			if send != nil {
				return OutgoingSend{}, false
			}
			send = stmt
		} else if !isSetupCall(stmt, call) {
			return OutgoingSend{}, false
		}
	}
	if send == nil {
		return OutgoingSend{}, false
	}
	return sendStatement(send)
}

func andLeaves(stmt *syntax.Stmt, leaves *[]*syntax.Stmt) bool {
	if stmt.Negated || stmt.Background || stmt.Coprocess {
		return false
	}
	if binary, ok := stmt.Cmd.(*syntax.BinaryCmd); ok {
		return binary.Op == syntax.AndStmt && len(stmt.Redirs) == 0 && andLeaves(binary.X, leaves) && andLeaves(binary.Y, leaves)
	}
	*leaves = append(*leaves, stmt)
	return true
}

func isAgenthailSend(call *syntax.CallExpr) bool {
	return len(call.Args) >= 2 && path.Base(call.Args[0].Lit()) == "agenthail" && call.Args[1].Lit() == "send"
}

func isSetupCall(stmt *syntax.Stmt, call *syntax.CallExpr) bool {
	if len(stmt.Redirs) > 0 {
		return false
	}
	return len(call.Args) == 0 || (call.Args[0].Lit() == "cd" && len(call.Args) <= 2)
}

func sendStatement(stmt *syntax.Stmt) (OutgoingSend, bool) {
	call := stmt.Cmd.(*syntax.CallExpr)
	var positional []string
	for index := 2; index < len(call.Args); index++ {
		word, ok := literalWord(call.Args[index])
		if !ok {
			return OutgoingSend{}, false
		}
		switch {
		case sendValueFlags[word]:
			index++
		case sendBoolFlags[word]:
		case word == "--":
		case strings.HasPrefix(word, "--"):
			return OutgoingSend{}, false
		default:
			positional = append(positional, word)
		}
	}
	if len(positional) < 2 {
		return OutgoingSend{}, false
	}
	target, message := positional[0], strings.Join(positional[1:], " ")
	if message != "-" {
		if len(stmt.Redirs) > 0 && !onlyOutputRedirects(stmt.Redirs) {
			return OutgoingSend{}, false
		}
		return outgoingSend(target, "", message), true
	}
	return stdinSend(target, stmt.Redirs)
}

func stdinSend(target string, redirects []*syntax.Redirect) (OutgoingSend, bool) {
	var input *syntax.Redirect
	for _, redirect := range redirects {
		switch redirect.Op {
		case syntax.RdrIn, syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
			if input != nil {
				return OutgoingSend{}, false
			}
			input = redirect
		default:
			if !isOutputRedirect(redirect.Op) {
				return OutgoingSend{}, false
			}
		}
	}
	if input == nil {
		return OutgoingSend{}, false
	}
	switch input.Op {
	case syntax.RdrIn:
		name, ok := literalWord(input.Word)
		if !ok {
			return OutgoingSend{}, false
		}
		return OutgoingSend{Target: target, Summary: "(message from " + path.Base(name) + ")"}, true
	case syntax.WordHdoc:
		body, ok := literalWord(input.Word)
		return outgoingSend(target, "", body), ok
	}
	body, ok := heredocBody(input)
	return outgoingSend(target, "", strings.TrimSuffix(body, "\n")), ok
}

func heredocBody(redirect *syntax.Redirect) (string, bool) {
	if redirect.Hdoc == nil {
		return "", true
	}
	if redirect.Word.Lit() == "" {
		return redirect.Hdoc.Lit(), true
	}
	if hasExpansion(redirect.Hdoc) {
		return "", false
	}
	body, err := expand.Document(nil, redirect.Hdoc)
	return body, err == nil
}

func onlyOutputRedirects(redirects []*syntax.Redirect) bool {
	for _, redirect := range redirects {
		if !isOutputRedirect(redirect.Op) {
			return false
		}
	}
	return true
}

func isOutputRedirect(op syntax.RedirOperator) bool {
	switch op {
	case syntax.RdrOut, syntax.AppOut, syntax.DplOut, syntax.RdrClob, syntax.RdrAll, syntax.AppAll:
		return true
	}
	return false
}

func literalWord(word *syntax.Word) (string, bool) {
	if word == nil || hasExpansion(word) {
		return "", false
	}
	for index, part := range word.Parts {
		if lit, ok := part.(*syntax.Lit); ok && (strings.ContainsAny(lit.Value, "*?[") || (index == 0 && strings.HasPrefix(lit.Value, "~"))) {
			return "", false
		}
	}
	value, err := expand.Literal(nil, word)
	return value, err == nil
}

func hasExpansion(word *syntax.Word) bool {
	found := false
	syntax.Walk(word, func(node syntax.Node) bool {
		switch part := node.(type) {
		case *syntax.ParamExp, *syntax.CmdSubst, *syntax.ArithmExp, *syntax.ProcSubst, *syntax.ExtGlob, *syntax.BraceExp:
			found = true
		case *syntax.SglQuoted:
			found = found || part.Dollar
		}
		return !found
	})
	return found
}
