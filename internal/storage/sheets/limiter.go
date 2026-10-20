// SPDX-License-Identifier: Apache-2.0

package sheets

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/mayura-andrew/eventpass/internal/storage"
)

// limiter keeps requests under a per-minute quota. Tokens refill evenly; a
// request of priority p may only take a token if at least reserve[p] tokens
// stay behind, so background work stops first and scanners last.
type limiter struct {
	mu      sync.Mutex
	l       *rate.Limiter
	burst   float64
	now     func() time.Time
	sleep   func(context.Context, time.Duration) error
	history []time.Time // grants in the last minute, for the health page
}

func newLimiter(perMin int) *limiter {
	burst := max(perMin/4, 4)
	return &limiter{
		l:     rate.NewLimiter(rate.Limit(float64(perMin)/60), burst),
		burst: float64(burst),
		now:   time.Now,
		sleep: sleepCtx,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// reserveShare is the fraction of the burst each priority must leave.
var reserveShare = map[storage.Priority]float64{
	storage.PriorityBackground: 0.5,
	storage.PriorityAdmin:      0.25,
	storage.PriorityPortal:     0.1,
	storage.PriorityScanner:    0,
}

// Wait blocks until the request may go ahead or ctx ends.
func (lm *limiter) Wait(ctx context.Context) error {
	reserve := reserveShare[storage.PriorityFrom(ctx)] * lm.burst
	for {
		lm.mu.Lock()
		now := lm.now()
		tokens := lm.l.TokensAt(now)
		if tokens-1 >= reserve && lm.l.AllowN(now, 1) {
			lm.history = append(lm.history, now)
			lm.mu.Unlock()
			return nil
		}
		need := reserve + 1 - tokens
		wait := time.Duration(need / float64(lm.l.Limit()) * float64(time.Second))
		lm.mu.Unlock()
		if err := lm.sleep(ctx, min(max(wait, 10*time.Millisecond), time.Second)); err != nil {
			return err
		}
	}
}

// UsedLastMinute counts grants in the last 60 seconds.
func (lm *limiter) UsedLastMinute() int {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	cut := lm.now().Add(-time.Minute)
	i := 0
	for i < len(lm.history) && lm.history[i].Before(cut) {
		i++
	}
	lm.history = lm.history[i:]
	return len(lm.history)
}
