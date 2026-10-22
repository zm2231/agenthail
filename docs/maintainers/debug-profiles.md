# Daemon runtime profiles

`agenthail debug heap <dir>` writes the running daemon's heap, allocs and goroutine profiles to `<dir>`, one gzipped pprof file each (`heap-<UTC time>.pb.gz` and so on). Use it when the daemon's memory or goroutine count looks wrong and you need to see what holds it.

## How it reaches the daemon

The daemon serves the profiles on a Unix socket at `~/.agenthail/debug.sock` (`internal/daemon/debug_socket.go`). Nothing listens on a TCP port.

- The socket is mode 0600 inside `~/.agenthail`, which is 0700, so only the daemon's user can connect.
- The daemon starts it under the daemon lock. A socket left at the path by a daemon that is gone is replaced. A regular file at the path is left alone and the socket is not started.
- Only `GET /profiles/heap`, `/profiles/allocs` and `/profiles/goroutine` exist. The heap handler runs a garbage collection first, so the heap profile shows live memory.
- Each response must finish within a minute. Shutdown waits five seconds for open requests and then closes them.

The command needs a running daemon. It creates `<dir>` (0700) if needed and writes each file with mode 0600. It never overwrites an existing file, and it removes a file that failed to write completely.

## Reading a capture

```
go tool pprof -top -sample_index=inuse_space heap-<time>.pb.gz
go tool pprof -top -sample_index=alloc_space allocs-<time>.pb.gz
go tool pprof -traces goroutine-<time>.pb.gz
```

`inuse_space` is what the daemon holds now. `alloc_space` is everything it has allocated since it started, which shows the paths that churn memory even when they release it.
