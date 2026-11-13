package daemon

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/extension"
)

const (
	extensionBadgePath  = "/extension/badge"
	extensionBadgeEvent = "extension.badge"
	maxExtensionBadge   = 8
)

var extensionBadgeInterval = 2 * time.Second

type extensionBadges struct {
	mu      sync.Mutex
	values  map[string]string
	sent    map[string]time.Time
	pending map[string]*time.Timer
}

func extensionBadgeKey(id, mount string) string {
	if mount == "" {
		return id
	}
	return extension.MountKey(id, mount)
}

func (b *extensionBadges) get(id, mount string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.values[extensionBadgeKey(id, mount)]
}

func (b *extensionBadges) set(id, mount, value string, publish func(id string)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.values == nil {
		b.values, b.sent, b.pending = map[string]string{}, map[string]time.Time{}, map[string]*time.Timer{}
	}
	key := extensionBadgeKey(id, mount)
	if b.values[key] == value {
		return
	}
	if value == "" {
		delete(b.values, key)
	} else {
		b.values[key] = value
	}
	if b.pending[id] != nil {
		return
	}
	wait := extensionBadgeInterval - time.Since(b.sent[id])
	if wait <= 0 {
		b.sent[id] = time.Now()
		go publish(id)
		return
	}
	b.pending[id] = time.AfterFunc(wait, func() {
		b.mu.Lock()
		delete(b.pending, id)
		b.sent[id] = time.Now()
		b.mu.Unlock()
		publish(id)
	})
}

func extensionBadgeValue(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		count, err := strconv.ParseInt(number.String(), 10, 64)
		if err != nil || count < 0 {
			return "", errors.New("a badge number is a whole number of 0 or more")
		}
		if count == 0 {
			return "", nil
		}
		if count > 9999 {
			return "9999+", nil
		}
		return strconv.FormatInt(count, 10), nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", errors.New("a badge is a short string, a whole number or null")
	}
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) > maxExtensionBadge {
		return "", errors.New("a badge is at most 8 characters")
	}
	return text, nil
}

func (d *Daemon) extensionBadgeHandler() http.Handler {
	return http.NewCrossOriginProtection().Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localReach(r) {
			writeAPIError(w, http.StatusForbidden, "remote_refused", "Extension credentials are accepted only from this machine.")
			return
		}
		id, _, ok := d.extensions.extensionByToken(bearerToken(r))
		if !ok {
			writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "Only a loaded extension may set its badge.")
			return
		}
		if r.Method != http.MethodPost {
			writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST.")
			return
		}
		var request struct {
			Mount string          `json:"mount"`
			Value json.RawMessage `json:"value"`
		}
		if err := decodeAPIV1JSON(w, r, &request, false); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid", "The badge request is not valid JSON: "+err.Error())
			return
		}
		value, err := extensionBadgeValue(request.Value)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		if request.Mount != "" {
			loaded, found := d.extensions.find(id)
			if !found || !slices.ContainsFunc(loaded.ext.Manifest.Mounts, func(mount extension.Mount) bool { return mount.Name == request.Mount }) {
				writeAPIError(w, http.StatusNotFound, "not_found", "Extension "+id+" declares no mount "+request.Mount+".")
				return
			}
		}
		d.extensions.badges.set(id, request.Mount, value, func(id string) {
			d.publishEvent(extensionBadgeEvent, id, map[string]any{"id": id})
		})
		writeDashboardJSON(w, http.StatusOK, map[string]string{"badge": value})
	}))
}
