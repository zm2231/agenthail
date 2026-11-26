package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/surface"
)

type absentCLIAgent struct {
	*runtimeCLISurface
}

func (absentCLIAgent) AgentLabel() string   { return "Codex" }
func (absentCLIAgent) LocateProgram() error { return errors.New("no codex command") }

type doctorAgentResult struct {
	OK               bool   `json:"ok"`
	Error            string `json:"error"`
	AgentOff         string `json:"agentOff"`
	AgentUnavailable string `json:"agentUnavailable"`
}

func runDoctorForAgent(t *testing.T, app *App) (doctorAgentResult, error) {
	t.Helper()
	output, err := captureStdout(t, func() error { return app.Run([]string{"doctor", "--json"}) })
	var payload struct {
		Surfaces []doctorAgentResult `json:"surfaces"`
	}
	if jsonErr := json.Unmarshal([]byte(output), &payload); jsonErr != nil || len(payload.Surfaces) != 1 {
		t.Fatalf("doctor output: %v %s", jsonErr, output)
	}
	return payload.Surfaces[0], err
}

func TestAnUnreachableAgentWithoutItsProgramIsUnavailableOnAutoOffWhenTurnedOffAndAFailureOnlyWhenTurnedOn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fake := &cliSurface{kind: surface.KindCodex}
	app, _ := cliFixture(t, fake)
	app.Surfaces[0].Surface = absentCLIAgent{&runtimeCLISurface{cliSurface: fake, status: surface.RuntimeStatus{Name: "Codex Desktop bridge", Reachable: true, Durable: true, Backend: "desktop"}}}
	app.daemonService = &fakeService{loaded: true}

	result, err := runDoctorForAgent(t, app)
	if err != nil || !result.OK || result.AgentUnavailable != "" || result.Error != "" {
		t.Fatalf("a Codex reached without its command on PATH is not unavailable: err=%v result=%+v", err, result)
	}

	fake.listErr = errors.New("Codex session discovery is unavailable")
	result, err = runDoctorForAgent(t, app)
	if err != nil || !result.OK || result.Error != "" || !strings.Contains(result.AgentUnavailable, "Codex is not installed") || result.AgentOff != "" {
		t.Fatalf("auto with Codex missing: err=%v result=%+v", err, result)
	}

	if _, err := captureStdout(t, func() error { return app.Run([]string{"agents", "use", "codex", "off"}) }); err != nil {
		t.Fatal(err)
	}
	result, err = runDoctorForAgent(t, app)
	if err != nil || !result.OK || !strings.Contains(result.AgentOff, "Codex is turned off") || result.AgentUnavailable != "" {
		t.Fatalf("off: err=%v result=%+v", err, result)
	}

	if _, err := captureStdout(t, func() error { return app.Run([]string{"agents", "use", "codex", "on"}) }); err != nil {
		t.Fatal(err)
	}
	result, err = runDoctorForAgent(t, app)
	if err == nil || result.OK || !strings.Contains(result.Error, "turned on but not installed") {
		t.Fatalf("on with Codex missing: err=%v result=%+v", err, result)
	}

	output, err := captureStdout(t, func() error { return app.Run([]string{"agents", "--json"}) })
	var listing struct {
		Agents []surface.AgentStatus `json:"agents"`
	}
	if err != nil || json.Unmarshal([]byte(output), &listing) != nil || len(listing.Agents) != 1 || listing.Agents[0].Mode != "on" || listing.Agents[0].State != surface.AgentMissing {
		t.Fatalf("agents --json: err=%v output=%s", err, output)
	}

	if _, err := captureStdout(t, func() error { return app.Run([]string{"agents", "use", "codex", "auto"}) }); err != nil {
		t.Fatal(err)
	}
	if result, err = runDoctorForAgent(t, app); err != nil || !result.OK || result.AgentUnavailable == "" {
		t.Fatalf("back to auto: err=%v result=%+v", err, result)
	}
}

func TestAgentsUseRefusesAnUnknownAgentOrSetting(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex}
	app, _ := cliFixture(t, fake)
	app.Surfaces[0].Surface = absentCLIAgent{&runtimeCLISurface{cliSurface: fake}}
	if err := app.Run([]string{"agents", "use", "devin", "off"}); err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("unknown agent: %v", err)
	}
	if err := app.Run([]string{"agents", "use", "codex", "disabled"}); err == nil || !strings.Contains(err.Error(), "auto, on or off") {
		t.Fatalf("unknown setting: %v", err)
	}
}

func TestAgentsReadTheirSettingDespiteAnUnrelatedMistakeInTheSettingsFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fake := &cliSurface{kind: surface.KindCodex}
	app, _ := cliFixture(t, fake)
	app.Surfaces[0].Surface = absentCLIAgent{&runtimeCLISurface{cliSurface: fake}}
	if err := os.MkdirAll(filepath.Dir(dashconfig.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dashconfig.Path(), []byte(`{"codexRecentHours":9999,"agentModes":{"codex":"off"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := captureStdout(t, func() error { return app.Run([]string{"agents", "--json"}) })
	var listing struct {
		Agents []surface.AgentStatus `json:"agents"`
	}
	if err != nil || json.Unmarshal([]byte(output), &listing) != nil || len(listing.Agents) != 1 || listing.Agents[0].State != surface.AgentOff {
		t.Fatalf("agents --json: err=%v output=%s", err, output)
	}
	result, _ := runDoctorForAgent(t, app)
	if !result.OK || result.AgentOff == "" {
		t.Fatalf("doctor: %+v", result)
	}
}
