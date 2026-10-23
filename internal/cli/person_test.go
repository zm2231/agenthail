package cli

import (
	"encoding/json"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestPersonNameIsTheNameSetElseTheAccountsName(t *testing.T) {
	app, _ := cliFixture(t, &cliSurface{kind: surface.KindCodex})
	read := func(args ...string) (name string, set bool) {
		t.Helper()
		output, err := captureStdout(t, func() error { return app.Run(append([]string{"person", "name"}, append(args, "--json")...)) })
		var got struct {
			Name string `json:"name"`
			Set  bool   `json:"set"`
		}
		if err != nil || json.Unmarshal([]byte(output), &got) != nil {
			t.Fatalf("person name %v: output=%q err=%v", args, output, err)
		}
		return got.Name, got.Set
	}
	account, set := read()
	if account == "" || set {
		t.Fatalf("unset name=%q set=%v", account, set)
	}
	if name, set := read("Ada", "Lovelace"); name != "Ada Lovelace" || !set {
		t.Fatalf("set name=%q set=%v", name, set)
	}
	if name, set := read(); name != "Ada Lovelace" || !set {
		t.Fatalf("stored name=%q set=%v", name, set)
	}
	if name, set := read("--reset"); name != account || set {
		t.Fatalf("reset name=%q set=%v, want %q", name, set, account)
	}
}
