package installdir

import "testing"

func TestTheInstallDirectoryOfABundledExecutableHoldsTheBundle(t *testing.T) {
	for _, c := range []struct{ exe, dir, bundle string }{
		{"/Library/Application Support/Agenthail/Agenthail Daemon.app/Contents/MacOS/agenthail", "/Library/Application Support/Agenthail", "/Library/Application Support/Agenthail/Agenthail Daemon.app"},
		{"/home/me/.local/share/agenthail/agenthail", "/home/me/.local/share/agenthail", ""},
		{"/Applications/Agenthail.app/Contents/Resources/agenthail", "/Applications/Agenthail.app/Contents/Resources", ""},
		{"/opt/MacOS/agenthail", "/opt/MacOS", ""},
	} {
		if got := Of(c.exe); got != c.dir {
			t.Errorf("Of(%q) = %q, want %q", c.exe, got, c.dir)
		}
		if got := Bundle(c.exe); got != c.bundle {
			t.Errorf("Bundle(%q) = %q, want %q", c.exe, got, c.bundle)
		}
	}
}
