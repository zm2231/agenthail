package surface

// CodexToolWindow is the Codex tool call awaiting its output while a
// transcript is read in order, and the background commands calls left running.
// Known is set once a turn start was read, so no call can be open unseen.
type CodexToolWindow struct {
	Known      bool
	CallID     string
	CallKey    string
	TurnID     string
	Commands   []string
	Consumed   bool
	Failed     bool
	Background []CodexBackgroundCommand
}

type CodexBackgroundCommand struct {
	CallID  string
	Command string
}
