package cache

import (
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type bucket struct {
	tokens     float64
	lastUpdate time.Time
}

type Limiter struct {
	mu       sync.Mutex
	buckets  map[string]bucket
	rate     float64
	burst    float64
	capacity int
}

func NewLimiter(requests int, per time.Duration, burst int, capacity int) (*Limiter, error) {
	if requests < 1 || per <= 0 || burst < 1 || capacity < 1 {
		return nil, fmt.Errorf("rate limiter settings must be positive")
	}
	return &Limiter{
		buckets:  make(map[string]bucket),
		rate:     float64(requests) / per.Seconds(),
		burst:    float64(burst),
		capacity: capacity,
	}, nil
}

func (l *Limiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	current, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.capacity {
			l.prune(now)
		}
		if len(l.buckets) >= l.capacity {
			return false
		}
		current = bucket{tokens: l.burst, lastUpdate: now}
	}
	elapsed := now.Sub(current.lastUpdate).Seconds()
	if elapsed > 0 {
		current.tokens += elapsed * l.rate
		if current.tokens > l.burst {
			current.tokens = l.burst
		}
		current.lastUpdate = now
	}
	if current.tokens < 1 {
		l.buckets[key] = current
		return false
	}
	current.tokens--
	l.buckets[key] = current
	return true
}

func (l *Limiter) prune(now time.Time) {
	cutoff := now.Add(-time.Minute)
	for key, current := range l.buckets {
		if current.lastUpdate.Before(cutoff) {
			delete(l.buckets, key)
		}
	}
}

func (l *Limiter) Middleware(keyPrefix string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.Allow(keyPrefix+":"+clientIP(r), time.Now()) {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(1/l.rate))))
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}
