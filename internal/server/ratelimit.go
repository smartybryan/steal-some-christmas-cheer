package server

import (
	"sync"
	"time"
)

// rateLimiter allows at most limit events per key within window.
type rateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	events map[string][]time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, window: window, events: map[string][]time.Time{}}
}

func (rl *rateLimiter) allow(key string, now time.Time) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := now.Add(-rl.window)
	if len(rl.events) > 10_000 { // keep memory bounded
		for k, times := range rl.events {
			if len(times) == 0 || times[len(times)-1].Before(cutoff) {
				delete(rl.events, k)
			}
		}
	}
	recent := rl.events[key][:0]
	for _, t := range rl.events[key] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= rl.limit {
		rl.events[key] = recent
		return false
	}
	rl.events[key] = append(recent, now)
	return true
}
