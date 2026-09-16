package surface

import "testing"

func TestEffectiveCapabilitiesMakeUnleasedZenSessionReadOnly(t *testing.T) {
	effective := EffectiveCapabilities(&Session{Surface: KindZen}, Capabilities{Send: true, Steer: true, Interrupt: true})
	if !effective.ReadOnly || effective.Send || effective.Steer || effective.Interrupt {
		t.Fatalf("effective=%+v", effective)
	}
	if effective.ReadOnlyReason == "" {
		t.Fatal("missing read-only reason")
	}
}

func TestEffectiveCapabilitiesKeepLeasedZenSessionWritable(t *testing.T) {
	effective := EffectiveCapabilities(&Session{Surface: KindZen, Transport: "unix:///tmp/zen.sock?controller=zen&generation=1"}, Capabilities{Send: true, Steer: true, Interrupt: true})
	if effective.ReadOnly || !effective.Send || !effective.Steer || !effective.Interrupt {
		t.Fatalf("effective=%+v", effective)
	}
}
