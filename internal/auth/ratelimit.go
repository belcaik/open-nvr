package auth

import (
	"sync"
	"time"
)

// Limiter bloquea una IP tras Max intentos fallidos dentro de Window.
type Limiter struct {
	max    int
	window time.Duration
	now    func() time.Time
	mu     sync.Mutex
	fails  map[string][]time.Time
}

func NewLimiter(max int, window time.Duration, now func() time.Time) *Limiter {
	return &Limiter{max: max, window: window, now: now, fails: map[string][]time.Time{}}
}

// recent descarta los fallos fuera de la ventana. Requiere mu.
func (l *Limiter) recent(ip string) []time.Time {
	now := l.now()
	var keep []time.Time
	for _, t := range l.fails[ip] {
		if now.Sub(t) <= l.window {
			keep = append(keep, t)
		}
	}
	if keep == nil {
		delete(l.fails, ip)
	} else {
		l.fails[ip] = keep
	}
	return keep
}

func (l *Limiter) Blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(ip)) >= l.max
}

func (l *Limiter) Fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[ip] = append(l.recent(ip), l.now())
}

func (l *Limiter) Reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}
