package cache

import (
	"fmt"
	"sync"
	"time"
)

type loginState struct {
	failures     []time.Time
	blockedUntil time.Time
}

type LoginProtection struct {
	mu        sync.Mutex
	clients   map[string]loginState
	threshold int
	window    time.Duration
	cooldown  time.Duration
	capacity  int
}

func NewLoginProtection(threshold int, window, cooldown time.Duration, capacity int) (*LoginProtection, error) {
	if threshold < 1 || window <= 0 || cooldown <= 0 || capacity < 1 {
		return nil, fmt.Errorf("login protection settings must be positive")
	}
	return &LoginProtection{
		clients: make(map[string]loginState), threshold: threshold,
		window: window, cooldown: cooldown, capacity: capacity,
	}, nil
}

func (p *LoginProtection) Blocked(key string, now time.Time) (bool, time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune(now)
	state, exists := p.clients[key]
	if !exists {
		if len(p.clients) >= p.capacity {
			return true, p.cooldown
		}
		return false, 0
	}
	if state.blockedUntil.After(now) {
		return true, state.blockedUntil.Sub(now)
	}
	return false, 0
}

func (p *LoginProtection) Failed(key string, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune(now)
	state, exists := p.clients[key]
	if !exists {
		if len(p.clients) >= p.capacity {
			return true
		}
	}
	if state.blockedUntil.After(now) {
		return true
	}
	cutoff := now.Add(-p.window)
	failures := state.failures[:0]
	for _, failedAt := range state.failures {
		if failedAt.After(cutoff) {
			failures = append(failures, failedAt)
		}
	}
	state.failures = append(failures, now)
	if len(state.failures) >= p.threshold {
		state.failures = nil
		state.blockedUntil = now.Add(p.cooldown)
	}
	p.clients[key] = state
	return state.blockedUntil.After(now)
}

func (p *LoginProtection) Clear(key string) {
	p.mu.Lock()
	delete(p.clients, key)
	p.mu.Unlock()
}

func (p *LoginProtection) prune(now time.Time) {
	for key, state := range p.clients {
		if state.blockedUntil.After(now) {
			continue
		}
		cutoff := now.Add(-p.window)
		failures := state.failures[:0]
		for _, failedAt := range state.failures {
			if failedAt.After(cutoff) {
				failures = append(failures, failedAt)
			}
		}
		if len(failures) == 0 {
			delete(p.clients, key)
			continue
		}
		state.failures = failures
		state.blockedUntil = time.Time{}
		p.clients[key] = state
	}
}
