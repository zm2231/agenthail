package daemon

import "sync"

type keyedLocks struct {
	mu   sync.Mutex
	held map[string]*keyedLock
}

type keyedLock struct {
	sync.Mutex
	refs int
}

// acquire locks key, or reports false at once when wait is false and key is already held or awaited; an entry lives only while someone holds or awaits it.
func (k *keyedLocks) acquire(key string, wait bool) (func(), bool) {
	k.mu.Lock()
	lock := k.held[key]
	if lock == nil {
		if k.held == nil {
			k.held = map[string]*keyedLock{}
		}
		lock = &keyedLock{}
		k.held[key] = lock
	}
	if !wait && lock.refs > 0 {
		k.mu.Unlock()
		return nil, false
	}
	lock.refs++
	k.mu.Unlock()
	lock.Lock()
	return func() {
		lock.Unlock()
		k.mu.Lock()
		defer k.mu.Unlock()
		if lock.refs--; lock.refs == 0 {
			delete(k.held, key)
		}
	}, true
}

func (k *keyedLocks) size() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.held)
}
