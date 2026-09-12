package surfaces

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	claudeModelCatalogTTL      = time.Minute
	claudeModelCatalogDeadline = 15 * time.Second
	claudeModelCatalogMaxBytes = 8 << 20
)

type claudeModelsFlight struct {
	done   chan struct{}
	models []surface.ModelOption
	err    error
}

func cloneModelOptions(models []surface.ModelOption) []surface.ModelOption {
	if models == nil {
		return nil
	}
	cloned := make([]surface.ModelOption, len(models))
	for index, model := range models {
		cloned[index] = model
		cloned[index].SupportedReasoningEfforts = append([]string(nil), model.SupportedReasoningEfforts...)
		cloned[index].ServiceTiers = append([]string(nil), model.ServiceTiers...)
	}
	return cloned
}

type claudeRuntimeModel struct {
	Value           string   `json:"value"`
	DisplayName     string   `json:"displayName"`
	Description     string   `json:"description"`
	SupportedEffort []string `json:"supportedEffortLevels"`
	ResolvedModel   string   `json:"resolvedModel"`
}

type claudeInitializeResponse struct {
	Type     string `json:"type"`
	Response struct {
		RequestID string `json:"request_id"`
		Response  struct {
			Models []claudeRuntimeModel `json:"models"`
		} `json:"response"`
	} `json:"response"`
}

func parseClaudeRuntimeModels(data []byte, requestID string) ([]surface.ModelOption, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		if models, ok := parseClaudeRuntimeModelLine(scanner.Bytes(), requestID); ok {
			return models, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read Claude model catalog: %w", err)
	}
	return nil, fmt.Errorf("Claude initialize response did not contain model options")
}

func parseClaudeRuntimeModelLine(line []byte, requestID string) ([]surface.ModelOption, bool) {
	var message claudeInitializeResponse
	if json.Unmarshal(line, &message) != nil || message.Type != "control_response" || message.Response.RequestID != requestID {
		return nil, false
	}
	models := make([]surface.ModelOption, 0, len(message.Response.Response.Models))
	seen := make(map[string]struct{}, len(message.Response.Response.Models))
	for _, model := range message.Response.Response.Models {
		id := strings.TrimSpace(model.Value)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		label := strings.TrimSpace(model.DisplayName)
		if label == "" {
			label = id
		}
		models = append(models, surface.ModelOption{
			ID:                        id,
			DisplayName:               label,
			Description:               strings.TrimSpace(model.Description),
			Default:                   id == "default",
			AllowsCustom:              true,
			SupportedReasoningEfforts: append([]string(nil), model.SupportedEffort...),
		})
	}
	return models, true
}

func (c *Claude) Models(ctx context.Context) ([]surface.ModelOption, error) {
	c.modelsMu.Lock()
	if time.Since(c.modelsAt) < claudeModelCatalogTTL && c.modelsCache != nil {
		models := cloneModelOptions(c.modelsCache)
		c.modelsMu.Unlock()
		return models, nil
	}
	if flight := c.modelsFlight; flight != nil {
		c.modelsMu.Unlock()
		select {
		case <-flight.done:
			return cloneModelOptions(flight.models), flight.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	flight := &claudeModelsFlight{done: make(chan struct{})}
	c.modelsFlight = flight
	c.modelsMu.Unlock()

	models, err := c.loadModels(ctx)
	c.modelsMu.Lock()
	if err == nil {
		c.modelsCache = cloneModelOptions(models)
		c.modelsAt = time.Now()
	}
	flight.models = cloneModelOptions(models)
	flight.err = err
	c.modelsFlight = nil
	close(flight.done)
	c.modelsMu.Unlock()
	return models, err
}

func (c *Claude) loadModels(ctx context.Context) ([]surface.ModelOption, error) {
	binary := os.Getenv("AGENTHAIL_CLAUDE_BIN")
	if binary == "" {
		binary = "claude"
	}
	catalogCtx, catalogCancel := context.WithTimeout(ctx, claudeModelCatalogDeadline)
	defer catalogCancel()
	processCtx, cancel := context.WithCancel(catalogCtx)
	defer cancel()
	cmd := processGroupCommand(processCtx, binary,
		"--print", "--input-format", "stream-json", "--output-format", "stream-json",
		"--verbose", "--no-session-persistence", "--permission-prompts", "none")
	cmd.Dir = c.home
	cmd.Env = append(os.Environ(), "HOME="+c.home)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("start Claude model catalog: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("capture Claude model catalog: %w", err)
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start Claude model catalog: %w", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = stdout.Close()
		cancel()
		if cmd.Process != nil {
			_ = cmd.Cancel()
		}
		_ = cmd.Wait()
	}()
	const requestID = "agenthail-model-catalog"
	request := map[string]any{
		"type":       "control_request",
		"request_id": requestID,
		"request":    map[string]string{"subtype": "initialize"},
	}
	encoded, _ := json.Marshal(request)
	if _, err := stdin.Write(append(encoded, '\n')); err != nil {
		return nil, fmt.Errorf("send Claude initialize request: %w", err)
	}
	models, err := parseClaudeRuntimeModelsFromReader(processCtx, stdout, requestID)
	if err != nil {
		return nil, err
	}
	return models, nil
}

func parseClaudeRuntimeModelsFromReader(ctx context.Context, reader interface{ Read([]byte) (int, error) }, requestID string) ([]surface.ModelOption, error) {
	lines := make(chan []byte)
	errs := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case lines <- line:
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
		}
		errs <- scanner.Err()
	}()
	var totalBytes int
	for {
		select {
		case line := <-lines:
			totalBytes += len(line)
			if totalBytes > claudeModelCatalogMaxBytes {
				return nil, fmt.Errorf("Claude model catalog exceeded %d bytes", claudeModelCatalogMaxBytes)
			}
			if models, ok := parseClaudeRuntimeModelLine(line, requestID); ok {
				if len(models) == 0 {
					return nil, fmt.Errorf("Claude initialize response contained no model options")
				}
				return models, nil
			}
		case err := <-errs:
			if err != nil {
				return nil, fmt.Errorf("read Claude model catalog: %w", err)
			}
			return nil, fmt.Errorf("Claude initialize response did not contain model options")
		case <-ctx.Done():
			return nil, fmt.Errorf("Claude model catalog timed out: %w", ctx.Err())
		}
	}
}
