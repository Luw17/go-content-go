package cache

import (
	"testing"
	"time"
)

func TestLoginProtectionStartsCooldownAfterThreshold(t *testing.T) {
	protection, err := NewLoginProtection(5, time.Minute, 15*time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for attempt := 1; attempt <= 4; attempt++ {
		if protection.Failed("client", now.Add(time.Duration(attempt)*time.Second)) {
			t.Fatalf("client blocked after only %d failures", attempt)
		}
	}
	fifthFailure := now.Add(5 * time.Second)
	if !protection.Failed("client", fifthFailure) {
		t.Fatal("client was not blocked after fifth failure")
	}
	blocked, remaining := protection.Blocked("client", fifthFailure.Add(time.Second))
	if !blocked || remaining < 14*time.Minute+time.Second {
		t.Fatalf("blocked state = (%v, %v), want a 15 minute cooldown", blocked, remaining)
	}
	blocked, _ = protection.Blocked("client", fifthFailure.Add(15*time.Minute))
	if blocked {
		t.Fatal("client remained blocked after cooldown expired")
	}
}

func TestLoginProtectionExpiresFailuresAndFailsClosedAtCapacity(t *testing.T) {
	protection, err := NewLoginProtection(2, time.Minute, 15*time.Minute, 1)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if protection.Failed("one", now) {
		t.Fatal("client blocked after first failure")
	}
	if protection.Failed("one", now.Add(2*time.Minute)) {
		t.Fatal("failure outside the sliding window counted")
	}
	if blocked, _ := protection.Blocked("another", now.Add(2*time.Minute)); !blocked {
		t.Fatal("protection did not fail closed at capacity")
	}
}

func TestLoginProtectionCanBeClearedAfterSuccessfulAuthentication(t *testing.T) {
	protection, err := NewLoginProtection(1, time.Minute, 15*time.Minute, 1)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if !protection.Failed("client", now) {
		t.Fatal("expected client to be blocked")
	}
	protection.Clear("client")
	if blocked, _ := protection.Blocked("client", now); blocked {
		t.Fatal("successful login did not clear client failures")
	}
}
