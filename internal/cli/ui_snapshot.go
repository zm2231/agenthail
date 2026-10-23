package cli

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/atomicfile"
)

func (a *App) uiSnapshot(parsed uiArgs) error {
	if len(parsed.positional) != 1 {
		return errors.New(uiUsage)
	}
	out := parsed.positional[0]
	payload := map[string]any{}
	if parsed.session != "" {
		payload["session"] = parsed.session
	}
	if parsed.name != "" {
		payload["device"] = parsed.name
	}
	if parsed.size != "" {
		width, height, err := parseSnapshotSize(parsed.size)
		if err != nil {
			return err
		}
		payload["width"], payload["height"] = width, height
	}
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	image, _, err := client.call(ctx, http.MethodPost, "/api/v1/ui/snapshot", nil, payload, nil)
	if err != nil {
		return err
	}
	width, height, err := pngSize(image)
	if err != nil {
		return fmt.Errorf("the app sent an image that is not a PNG: %w", err)
	}
	if err := atomicfile.Write(out, image, 0o600); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "wrote %s (%dx%d pixels)\n", out, width, height)
	return nil
}

func parseSnapshotSize(text string) (int, int, error) {
	parts := strings.Split(strings.ToLower(text), "x")
	if len(parts) == 2 {
		width, werr := strconv.Atoi(parts[0])
		height, herr := strconv.Atoi(parts[1])
		if werr == nil && herr == nil && width > 0 && height > 0 && width <= 8192 && height <= 8192 {
			return width, height, nil
		}
	}
	return 0, 0, fmt.Errorf("--size must be <width>x<height> in points, each from 1 to 8192, like 1440x900")
}

func pngSize(image []byte) (int, int, error) {
	if len(image) < 24 || !bytes.HasPrefix(image, []byte("\x89PNG\r\n\x1a\n")) || string(image[12:16]) != "IHDR" {
		return 0, 0, errors.New("missing PNG header")
	}
	return int(binary.BigEndian.Uint32(image[16:20])), int(binary.BigEndian.Uint32(image[20:24])), nil
}
