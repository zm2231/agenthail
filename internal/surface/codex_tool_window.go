package surface

// Known is set once a turn start was read, so no call can be open unseen.
type CodexToolWindow struct {
	Known      bool
	Open       []CodexOpenCall
	Background []CodexBackgroundCommand
	Inputs     []CodexOpenInput
}

// CodexOpenInput is a user message Codex recorded before the completion that
// names the client it came from.
type CodexOpenInput struct {
	Key    string
	TurnID string
	At     string
	Text   string
}

type CodexOpenCall struct {
	CallID   string
	CallKey  string
	TurnID   string
	At       string
	Tools    []string
	Commands []string
	Consumed bool
	Failed   bool
}

type CodexBackgroundCommand struct {
	CallID  string
	Command string
	At      string
}
