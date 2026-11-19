package pipeline

import (
	"math"
	"sync"
	"testing"
	"time"
)

func TestRetryPolicyUsesControlledJitter(t *testing.T) {
	options := testSenderOptions()
	values := []int{0, 20, 40, 0, 20, 40}
	next := 0
	options.RandomIntN = func(n int) int {
		if n != 41 {
			t.Errorf("random upper bound=%d, want 41", n)
		}
		value := values[next]
		next++
		return value
	}
	retry := newRetryPolicy(options)
	for _, test := range []struct {
		name    string
		attempt int
		want    time.Duration
	}{
		{"first lower", 1, 200 * time.Millisecond},
		{"first midpoint", 1, 250 * time.Millisecond},
		{"first upper", 1, 300 * time.Millisecond},
		{"past list lower", 7, 4 * time.Second},
		{"past list midpoint", 7, 5 * time.Second},
		{"past list upper", 7, 6 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := retry.delay(test.attempt); got != test.want {
				t.Fatalf("delay=%v, want %v", got, test.want)
			}
		})
	}
}

func TestRetryPolicySerializesInjectedRandom(t *testing.T) {
	options := testSenderOptions()
	next := 0
	options.RandomIntN = func(n int) int {
		value := next % n
		next++
		return value
	}
	retry := newRetryPolicy(options)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 32 {
				_ = retry.delay(1)
			}
		})
	}
	wg.Wait()
	if next != 512 {
		t.Fatalf("random calls=%d, want 512", next)
	}
}

func TestSenderRetryDurationSaturates(t *testing.T) {
	const maximumMS = math.MaxInt64 / (int64(time.Millisecond) * 120 / 100)
	base := time.Duration(maximumMS) * time.Millisecond
	if got, want := senderRetryDuration(base, 20), time.Duration(maximumMS*1200000); got != want {
		t.Fatalf("maximum safe retry duration=%s", got)
	}
	if got := senderRetryDuration(base+time.Millisecond, 20); got != time.Duration(math.MaxInt64) {
		t.Fatalf("overflow retry duration=%s, want saturated maximum", got)
	}
}
