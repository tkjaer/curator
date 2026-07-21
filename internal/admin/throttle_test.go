package admin

import (
	"testing"
	"time"
)

func fixedThrottle() (*throttle, *time.Time) {
	now := time.Unix(0, 0)
	th := newThrottle()
	th.now = func() time.Time { return now }
	return th, &now
}

func TestBackoffProgression(t *testing.T) {
	th, _ := fixedThrottle()

	want := []time.Duration{
		0, 0, 0, // free attempts
		throttleBaseDelay,     // 4th
		2 * throttleBaseDelay, // 5th
		4 * throttleBaseDelay, // 6th
	}
	for i, w := range want {
		if got := th.fail("1.2.3.4"); got != w {
			t.Errorf("attempt %d delay = %v, want %v", i+1, got, w)
		}
	}
}

func TestGlobalBackstop(t *testing.T) {
	th, _ := fixedThrottle()

	// Each distinct IP fails once, so per-IP backoff never triggers; only the
	// global counter accumulates.
	var last time.Duration
	for i := 0; i < throttleGlobalFree+2; i++ {
		last = th.fail(ipf(i))
	}
	if last == 0 {
		t.Error("expected global backstop to add delay once total failures exceed the global free limit")
	}
}

func TestDecayForgivesOverTime(t *testing.T) {
	th, now := fixedThrottle()

	for i := 0; i < 5; i++ {
		th.fail("9.9.9.9") // count = 5, delayed
	}
	// Wait long enough to forgive several failures.
	*now = now.Add(3 * throttleDecay)
	if d := th.fail("9.9.9.9"); d != 0 {
		t.Errorf("after decay, delay = %v, want 0", d)
	}
}

func TestSuccessResetsClient(t *testing.T) {
	th, _ := fixedThrottle()
	for i := 0; i < 5; i++ {
		th.fail("5.5.5.5")
	}
	th.success("5.5.5.5")
	if d := th.fail("5.5.5.5"); d != 0 {
		t.Errorf("after success reset, delay = %v, want 0", d)
	}
}

func ipf(n int) string {
	return "10.0." + itoa(n/256) + "." + itoa(n%256)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
