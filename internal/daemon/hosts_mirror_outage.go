package daemon

import (
	"errors"
	"fmt"
	"time"
)

const hostDownGrace = 20 * time.Second

type localMirrorError struct{ err error }

func (e localMirrorError) Error() string { return "local registry: " + e.err.Error() }

func (e localMirrorError) Unwrap() error { return e.err }

// catalogApplyError is a catalog event the follower could not apply. Its
// cursor stays before the event, and the mirror reconciles from a snapshot.
type catalogApplyError struct {
	host string
	seq  uint64
	err  error
}

func (e catalogApplyError) Error() string {
	return fmt.Sprintf("host %s catalog event %d: %s", e.host, e.seq, e.err)
}

func (e catalogApplyError) Unwrap() error { return e.err }

func localMirror(err error) error {
	if err == nil {
		return nil
	}
	return localMirrorError{err: err}
}

type hostOutage struct {
	since time.Time
	cause error
	down  bool
}

func (d *Daemon) hostAnswered(host *pairedHost, outage *hostOutage) {
	d.setHostState(host, hostStateConnected, "", true)
	if outage.since.IsZero() {
		return
	}
	lasted := time.Since(outage.since).Round(time.Millisecond)
	if outage.down {
		d.log.Printf("host %s reachable again after %s shown down: %v", host.record.Name, lasted, outage.cause)
	} else {
		d.log.Printf("host %s answered again after %s held as connected: %v", host.record.Name, lasted, outage.cause)
	}
	*outage = hostOutage{}
}

func (d *Daemon) hostFailed(host *pairedHost, outage *hostOutage, err error) {
	var local localMirrorError
	var unapplied catalogApplyError
	if errors.As(err, &local) || errors.As(err, &unapplied) {
		d.logRuntimeError("host "+host.record.Name+" mirror", err)
		return
	}
	now := time.Now()
	if outage.since.IsZero() {
		outage.since = now
	}
	outage.cause = err
	if !outage.down && hostFailureState(err) == hostStateUnreachable && now.Sub(outage.since) < d.federation.downGrace {
		return
	}
	if !outage.down {
		d.log.Printf("host %s shown %s after %s: %v", host.record.Name, hostFailureState(err), now.Sub(outage.since).Round(time.Millisecond), err)
	}
	outage.down = true
	d.markHostDown(host, err)
}
