package admin

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Login throttling: exponential backoff on repeated failures, keyed per client
// IP with a global backstop so a botnet sending one request per IP is still
// slowed. It never hard-locks (a mistyping admin only waits a few seconds), and
// state is in-memory (an attacker cannot restart the process to clear it).
const (
	throttleFreeAttempts  = 3  // failures before a client is delayed
	throttleGlobalFree    = 10 // total failures before the global backstop engages
	throttleBaseDelay     = 500 * time.Millisecond
	throttleMaxDelay      = 20 * time.Second
	throttleDecay         = 30 * time.Second // one failure forgiven per interval of quiet
	throttleMaxConcurrent = 20               // cap simultaneous delayed logins
	throttlePruneAt       = 4096             // prune the per-IP map beyond this size
)

type attempts struct {
	count    int
	lastSeen time.Time
}

type throttle struct {
	mu     sync.Mutex
	perIP  map[string]*attempts
	global attempts
	now    func() time.Time
}

func newThrottle() *throttle {
	return &throttle{perIP: make(map[string]*attempts), now: time.Now}
}

// fail records a failed attempt for ip and returns how long to delay the
// response.
func (t *throttle) fail(ip string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.perIP) > throttlePruneAt {
		t.prune()
	}

	a := t.perIP[ip]
	if a == nil {
		a = &attempts{}
		t.perIP[ip] = a
	}
	t.decay(a)
	t.decay(&t.global)

	a.count++
	a.lastSeen = t.now()
	t.global.count++
	t.global.lastSeen = t.now()

	return maxDuration(
		backoff(a.count, throttleFreeAttempts),
		backoff(t.global.count, throttleGlobalFree),
	)
}

// success clears a client's failure history. The global counter is left to
// decay on its own so a single success cannot wipe broader attack pressure.
func (t *throttle) success(ip string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.perIP, ip)
}

func (t *throttle) decay(a *attempts) {
	if a.count == 0 {
		return
	}
	forgiven := int(t.now().Sub(a.lastSeen) / throttleDecay)
	if forgiven <= 0 {
		return
	}
	a.count -= forgiven
	if a.count < 0 {
		a.count = 0
	}
	a.lastSeen = a.lastSeen.Add(time.Duration(forgiven) * throttleDecay)
}

func (t *throttle) prune() {
	for ip, a := range t.perIP {
		t.decay(a)
		if a.count == 0 {
			delete(t.perIP, ip)
		}
	}
}

func backoff(count, free int) time.Duration {
	if count <= free {
		return 0
	}
	shift := count - free - 1
	if shift > 20 {
		return throttleMaxDelay
	}
	d := throttleBaseDelay << uint(shift)
	if d <= 0 || d > throttleMaxDelay {
		return throttleMaxDelay
	}
	return d
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

// clientIP identifies the caller, honoring the first X-Forwarded-For hop only
// when running behind a trusted proxy.
func (s *Server) clientIP(r *http.Request) string {
	if s.trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			if ip := strings.TrimSpace(first); ip != "" {
				return ip
			}
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
