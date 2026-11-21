package registry

import (
	"errors"
	"testing"
	"time"
)

func TestTheRemoteEpochRisesOnEveryCredentialChangeAndOnNothingElse(t *testing.T) {
	r := openTestRegistry(t)
	if _, err := r.AddAgentIdentity("scout"); err != nil {
		t.Fatal(err)
	}
	last, err := r.RemoteEpoch()
	if err != nil {
		t.Fatal(err)
	}
	step := func(what string, rises bool, change func() error) {
		t.Helper()
		if err := change(); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		epoch, err := r.RemoteEpoch()
		if err != nil {
			t.Fatal(err)
		}
		if rises && epoch <= last || !rises && epoch != last {
			t.Fatalf("%s: epoch %d -> %d, want rises=%v", what, last, epoch, rises)
		}
		last = epoch
	}
	bind := func(clientID string) func() error {
		return func() error { _, _, err := r.BindRemoteIdentity("scout", clientID, ""); return err }
	}
	rotate := func() error { _, _, err := r.RotateRemoteIdentity("scout"); return err }
	step("bind", true, bind(""))
	step("rotate", true, rotate)
	step("rotate again at once", true, rotate)
	step("rebind to a client id", true, bind("client-a"))
	remote, err := r.RemoteIdentityNamed("scout")
	if err != nil {
		t.Fatal(err)
	}
	step("touch", false, func() error { return r.TouchRemoteIdentity(remote.IdentityID, time.Now().Add(time.Hour)) })
	step("set Access", true, func() error { return r.SetAccessSettings("example-team", "aud") })
	step("record a key read", false, func() error { return r.RecordAccessKeys(time.Now(), errors.New("down")) })
	step("set the same Access settings again", true, func() error { return r.SetAccessSettings("example-team", "aud") })
	step("clear Access", true, r.ClearAccessSettings)
	step("restore the same Access settings", true, func() error { return r.SetAccessSettings("example-team", "aud") })
	step("turn remote access off", true, func() error { return r.UnbindRemoteIdentity("scout") })
	step("bind again", true, bind(""))
	step("remove the identity", true, func() error { return r.RemoveAgentIdentity("scout") })
}
