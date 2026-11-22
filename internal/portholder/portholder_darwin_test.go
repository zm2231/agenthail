package portholder

import (
	"slices"
	"testing"
)

func TestParseLsofKeepsOnlySocketsWhoseLocalEndIsThePort(t *testing.T) {
	out := "p100\ncagenthail\nn127.0.0.1:7422\nTST=LISTEN\np200\nccurl\nn127.0.0.1:51000->127.0.0.1:7422\nTST=ESTABLISHED\np300\ncnode\nn127.0.0.1:7422->127.0.0.1:51000\nTST=ESTABLISHED\n"
	want := []Holder{{PID: 100, Command: "agenthail"}, {PID: 300, Command: "node"}}
	if got := parseLsof(out, 7422); !slices.Equal(got, want) {
		t.Fatalf("parseLsof = %+v, want %+v", got, want)
	}
}
