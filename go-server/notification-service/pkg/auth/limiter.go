package auth

import (
	"strings"
	"sync"
	"time"
)

// Defaults for the sign-in throttle.
const (
	DefaultMaxFailures = 5
	DefaultLockout     = 15 * time.Minute
	// maxTracked bounds memory when someone sprays many emails or addresses.
	maxTracked = 10_000
)

// Limiter locks a key (an email or an address) after repeated failed sign-ins.
// It is in process: with several replicas each keeps its own count, so the
// effective limit is higher. Failures age out after the lockout period.
type Limiter struct {
	mu       sync.Mutex
	max      int
	lockout  time.Duration
	failures map[string][]time.Time
	now      func() time.Time
}

// NewLimiter locks a key after max failures within the lockout window.
func NewLimiter(max int, lockout time.Duration) *Limiter {
	return &Limiter{max: max, lockout: lockout, failures: map[string][]time.Time{}, now: time.Now}
}

// Locked reports whether any of the keys is currently locked.
func (l *Limiter) Locked(keys ...string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		if len(l.recent(k)) >= l.maxFor(k) {
			return true
		}
	}
	return false
}

// ipFactor makes the per-address limit looser than the per-account one: an
// address is shared by a whole office or a proxy, and exists to catch one
// source trying many accounts, not one person mistyping.
const ipFactor = 5

func (l *Limiter) maxFor(key string) int {
	if strings.HasPrefix(key, "ip:") {
		return l.max * ipFactor
	}
	return l.max
}

// Fail records a failure against every key.
func (l *Limiter) Fail(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.failures) >= maxTracked {
		for k := range l.failures { // drop expired entries; if that is not enough, start over
			if len(l.recent(k)) == 0 {
				delete(l.failures, k)
			}
		}
		if len(l.failures) >= maxTracked {
			l.failures = map[string][]time.Time{}
		}
	}
	now := l.now()
	for _, k := range keys {
		l.failures[k] = append(l.recent(k), now)
	}
}

// Reset forgets a key after a successful sign-in.
func (l *Limiter) Reset(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		delete(l.failures, k)
	}
}

// recent returns the failures still inside the window and prunes the rest. Callers hold the lock.
func (l *Limiter) recent(key string) []time.Time {
	cutoff := l.now().Add(-l.lockout)
	list := l.failures[key]
	i := 0
	for i < len(list) && !list[i].After(cutoff) {
		i++
	}
	list = list[i:]
	if len(list) == 0 {
		delete(l.failures, key)
		return nil
	}
	l.failures[key] = list
	return list
}
