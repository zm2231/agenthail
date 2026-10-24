package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/zm2231/agenthail/internal/poll"
	"github.com/zm2231/agenthail/internal/registry"
)

const devicesUsage = `usage: agenthail devices [list] [--json]
       agenthail devices pair [--name <label>] [--json]
       agenthail devices remove <device>`

var devicePairPoll = time.Second

func (a *App) cmdDevices(args []string) error {
	if hasFlag(args, "--help") {
		fmt.Println(devicesUsage)
		return nil
	}
	positional := stripFlags(args)
	sub := "list"
	if len(positional) > 0 {
		sub, positional = positional[0], positional[1:]
	}
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	switch sub {
	case "list", "ls":
		if len(positional) != 0 {
			return errors.New(devicesUsage)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return devicesList(ctx, client, hasFlag(args, "--json"))
	case "pair":
		if len(positional) != 0 {
			return errors.New(devicesUsage)
		}
		return devicesPair(client, flagVal(args, "--name"), hasFlag(args, "--json"), os.Stdout)
	case "remove", "rm":
		if len(positional) != 1 {
			return errors.New(devicesUsage)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return devicesRemove(ctx, client, positional[0])
	default:
		return errors.New(devicesUsage)
	}
}

func listedDevices(ctx context.Context, client *daemonAPIClient) ([]registry.Device, error) {
	var listing struct {
		Devices []registry.Device `json:"devices"`
	}
	if err := client.getJSON(ctx, "/api/v1/devices", nil, &listing); err != nil {
		return nil, err
	}
	devices := listing.Devices[:0]
	for _, device := range listing.Devices {
		if !device.IsHost() {
			devices = append(devices, device)
		}
	}
	return devices, nil
}

func devicesList(ctx context.Context, client *daemonAPIClient, jsonOut bool) error {
	devices, err := listedDevices(ctx, client)
	if err != nil {
		return err
	}
	if jsonOut {
		return emit(map[string]any{"devices": devices})
	}
	if len(devices) == 0 {
		fmt.Println("no paired devices; pair an iPhone with 'agenthail devices pair'")
		return nil
	}
	fmt.Printf("%-24s %-36s %-13s %s\n", "DEVICE", "ID", "LAST SEEN", "NOTIFICATIONS")
	for _, device := range devices {
		seen := "never"
		if at, err := time.Parse(time.RFC3339Nano, device.LastSeenAt); err == nil {
			seen = relTime(at)
		}
		notifications := "off"
		if device.PushEnabled {
			notifications = "on"
		}
		fmt.Printf("%-24s %-36s %-13s %s\n", truncate(device.Name, 24), device.ID, seen, notifications)
	}
	return nil
}

func devicesRemove(ctx context.Context, client *daemonAPIClient, target string) error {
	var listing struct {
		Devices []registry.Device `json:"devices"`
	}
	if err := client.getJSON(ctx, "/api/v1/devices", nil, &listing); err != nil {
		return err
	}
	var matches []registry.Device
	for _, device := range listing.Devices {
		if device.ID == target {
			matches = []registry.Device{device}
			break
		}
		if device.Name == target {
			matches = append(matches, device)
		}
	}
	switch {
	case len(matches) == 0:
		return fmt.Errorf("no paired device is %q; see agenthail devices list", target)
	case len(matches) > 1:
		ids := make([]string, 0, len(matches))
		for _, device := range matches {
			ids = append(ids, device.ID)
		}
		return fmt.Errorf("%d devices are named %q; remove one by id: %s", len(matches), target, strings.Join(ids, ", "))
	case matches[0].IsHost():
		return fmt.Errorf("%s is a paired host; remove it with agenthail hosts remove on the host that paired it", target)
	}
	if _, _, err := client.call(ctx, http.MethodDelete, "/api/v1/devices", nil, map[string]string{"id": matches[0].ID}, nil); err != nil {
		return fmt.Errorf("remove device: %w", err)
	}
	fmt.Printf("removed %s; it can no longer reach this host\n", matches[0].Name)
	return nil
}

type devicePairing struct {
	ID         string    `json:"id"`
	PairingURL string    `json:"pairingURL"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

func devicesPair(client *daemonAPIClient, name string, jsonOut bool, out io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	data, _, err := client.call(ctx, http.MethodPost, "/api/v1/pairings", nil, map[string]any{"name": name, "scopes": []string{"read", "control"}}, nil)
	if err != nil {
		return fmt.Errorf("create pairing code: %w", err)
	}
	var pairing devicePairing
	if err := json.Unmarshal(data, &pairing); err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(out).Encode(pairing)
	}
	code, err := qrcode.New(pairing.PairingURL, qrcode.Low)
	if err != nil {
		return fmt.Errorf("draw pairing code: %w", err)
	}
	fmt.Fprintf(out, "Scan this code with the iPhone's camera, or with Scan pairing code in Agenthail, before %s:\n\n", pairing.ExpiresAt.Local().Format(time.Kitchen))
	fmt.Fprint(out, terminalQR(code.ToSmallString(false), isTerminal(out)))
	fmt.Fprintf(out, "\nOr open this link on the iPhone:\n  %s\n\nWaiting for the iPhone to pair (Ctrl-C to stop; the code expires on its own)...\n", pairing.PairingURL)
	device, err := awaitPairing(client, pairing)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "paired %s\n", device)
	return nil
}

// terminalQR paints the code light-on-dark itself when it reaches a
// terminal, since a light terminal theme would otherwise invert it.
func terminalQR(code string, terminal bool) string {
	if !terminal {
		return code
	}
	var painted strings.Builder
	for _, line := range strings.SplitAfter(code, "\n") {
		if line == "" {
			continue
		}
		painted.WriteString("\x1b[97;40m" + strings.TrimSuffix(line, "\n") + "\x1b[0m\n")
	}
	return painted.String()
}

func isTerminal(out io.Writer) bool {
	file, ok := out.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func awaitPairing(client *daemonAPIClient, pairing devicePairing) (string, error) {
	name := ""
	err := poll.Until(context.Background(), devicePairPoll, func() (bool, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		var status registry.DevicePairingStatus
		err := client.getJSON(ctx, "/api/v1/pairings", url.Values{"id": {pairing.ID}}, &status)
		cancel()
		if err != nil {
			return false, fmt.Errorf("check pairing: %w", err)
		}
		switch status.State {
		case registry.PairingPaired:
			if status.Device == nil {
				name = "a device"
			} else {
				name = status.Device.Name
			}
			return true, nil
		case registry.PairingExpired:
			return false, errors.New("the pairing code expired before a device used it; run agenthail devices pair again")
		}
		return false, nil
	})
	return name, err
}
