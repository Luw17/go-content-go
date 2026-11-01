package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type entry struct {
	status     int
	header     http.Header
	body       []byte
	etag       string
	expiresAt  time.Time
	lastAccess time.Time
}

type Cache struct {
	mu       sync.Mutex
	entries  map[string]entry
	capacity int
	ttl      time.Duration
}

func New(capacity int, ttl time.Duration) (*Cache, error) {
	if capacity < 1 || ttl <= 0 {
		return nil, fmt.Errorf("cache capacity and TTL must be positive")
	}
	return &Cache{entries: make(map[string]entry), capacity: capacity, ttl: ttl}, nil
}

func (c *Cache) Clear() {
	c.mu.Lock()
	clear(c.entries)
	c.mu.Unlock()
}

func (c *Cache) Middleware(next http.Handler) http.Handler {
	return c.MiddlewareWithKey(next, func(r *http.Request) string {
		return r.Host + r.URL.RequestURI()
	})
}

func (c *Cache) MiddlewareWithKey(next http.Handler, cacheKey func(*http.Request) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			next.ServeHTTP(w, r)
			return
		}
		key := cacheKey(r)
		if cached, ok := c.get(key); ok {
			writeCached(w, r, cached)
			return
		}
		recorder := newCaptureWriter()
		next.ServeHTTP(recorder, r)
		if recorder.status == http.StatusOK && recorder.header.Get("Set-Cookie") == "" &&
			!strings.Contains(strings.ToLower(recorder.header.Get("Cache-Control")), "no-store") {
			body := append([]byte(nil), recorder.body.Bytes()...)
			sum := sha256.Sum256(body)
			cached := entry{
				status:     recorder.status,
				header:     recorder.header.Clone(),
				body:       body,
				etag:       `"` + hex.EncodeToString(sum[:]) + `"`,
				expiresAt:  time.Now().Add(c.ttl),
				lastAccess: time.Now(),
			}
			c.set(key, cached)
			cached.header.Set("ETag", cached.etag)
			cached.header.Set("Cache-Control", "public, max-age="+strconv.Itoa(int(c.ttl.Seconds())))
			writeCached(w, r, cached)
			return
		}
		copyHeaders(w.Header(), recorder.header)
		w.WriteHeader(recorder.status)
		if _, err := w.Write(recorder.body.Bytes()); err != nil {
			log.Printf("write uncached response: %v", err)
		}
	})
}

func (c *Cache) get(key string) (entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.entries[key]
	if ok && time.Now().After(value.expiresAt) {
		delete(c.entries, key)
		return entry{}, false
	}
	if ok {
		value.lastAccess = time.Now()
		c.entries[key] = value
	}
	return value, ok
}

func (c *Cache) set(key string, value entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.capacity {
		oldestKey := ""
		var oldest time.Time
		for candidate, current := range c.entries {
			if oldestKey == "" || current.lastAccess.Before(oldest) {
				oldestKey, oldest = candidate, current.lastAccess
			}
		}
		delete(c.entries, oldestKey)
	}
	c.entries[key] = value
}

func writeCached(w http.ResponseWriter, r *http.Request, cached entry) {
	copyHeaders(w.Header(), cached.header)
	w.Header().Set("ETag", cached.etag)
	if etagMatches(r.Header.Get("If-None-Match"), cached.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(cached.status)
	if r.Method != http.MethodHead {
		if _, err := w.Write(cached.body); err != nil {
			log.Printf("write cached response: %v", err)
		}
	}
}

func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == etag || strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}

func copyHeaders(destination, source http.Header) {
	for name, values := range source {
		destination.Del(name)
		for _, value := range values {
			destination.Add(name, value)
		}
	}
}

type captureWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newCaptureWriter() *captureWriter {
	return &captureWriter{header: make(http.Header)}
}

func (w *captureWriter) Header() http.Header {
	return w.header
}

func (w *captureWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *captureWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(body)
}
