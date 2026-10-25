package surface

import (
	"encoding/json"
	"testing"
)

func bashInput(t *testing.T, command string) string {
	t.Helper()
	data, err := json.Marshal(map[string]string{"command": command, "description": "Report"})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestParseOutgoingSendReadsAgenthailSend(t *testing.T) {
	cases := []struct {
		name, command string
		want          OutgoingSend
	}{
		{"quoted message", `agenthail send @collab "Tests pass on main"`, OutgoingSend{Target: "@collab", Summary: "Tests pass on main", Body: "Tests pass on main"}},
		{"single quotes with punctuation", `agenthail send @collab 'Done? yes [all] *green*'`, OutgoingSend{Target: "@collab", Summary: "Done? yes [all] *green*", Body: "Done? yes [all] *green*"}},
		{"flags around the message", `agenthail send --from me @collab "hi there" --json`, OutgoingSend{Target: "@collab", Summary: "hi there", Body: "hi there"}},
		{"cd first", `cd /work/repo && agenthail send @collab "ok" 2>&1`, OutgoingSend{Target: "@collab", Summary: "ok", Body: "ok"}},
		{"stdin from a file", `agenthail send @collab - < /tmp/x/report.md`, OutgoingSend{Target: "@collab", Summary: "(message from report.md)"}},
		{"quoted heredoc", "agenthail send @collab - <<'EOF'\nLine one $HOME\nLine two\nEOF", OutgoingSend{Target: "@collab", Summary: "Line one $HOME", Body: "Line one $HOME\nLine two"}},
		{"plain heredoc", "agenthail send @collab - <<EOF\nLine one\nEOF", OutgoingSend{Target: "@collab", Summary: "Line one", Body: "Line one"}},
		{"here string", `agenthail send @collab - <<< "short"`, OutgoingSend{Target: "@collab", Summary: "short", Body: "short"}},
		{"absolute binary path", `/usr/local/bin/agenthail send 0a35a199 "hey"`, OutgoingSend{Target: "0a35a199", Summary: "hey", Body: "hey"}},
		{"value flags", `agenthail send @peer "done" --model gpt-5 --effort high --mode plan --service-tier flex --output-schema /tmp/s.json --timeout 30s --from "$ME"`, OutgoingSend{Target: "@peer", Summary: "done", Body: "done"}},
		{"bool flags", `agenthail send @peer "done" --no-queue --stream`, OutgoingSend{Target: "@peer", Summary: "done", Body: "done"}},
		{"words after -- are the message", `agenthail send @peer -- --no-queue is a flag`, OutgoingSend{Target: "@peer", Summary: "--no-queue is a flag", Body: "--no-queue is a flag"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseOutgoingSend("Bash", bashInput(t, c.command))
			if !ok || got != c.want {
				t.Fatalf("ParseOutgoingSend(%q) = %+v, %v; want %+v", c.command, got, ok, c.want)
			}
		})
	}
}

func TestParseOutgoingSendLeavesOtherCommandsAlone(t *testing.T) {
	for _, command := range []string{
		`go test ./...`,
		`agenthail list --json`,
		`agenthail send @collab "$RESULT"`,
		`agenthail send @collab "$(cat out.txt)"`,
		`agenthail send @a "one" && agenthail send @b "two"`,
		`go test ./... && agenthail send @collab "ok"`,
		`agenthail send @collab "ok" || echo failed`,
		`echo hi | agenthail send @collab -`,
		`agenthail send @collab -`,
		`agenthail send @collab - <<EOF` + "\n$(date)\nEOF",
		`agenthail send @collab "ok" &`,
		`for x in a b; do agenthail send @collab "$x"; done`,
		`agenthail send @collab --unknown "ok"`,
		`agenthail send @collab "ok" --json --json`,
		`agenthail send @collab "ok" --model`,
		`agenthail send @collab "ok" --model --json`,
		`agenthail send @collab "ok" --model "--json"`,
		`agenthail send @collab`,
		`agenthail send @collab "unterminated`,
		`agenthail send @c* "ok"`,
	} {
		if got, ok := ParseOutgoingSend("Bash", bashInput(t, command)); ok {
			t.Errorf("ParseOutgoingSend(%q) = %+v; want a plain tool call", command, got)
		}
	}
}

func TestParseOutgoingSendReadsCodexShellCalls(t *testing.T) {
	got, ok := ParseOutgoingSend("exec_command", `{"cmd":"agenthail send @collab \"ready\"","workdir":"/work"}`)
	if !ok || got.Target != "@collab" || got.Body != "ready" {
		t.Fatalf("exec_command cmd = %+v, %v", got, ok)
	}
	got, ok = ParseOutgoingSend("shell", `{"command":["bash","-lc","agenthail send @collab ready"]}`)
	if !ok || got.Target != "@collab" || got.Body != "ready" {
		t.Fatalf("shell argv = %+v, %v", got, ok)
	}
	if _, ok := ParseOutgoingSend("shell", `{"command":["python3","-c","agenthail send @collab ready"]}`); ok {
		t.Fatal("a non-shell argv parsed as a send")
	}
}

func TestParseOutgoingSendReadsSendMessage(t *testing.T) {
	got, ok := ParseOutgoingSend("SendMessage", `{"to":"@collab","summary":"Audit result","message":"Long body"}`)
	if !ok || got != (OutgoingSend{Target: "@collab", Summary: "Audit result", Body: "Long body"}) {
		t.Fatalf("SendMessage = %+v, %v", got, ok)
	}
	if _, ok := ParseOutgoingSend("SendMessage", `{"message":"no target"}`); ok {
		t.Fatal("SendMessage without a target parsed")
	}
	if _, ok := ParseOutgoingSend("Bash", "not json"); ok {
		t.Fatal("non-JSON input parsed")
	}
}
