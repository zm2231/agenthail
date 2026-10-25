package surface

import "testing"

func TestParsePeerEnvelopeReadsEachWriter(t *testing.T) {
	cases := []struct {
		name, text string
		want       PeerEnvelope
		ok         bool
	}{
		{"send --from", "[from reviewer] Please rebase", PeerEnvelope{Sender: "reviewer", Body: "Please rebase"}, true},
		{"send --from with a qualified name", "[from agenthail/claude: @collab] Ship it\nnow", PeerEnvelope{Sender: "agenthail/claude: @collab", Body: "Ship it\nnow"}, true},
		{"channel post", "[from @builder via #repo:github.com/x/y] Tests pass", PeerEnvelope{Sender: "@builder", Channel: "repo:github.com/x/y", Body: "Tests pass"}, true},
		{"no writer omits the space after the label", "[from @builder via #ops]", PeerEnvelope{}, false},
		{"typed text shaped like a bare label", "[from reviewer]", PeerEnvelope{}, false},
		{"cli label", CLILabel + "run the suite", PeerEnvelope{Sender: CLISenderName, Body: "run the suite"}, true},
		{"plain prompt", "fix the build", PeerEnvelope{}, false},
		{"bracket later in the text", "see [from x] here", PeerEnvelope{}, false},
		{"empty label", "[from ] hi", PeerEnvelope{}, false},
		{"label across lines", "[from a\nb] hi", PeerEnvelope{}, false},
		{"unclosed", "[from reviewer please", PeerEnvelope{}, false},
		{"no space after the label", "[from reviewer]hi", PeerEnvelope{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParsePeerEnvelope(c.text)
			if ok != c.ok || got != c.want {
				t.Fatalf("ParsePeerEnvelope(%q) = %+v, %v; want %+v, %v", c.text, got, ok, c.want, c.ok)
			}
		})
	}
}
