package cache

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResponseCacheETagAndInvalidation(t *testing.T) {
	responseCache, err := New(2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	handler := responseCache.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte("response"))
	}))

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", nil))
	if first.Code != http.StatusOK || first.Header().Get("ETag") == "" || first.Body.String() != "response" {
		t.Fatalf("first response = %d, ETag %q, body %q", first.Code, first.Header().Get("ETag"), first.Body.String())
	}
	conditional := httptest.NewRequest(http.MethodGet, "/", nil)
	conditional.Header.Set("If-None-Match", first.Header().Get("ETag"))
	notModified := httptest.NewRecorder()
	handler.ServeHTTP(notModified, conditional)
	if notModified.Code != http.StatusNotModified || calls != 1 {
		t.Fatalf("cached conditional response = %d, calls %d; want 304 and one call", notModified.Code, calls)
	}
	responseCache.Clear()
	afterInvalidation := httptest.NewRecorder()
	handler.ServeHTTP(afterInvalidation, httptest.NewRequest(http.MethodGet, "/", nil))
	if afterInvalidation.Code != http.StatusOK || calls != 2 {
		t.Fatalf("response after invalidation = %d, calls %d; want fresh response", afterInvalidation.Code, calls)
	}
}

func TestResponseCacheDoesNotCachePersonalizedRequests(t *testing.T) {
	responseCache, err := New(2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	handler := responseCache.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte("private"))
	}))
	for range 2 {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.AddCookie(&http.Cookie{Name: "session", Value: "secret"})
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
	if calls != 2 {
		t.Fatalf("personalized requests hit cache: handler calls = %d, want 2", calls)
	}
}

func TestLimiterEnforcesBurstAndRefills(t *testing.T) {
	limiter, err := NewLimiter(1, time.Second, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if !limiter.Allow("client", now) || !limiter.Allow("client", now) {
		t.Fatal("limiter rejected a request inside the burst")
	}
	if limiter.Allow("client", now) {
		t.Fatal("limiter accepted a request over the burst")
	}
	if !limiter.Allow("client", now.Add(time.Second)) {
		t.Fatal("limiter did not refill")
	}
}
