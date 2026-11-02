package ledger

import (
	"encoding/json"
	"sort"
	"strings"
)

type gateState struct {
	lastSeq map[string]int64
	blocks  map[string]int
	waived  map[string]bool
	turns   map[string]GatePayload
}

func newGateState() gateState {
	return gateState{lastSeq: map[string]int64{}, blocks: map[string]int{}, waived: map[string]bool{}, turns: map[string]GatePayload{}}
}

func gateKey(sessionID, item string) string { return sessionID + "\x00" + item }

func (g gateState) applyGate(event Event) error {
	var p GatePayload
	if err := json.Unmarshal(event.Payload, &p); err != nil {
		return err
	}
	if p.Gate != GateG2 {
		return nil
	}
	if p.TurnID != "" {
		g.turns[gateKey(p.SessionID, p.TurnID)] = p
	}
	if p.Result == GateDeferredPerson {
		return nil
	}
	g.lastSeq[p.SessionID] = event.Seq
	if p.Result == GateBlocked {
		for _, item := range p.Blocked {
			g.blocks[gateKey(p.SessionID, item)]++
		}
	}
	return nil
}

type GateWaiver struct {
	SessionID string `json:"sessionId"`
	Item      string `json:"item"`
}

func (b *Board) G2Waivers() []GateWaiver {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]GateWaiver, 0, len(b.gates.waived))
	for key := range b.gates.waived {
		session, item, _ := strings.Cut(key, "\x00")
		out = append(out, GateWaiver{SessionID: session, Item: item})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Item+out[i].SessionID < out[j].Item+out[j].SessionID })
	return out
}

func (g gateState) applyWaiver(data []byte) error {
	var p WaiverPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	if p.Gate == GateG2 {
		g.waived[gateKey(p.SessionID, p.ClauseID)] = true
	}
	return nil
}
