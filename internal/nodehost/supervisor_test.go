package nodehost

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/testnode"
)

const slowHelloHost = `
import net from 'node:net';
import readline from 'node:readline';
import { parseArgs } from 'node:util';
const { values: args } = parseArgs({ options: { socket: { type: 'string' }, version: { type: 'string' } } });
let answered = 0;
readline.createInterface({ input: process.stdin }).once('line', () => {
  net.createServer(socket => {
    readline.createInterface({ input: socket }).on('line', line => {
      const { id } = JSON.parse(line);
      const reply = () => socket.write(JSON.stringify({ id, result: { protocol: 1, hostVersion: args.version, pid: process.pid } }) + '\n');
      if (answered++ === 0) setTimeout(reply, 1500);
      else reply();
    });
  }).listen(args.socket);
});
process.stdin.on('end', () => process.exit(0));
`

func TestAHostThatAnswersItsFirstHelloLateIsKept(t *testing.T) {
	dir, err := os.MkdirTemp("", "nh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	script := filepath.Join(dir, "host.mjs")
	if err := os.WriteFile(script, []byte(slowHelloHost), 0o600); err != nil {
		t.Fatal(err)
	}
	ready := make(chan Hello, 1)
	down := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	s := &Supervisor{
		Name:     "test host",
		StateDir: filepath.Join(dir, "state"),
		Socket:   filepath.Join(dir, "h.sock"),
		Protocol: 1,
		Launch: func() (Launch, error) {
			return Launch{Node: testnode.Path(t), Script: script, Version: "test", Env: os.Environ()}, nil
		},
		Ready: func(ctx context.Context, client Client) error {
			hello, err := client.Hello(ctx)
			if err == nil {
				ready <- hello
			}
			return err
		},
		Down:       func(err error) { down <- err },
		MinRestart: time.Hour,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	select {
	case <-ready:
	case err := <-down:
		t.Fatalf("the supervisor stopped a host that answered its first hello late: %v", err)
	}
}
