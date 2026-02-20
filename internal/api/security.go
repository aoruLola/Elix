package api

import (
	"sync"
	"time"
)

type SecurityConfig struct {
	PairStartRateLimit             int
	PairStartRateWindow            time.Duration
	RefreshFailureAlertLimit       int
	RefreshFailureAlertWindow      time.Duration
	AuthFailureAlertLimit          int
	AuthFailureAlertWindow         time.Duration
	PairCompleteFailureAlertLimit  int
	PairCompleteFailureAlertWindow time.Duration
	BackendCallReadMethods         []string
	BackendCallCancelMethods       []string
	TrustedProxyCIDRs              []string
}

func defaultSecurityConfig() SecurityConfig {
	return SecurityConfig{
		PairStartRateLimit:             6,
		PairStartRateWindow:            60 * time.Second,
		RefreshFailureAlertLimit:       5,
		RefreshFailureAlertWindow:      2 * time.Minute,
		AuthFailureAlertLimit:          8,
		AuthFailureAlertWindow:         2 * time.Minute,
		PairCompleteFailureAlertLimit:  5,
		PairCompleteFailureAlertWindow: 2 * time.Minute,
		BackendCallReadMethods:         []string{"status"},
		BackendCallCancelMethods:       []string{"turn/interrupt"},
	}
}

func normalizeSecurityConfig(cfg SecurityConfig) SecurityConfig {
	def := defaultSecurityConfig()
	if cfg.PairStartRateLimit <= 0 {
		cfg.PairStartRateLimit = def.PairStartRateLimit
	}
	if cfg.PairStartRateWindow <= 0 {
		cfg.PairStartRateWindow = def.PairStartRateWindow
	}
	if cfg.RefreshFailureAlertLimit <= 0 {
		cfg.RefreshFailureAlertLimit = def.RefreshFailureAlertLimit
	}
	if cfg.RefreshFailureAlertWindow <= 0 {
		cfg.RefreshFailureAlertWindow = def.RefreshFailureAlertWindow
	}
	if cfg.AuthFailureAlertLimit <= 0 {
		cfg.AuthFailureAlertLimit = def.AuthFailureAlertLimit
	}
	if cfg.AuthFailureAlertWindow <= 0 {
		cfg.AuthFailureAlertWindow = def.AuthFailureAlertWindow
	}
	if cfg.PairCompleteFailureAlertLimit <= 0 {
		cfg.PairCompleteFailureAlertLimit = def.PairCompleteFailureAlertLimit
	}
	if cfg.PairCompleteFailureAlertWindow <= 0 {
		cfg.PairCompleteFailureAlertWindow = def.PairCompleteFailureAlertWindow
	}
	if len(cfg.BackendCallReadMethods) == 0 {
		cfg.BackendCallReadMethods = append([]string{}, def.BackendCallReadMethods...)
	}
	if len(cfg.BackendCallCancelMethods) == 0 {
		cfg.BackendCallCancelMethods = append([]string{}, def.BackendCallCancelMethods...)
	}
	if len(cfg.TrustedProxyCIDRs) > 0 {
		cfg.TrustedProxyCIDRs = append([]string{}, cfg.TrustedProxyCIDRs...)
	}
	return cfg
}

type windowLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	buckets map[string]*windowBucket
}

type windowCounter struct {
	mu      sync.Mutex
	window  time.Duration
	buckets map[string]*windowBucket
}

type windowBucket struct {
	start time.Time
	count int
}

func newWindowLimiter(limit int, window time.Duration) *windowLimiter {
	return &windowLimiter{
		limit:   limit,
		window:  window,
		buckets: map[string]*windowBucket{},
	}
}

func (l *windowLimiter) Allow(key string, now time.Time) (bool, int, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.buckets[key]
	if b == nil || now.Sub(b.start) >= l.window {
		b = &windowBucket{start: now, count: 0}
		l.buckets[key] = b
	}
	if b.count >= l.limit {
		retry := l.window - now.Sub(b.start)
		if retry < 0 {
			retry = 0
		}
		return false, b.count, retry
	}
	b.count++
	return true, b.count, 0
}

func newWindowCounter(window time.Duration) *windowCounter {
	return &windowCounter{
		window:  window,
		buckets: map[string]*windowBucket{},
	}
}

func (c *windowCounter) Inc(key string, now time.Time) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.buckets[key]
	if b == nil || now.Sub(b.start) >= c.window {
		b = &windowBucket{start: now, count: 0}
		c.buckets[key] = b
	}
	b.count++
	return b.count
}

func (c *windowCounter) Reset(key string) {
	c.mu.Lock()
	delete(c.buckets, key)
	c.mu.Unlock()
}
