package daemonapi

import "github.com/zm2231/agenthail/internal/ledger"

const WordsMaxAround = 20

type Words struct {
	ID         string                 `json:"id"`
	TargetID   string                 `json:"targetId"`
	At         string                 `json:"ts"`
	Host       string                 `json:"host"`
	Text       string                 `json:"text"`
	Typed      string                 `json:"typed"`
	Quoted     []string               `json:"quoted,omitempty"`
	MoreQuoted int                    `json:"moreQuoted,omitempty"`
	Context    *ledger.ContextPayload `json:"context,omitempty"`
	Anchor     string                 `json:"anchor,omitempty"`
	Turns      []WordsTurn            `json:"turns,omitempty"`
}

type WordsTurn struct {
	ItemID    string `json:"itemId"`
	Role      string `json:"role"`
	TS        string `json:"ts"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
	Message   bool   `json:"message,omitempty"`
}
