package daemon

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/agenttools"
)

type mcpToolBudget struct {
	MCPToolListBytes    int      `json:"mcpToolListBytes"`
	AlwaysLoadToolBytes int      `json:"alwaysLoadToolBytes"`
	AlwaysLoadTools     []string `json:"alwaysLoadTools"`
}

func pinnedMCPToolBudget(t *testing.T) mcpToolBudget {
	t.Helper()
	data, err := os.ReadFile("../agenttools/budget.json")
	if err != nil {
		t.Fatal(err)
	}
	var budget mcpToolBudget
	if err := json.Unmarshal(data, &budget); err != nil {
		t.Fatal(err)
	}
	return budget
}

func TestTheToolListAgentsSeeStaysAtItsPinnedSize(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	pending, err := r.IssuePendingCredential(nil, time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := mcpSession(t, mcpServer(t, d), pending.Token).ListTools(t.Context(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	var specs []string
	for _, tool := range agenttools.Tools {
		specs = append(specs, tool.Name)
	}
	slices.Sort(names)
	slices.Sort(specs)
	if !slices.Equal(names, specs) {
		t.Fatalf("listed %v, specified %v", names, specs)
	}

	got := mcpToolBudget{AlwaysLoadTools: []string{}}
	all, err := json.Marshal(listed.Tools)
	if err != nil {
		t.Fatal(err)
	}
	got.MCPToolListBytes = len(all)
	for _, tool := range listed.Tools {
		if tool.Meta["anthropic/alwaysLoad"] != true {
			continue
		}
		encoded, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		got.AlwaysLoadToolBytes += len(encoded)
		got.AlwaysLoadTools = append(got.AlwaysLoadTools, tool.Name)
	}
	pinned := pinnedMCPToolBudget(t)
	slices.Sort(got.AlwaysLoadTools)
	slices.Sort(pinned.AlwaysLoadTools)
	if got.MCPToolListBytes != pinned.MCPToolListBytes || got.AlwaysLoadToolBytes != pinned.AlwaysLoadToolBytes || !slices.Equal(got.AlwaysLoadTools, pinned.AlwaysLoadTools) {
		t.Fatalf("the tool list agents see changed size; if that is intended, set internal/agenttools/budget.json to %+v (pinned %+v)", got, pinned)
	}
}
