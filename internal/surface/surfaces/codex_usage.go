package surfaces

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	codexUsageTailBytes = int64(1 << 20)
	codexUsageRollouts  = 5
	codexUsageDays      = 8
)

var ErrNoCodexUsage = fmt.Errorf("no Codex usage recorded yet")

type codexUsageRead struct {
	identity string
	size     int64
	usage    *surface.Usage
}

type codexRateLimits struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Payload   struct {
		Type       string `json:"type"`
		RateLimits *struct {
			Primary   *codexRateWindow `json:"primary"`
			Secondary *codexRateWindow `json:"secondary"`
		} `json:"rate_limits"`
	} `json:"payload"`
}

type codexRateWindow struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int     `json:"window_minutes"`
	ResetsAt      int64   `json:"resets_at"`
}

// Usage is as fresh as the last Codex turn on this computer; FetchedAt says
// when that was.
func (c *Codex) Usage(ctx context.Context) (*surface.Usage, error) {
	for _, path := range newestCodexRollouts(filepath.Join(codexHome(), "sessions"), codexUsageRollouts) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if usage := c.rolloutUsage(ctx, path); usage != nil {
			return usage, nil
		}
	}
	return nil, ErrNoCodexUsage
}

func (c *Codex) rolloutUsage(ctx context.Context, path string) *surface.Usage {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	identity := transcriptFileIdentity(info)
	c.usageMu.Lock()
	cached := c.usageReads[path]
	c.usageMu.Unlock()
	if cached != nil && identity != "" && cached.identity == identity && cached.size == info.Size() {
		return cached.usage
	}
	lines, _, err := readRecentJSONLLines(ctx, path, 0, codexUsageTailBytes, maxCodexTranscriptRecordBytes)
	if err != nil {
		return nil
	}
	var usage *surface.Usage
	for index := len(lines) - 1; index >= 0 && usage == nil; index-- {
		usage = codexLineUsage(lines[index])
	}
	c.usageMu.Lock()
	if c.usageReads == nil {
		c.usageReads = map[string]*codexUsageRead{}
	}
	for kept := range c.usageReads {
		if len(c.usageReads) < codexUsageRollouts*2 {
			break
		}
		delete(c.usageReads, kept)
	}
	c.usageReads[path] = &codexUsageRead{identity: identity, size: info.Size(), usage: usage}
	c.usageMu.Unlock()
	return usage
}

func codexLineUsage(line []byte) *surface.Usage {
	if !bytes.Contains(line, []byte(`"rate_limits"`)) {
		return nil
	}
	var record codexRateLimits
	if json.Unmarshal(line, &record) != nil || record.Type != "event_msg" || record.Payload.Type != "token_count" || record.Payload.RateLimits == nil {
		return nil
	}
	usage := &surface.Usage{FetchedAt: record.Timestamp}
	for _, window := range []*codexRateWindow{record.Payload.RateLimits.Primary, record.Payload.RateLimits.Secondary} {
		if window == nil || window.WindowMinutes <= 0 {
			continue
		}
		limit := surface.UsageWindow{Kind: codexWindowKind(window.WindowMinutes), Percent: window.UsedPercent}
		if window.ResetsAt > 0 {
			limit.ResetsAt = time.Unix(window.ResetsAt, 0).UTC()
		}
		usage.Limits = append(usage.Limits, limit)
	}
	if len(usage.Limits) == 0 {
		return nil
	}
	sort.SliceStable(usage.Limits, func(i, j int) bool {
		return codexWindowMinutes(usage.Limits[i].Kind) < codexWindowMinutes(usage.Limits[j].Kind)
	})
	return usage
}

func codexWindowKind(minutes int) string {
	switch minutes {
	case 300:
		return "5h"
	case 10080:
		return "weekly"
	}
	return fmt.Sprintf("%dm", minutes)
}

func codexWindowMinutes(kind string) int {
	switch kind {
	case "5h":
		return 300
	case "weekly":
		return 10080
	}
	var minutes int
	fmt.Sscanf(strings.TrimSuffix(kind, "m"), "%d", &minutes)
	return minutes
}

// A long session keeps writing to the folder of the day it started, so the
// newest day folders are sorted by write time.
func newestCodexRollouts(root string, limit int) []string {
	var days []string
	years, _ := os.ReadDir(root)
	for index := len(years) - 1; index >= 0 && len(days) < codexUsageDays; index-- {
		months, _ := os.ReadDir(filepath.Join(root, years[index].Name()))
		for month := len(months) - 1; month >= 0 && len(days) < codexUsageDays; month-- {
			dayEntries, _ := os.ReadDir(filepath.Join(root, years[index].Name(), months[month].Name()))
			for day := len(dayEntries) - 1; day >= 0 && len(days) < codexUsageDays; day-- {
				if dayEntries[day].IsDir() {
					days = append(days, filepath.Join(root, years[index].Name(), months[month].Name(), dayEntries[day].Name()))
				}
			}
		}
	}
	type rollout struct {
		path    string
		written time.Time
	}
	var rollouts []rollout
	for _, day := range days {
		entries, _ := os.ReadDir(day)
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				continue
			}
			if info, err := entry.Info(); err == nil {
				rollouts = append(rollouts, rollout{path: filepath.Join(day, entry.Name()), written: info.ModTime()})
			}
		}
	}
	sort.Slice(rollouts, func(i, j int) bool { return rollouts[i].written.After(rollouts[j].written) })
	if len(rollouts) > limit {
		rollouts = rollouts[:limit]
	}
	paths := make([]string, len(rollouts))
	for index, rollout := range rollouts {
		paths[index] = rollout.path
	}
	return paths
}
