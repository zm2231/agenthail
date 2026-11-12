package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/simhost"
)

type mcpSimulatorInput struct {
	Action    string            `json:"action" jsonschema:"list, acquire, renew, release, install, launch, terminate, openUrl, describe, tap, type, swipe, button, screenshot, push, location or logs."`
	Kind      string            `json:"kind,omitempty" jsonschema:"iphone or ipad; needed when you hold both."`
	App       string            `json:"app,omitempty" jsonschema:"install: a .app path inside your outDir."`
	BundleID  string            `json:"bundleId,omitempty" jsonschema:"The app's bundle id."`
	Args      []string          `json:"args,omitempty" jsonschema:"launch: arguments."`
	Env       map[string]string `json:"env,omitempty" jsonschema:"launch: environment."`
	URL       string            `json:"url,omitempty" jsonschema:"openUrl: a link or deep link."`
	Ref       string            `json:"ref,omitempty" jsonschema:"tap, swipe: a ref from the latest describe."`
	X         *float64          `json:"x,omitempty" jsonschema:"tap, swipe: x in points."`
	Y         *float64          `json:"y,omitempty" jsonschema:"tap, swipe: y in points."`
	ToX       *float64          `json:"toX,omitempty" jsonschema:"swipe: end x."`
	ToY       *float64          `json:"toY,omitempty" jsonschema:"swipe: end y."`
	Seconds   float64           `json:"seconds,omitempty" jsonschema:"swipe: duration; logs: how far back, at most 300."`
	Text      string            `json:"text,omitempty" jsonschema:"type: text for the focused field."`
	Button    string            `json:"button,omitempty" jsonschema:"home, lock or side-button."`
	Payload   json.RawMessage   `json:"payload,omitempty" jsonschema:"push: a JSON object with aps."`
	Latitude  *float64          `json:"latitude,omitempty" jsonschema:"location: with longitude; neither clears it."`
	Longitude *float64          `json:"longitude,omitempty" jsonschema:"location: with latitude."`
	Path      string            `json:"path,omitempty" jsonschema:"screenshot: a new absolute .png path in your folders."`
}

func (d *Daemon) mcpSimulator(ctx context.Context, caller *mcpCaller, input mcpSimulatorInput) (any, error) {
	if !caller.has("simulator.drive") {
		return nil, refuse("capability_missing", "Driving a simulator needs simulator.drive.")
	}
	if d.simHost == nil {
		return nil, refuse("unavailable", "This host runs no iOS simulators.")
	}
	if d.simHost.Local() {
		if status := d.simulatorStatus(ctx); !status.Ready {
			return nil, &mcpRefusal{Code: "unavailable", Message: "This host can't run iOS simulators: " + status.Problem + ".", Detail: map[string]any{"fix": status.Fix}}
		}
	}
	var save string
	if input.Action == "screenshot" && input.Path != "" {
		var err error
		if save, err = screenshotPath(caller, input.Path); err != nil {
			return nil, err
		}
	}
	timeout := time.Minute
	if input.Action == "acquire" {
		timeout = 11 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	limit := 0
	if input.Action == "describe" || input.Action == "logs" {
		limit = browserTextLimit
	}
	call := d.simHost.Call
	if !d.simHost.Local() {
		call = d.forwardSimulator
	}
	out, err := call(ctx, caller.session.ID, simhost.Request{Action: input.Action, Kind: input.Kind, App: input.App, BundleID: input.BundleID, Args: input.Args, Env: input.Env, URL: input.URL, Ref: input.Ref, X: input.X, Y: input.Y, ToX: input.ToX, ToY: input.ToY, Seconds: input.Seconds, Text: input.Text, Button: input.Button, Payload: input.Payload, Latitude: input.Latitude, Longitude: input.Longitude, Limit: limit})
	if err != nil {
		return nil, simulatorRefusal(err)
	}
	if file, _ := out["file"].(string); save != "" && file != "" {
		if err := saveScreenshot(file, save); err != nil {
			return nil, err
		}
		out["path"] = save
	}
	return out, nil
}

func (d *Daemon) simulatorStatus(ctx context.Context) simhost.Status {
	d.simStatusMu.Lock()
	defer d.simStatusMu.Unlock()
	if time.Since(d.simStatusAt) < time.Minute {
		return d.simStatus
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	d.simStatus, d.simStatusAt = d.simHost.Status(ctx), time.Now()
	return d.simStatus
}

func simulatorRefusal(err error) error {
	var refusal *simhost.Error
	if errors.As(err, &refusal) {
		return &mcpRefusal{Code: refusal.Code, Message: refusal.Message, Detail: refusal.Detail}
	}
	var command *simhost.CommandError
	if errors.As(err, &command) {
		message := command.Stderr
		if message == "" {
			message = command.Err.Error()
		}
		return &mcpRefusal{Code: "simulator_failed", Message: strings.TrimSpace(command.Command + ": " + message)}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &mcpRefusal{Code: "timeout", Message: "The simulator did not answer in time.", Retryable: true}
	}
	return err
}
