package claudehosttest

import (
	"context"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost"
)

func TestHostsExitOnceTheTestProcessThatOwnsThemIsGone(t *testing.T) {
	owner := exec.Command("sleep", "60")
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Process.Kill(); _ = owner.Wait() })

	f := New(t, "")
	launch := f.launch(t, "host.mjs", "1.0.0", "AGENTHAIL_TEST_OWNER_PIDS="+strconv.Itoa(owner.Process.Pid))
	manager := &claudehost.Manager{Home: f.Home, Launch: func() (claudehost.Launch, error) { return launch, nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := manager.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hello, err := client.Hello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Open(ctx, claudehost.OpenRequest{Cwd: f.Cwd})
	if err != nil {
		t.Fatal(err)
	}

	_ = owner.Process.Kill()
	_ = owner.Wait()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && (f.owns(hello.PID) || f.owns(session.PID)) {
		time.Sleep(50 * time.Millisecond)
	}
	for _, pid := range []int{hello.PID, session.PID} {
		if f.owns(pid) {
			t.Errorf("pid %d outlived the process that owns it", pid)
		}
	}
}
