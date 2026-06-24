package main

import (
	"testing"
	"time"
)

func TestNextReturnsKey(t *testing.T) {
	p := NewKeyPool([]string{"key-a", "key-b", "key-c"})
	for i := 0; i < 3; i++ {
		idx, key, ok := p.Next()
		if !ok {
			t.Errorf("Next() returned ok=false on call %d", i)
		}
		if idx < 0 {
			t.Errorf("Next() returned index %d (expected >= 0) on call %d", idx, i)
		}
		if key == "" {
			t.Errorf("Next() returned empty key on call %d", i)
		}
	}
}

func TestNextAllCooldown(t *testing.T) {
	p := NewKeyPool([]string{"key-a", "key-b", "key-c"})
	// Put all keys on cooldown for 10 minutes
	for i := 0; i < 3; i++ {
		p.Cooldown(i, 10*time.Minute)
	}

	idx, key, ok := p.Next()
	if ok {
		t.Errorf("Next() returned ok=true when all keys are on cooldown")
	}
	if idx != -1 {
		t.Errorf("Next() returned index %d, want -1", idx)
	}
	if key != "" {
		t.Errorf("Next() returned key=%q, want empty", key)
	}
}

func TestDisableKey(t *testing.T) {
	p := NewKeyPool([]string{"key-a", "key-b", "key-c"})
	p.Disable(1)

	for i := 0; i < 10; i++ {
		idx, _, ok := p.Next()
		if !ok {
			t.Fatalf("Next() returned ok=false on iteration %d with 2 active keys", i)
		}
		if idx == 1 {
			t.Errorf("Next() returned disabled index 1 on iteration %d", i)
		}
	}
}

func TestAddKey(t *testing.T) {
	p := NewKeyPool([]string{"key-a", "key-b"})
	if n := p.ActiveCount(); n != 2 {
		t.Fatalf("ActiveCount() = %d, want 2 before AddKey", n)
	}

	idx := p.AddKey("new-key")
	if idx != 2 {
		t.Errorf("AddKey() returned index %d, want 2", idx)
	}
	if n := p.ActiveCount(); n != 3 {
		t.Errorf("ActiveCount() = %d after AddKey, want 3", n)
	}
}

func TestRemoveKey(t *testing.T) {
	p := NewKeyPool([]string{"key-a", "key-b", "key-c"})
	if n := p.ActiveCount(); n != 3 {
		t.Fatalf("ActiveCount() = %d, want 3 before RemoveKey", n)
	}

	err := p.RemoveKey(0)
	if err != nil {
		t.Fatalf("RemoveKey(0) returned error: %v", err)
	}
	if n := p.ActiveCount(); n != 2 {
		t.Errorf("ActiveCount() = %d after RemoveKey, want 2", n)
	}
}

func TestRemoveKeyOutOfRange(t *testing.T) {
	p := NewKeyPool([]string{"key-a", "key-b", "key-c"})
	err := p.RemoveKey(999)
	if err == nil {
		t.Error("RemoveKey(999) expected error, got nil")
	}
}

func TestNextEmptyPool(t *testing.T) {
	p := NewKeyPool([]string{})
	idx, key, ok := p.Next()
	if ok {
		t.Errorf("Next() returned ok=true for empty pool")
	}
	if idx != -1 {
		t.Errorf("Next() returned index %d, want -1", idx)
	}
	if key != "" {
		t.Errorf("Next() returned key=%q, want empty", key)
	}
}

func TestActiveCount(t *testing.T) {
	p := NewKeyPool([]string{"k1", "k2", "k3", "k4"})
	if n := p.ActiveCount(); n != 4 {
		t.Fatalf("ActiveCount() = %d, want 4", n)
	}

	p.Disable(0)
	if n := p.ActiveCount(); n != 3 {
		t.Errorf("ActiveCount() = %d after Disable(0), want 3", n)
	}

	p.Disable(1)
	if n := p.ActiveCount(); n != 2 {
		t.Errorf("ActiveCount() = %d after Disable(1), want 2", n)
	}
}