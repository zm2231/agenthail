package cli

import "testing"

func TestManagedCodexLaunchArgsExtractsOnlyLauncherContract(t *testing.T) {
	model, message, err := managedCodexLaunchArgs([]string{"--cd", "/work", "--model", "gpt-5.6-sol", "--", "Build the release"})
	if err != nil || model != "gpt-5.6-sol" || message != "Build the release" {
		t.Fatalf("model=%q message=%q err=%v", model, message, err)
	}
}

func TestManagedCodexLaunchArgsRejectsUnforwardedOptions(t *testing.T) {
	if _, _, err := managedCodexLaunchArgs([]string{"--resume", "old"}); err == nil {
		t.Fatal("unsupported launcher option was accepted")
	}
}
