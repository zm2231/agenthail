package surface

// Known is set once a turn start was read, so no call can be open unseen.
type CodexToolWindow struct {
	Known      bool
	Open       []CodexOpenCall
	Background []CodexBackgroundCommand
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
}
