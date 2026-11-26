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
	Action    string            `json:"action" jsonschema:"list, acquire, renew, release, install, uninstall, launch, terminate, openUrl, describe, tap, type, key, swipe, button, screenshot, push, location or logs."`
	Kind      string            `json:"kind,omitempty" jsonschema:"iphone or ipad; needed when you hold both."`
	Runtime   string            `json:"runtime,omitempty" jsonschema:"acquire: an installed iOS version such as 18.5; without it, the shared device, whatever its version. Other actions: which held device. release with neither kind nor runtime ends every lease."`
	App       string            `json:"app,omitempty" jsonschema:"install: a .app in your outDir, or in your folders when the simulators are on another host."`
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
	Key       *int              `json:"key,omitempty" jsonschema:"key: one HID keycode, 0 to 255: 40 Return, 41 Escape, 42 Backspace, 43 Tab, 44 Space, 79 to 82 Right, Left, Down, Up, 4 to 29 a to z."`
	Keys      []int             `json:"keys,omitempty" jsonschema:"key: HID keycodes pressed in order, at most 100, instead of key."`
	Modifiers []int             `json:"modifiers,omitempty" jsonschema:"key: modifiers held while key is pressed: 224 Control, 225 Shift, 226 Option, 227 Command; [225] with key 43 is Shift-Tab."`
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
	if input.Action == "install" && !d.simHost.Local() {
		app, err := callerApp(caller, input.App)
		if err != nil {
			return nil, err
		}
		input.App = app
	}
	var save string
	if input.Action == "screenshot" && input.Path != "" {
		var err error
		if save, err = screenshotPath(caller, input.Path); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, input.mcpTimeout())
	defer cancel()
	limit := 0
	if input.Action == "describe" || input.Action == "logs" {
		limit = browserTextLimit
	}
	out, err := d.callSimulator(ctx, caller.session.ID, simhost.Request{Action: input.Action, Kind: input.Kind, Runtime: input.Runtime, App: input.App, BundleID: input.BundleID, Args: input.Args, Env: input.Env, URL: input.URL, Ref: input.Ref, X: input.X, Y: input.Y, ToX: input.ToX, ToY: input.ToY, Seconds: input.Seconds, Text: input.Text, Button: input.Button, Key: input.Key, Keys: input.Keys, Modifiers: input.Modifiers, Payload: input.Payload, Latitude: input.Latitude, Longitude: input.Longitude, Limit: limit})
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

const (
	simulatorStatusFresh = time.Minute
	simulatorProbeLimit  = 20 * time.Second
)

func (d *Daemon) simulatorStatus(ctx context.Context) simhost.Status {
	select {
	case <-d.probeSimulators():
	case <-ctx.Done():
	}
	d.simStatusMu.Lock()
	defer d.simStatusMu.Unlock()
	return d.simStatus
}

// probeSimulators runs outside simStatusMu so a reader of the last status
// never waits on xcrun, and a probe that runs out of time keeps the last one.
func (d *Daemon) probeSimulators() <-chan struct{} {
	d.simStatusMu.Lock()
	defer d.simStatusMu.Unlock()
	if d.simProbing != nil {
		return d.simProbing
	}
	done := make(chan struct{})
	if !d.simStatusAt.IsZero() && time.Since(d.simStatusAt) < simulatorStatusFresh {
		close(done)
		return done
	}
	d.simProbing = done
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), simulatorProbeLimit)
		status := d.simHost.Status(ctx)
		finished := ctx.Err() == nil
		cancel()
		d.simStatusMu.Lock()
		defer d.simStatusMu.Unlock()
		if finished {
			d.simStatus, d.simStatusAt = status, time.Now()
		}
		d.simProbing = nil
	}()
	return done
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

func (input mcpSimulatorInput) mcpTimeout() time.Duration {
	if input.Action == "acquire" || input.Action == "install" {
		return 11 * time.Minute
	}
	return time.Minute
}
