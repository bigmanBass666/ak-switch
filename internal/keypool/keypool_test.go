package keypool

import (
	"fmt"
	"testing"
	"time"
)

func TestNextReturnsKey(t *testing.T) {
	p := NewKeyPool([]string{"key-a", "key-b", "key-c"}, nil)
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
	p := NewKeyPool([]string{"key-a", "key-b", "key-c"}, nil)
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
	p := NewKeyPool([]string{"key-a", "key-b", "key-c"}, nil)
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
	p := NewKeyPool([]string{"key-a", "key-b"}, nil)
	if n := p.ActiveCount(); n != 2 {
		t.Fatalf("ActiveCount() = %d, want 2 before AddKey", n)
	}

	idx := p.AddKey("new-key", "")
	if idx != 2 {
		t.Errorf("AddKey() returned index %d, want 2", idx)
	}
	if n := p.ActiveCount(); n != 3 {
		t.Errorf("ActiveCount() = %d after AddKey, want 3", n)
	}
}

func TestRemoveKey(t *testing.T) {
	p := NewKeyPool([]string{"key-a", "key-b", "key-c"}, nil)
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
	p := NewKeyPool([]string{"key-a", "key-b", "key-c"}, nil)
	err := p.RemoveKey(999)
	if err == nil {
		t.Error("RemoveKey(999) expected error, got nil")
	}
}

func TestNextEmptyPool(t *testing.T) {
	p := NewKeyPool([]string{}, nil)
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
	p := NewKeyPool([]string{"k1", "k2", "k3", "k4"}, nil)
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

func TestNameReturnsCorrectName(t *testing.T) {
	keys := []string{"key-a", "key-b", "key-c"}
	names := []string{"主账号", "备用key", ""}
	p := NewKeyPool(keys, names)

	if n := p.Name(0); n != "主账号" {
		t.Errorf("Name(0) = %q, want %q", n, "主账号")
	}
	if n := p.Name(1); n != "备用key" {
		t.Errorf("Name(1) = %q, want %q", n, "备用key")
	}
	if n := p.Name(2); n != "" {
		t.Errorf("Name(2) = %q, want empty", n)
	}
}

func TestNameOutOfRange(t *testing.T) {
	p := NewKeyPool([]string{"key-a"}, []string{"test"})
	if n := p.Name(-1); n != "" {
		t.Errorf("Name(-1) = %q, want empty", n)
	}
	if n := p.Name(5); n != "" {
		t.Errorf("Name(5) = %q, want empty", n)
	}
}

func TestGetKeyDetailsIncludesName(t *testing.T) {
	p := NewKeyPool([]string{"key-a", "key-b"}, []string{"主key", ""})
	details := p.GetKeyDetails()
	if len(details) != 2 {
		t.Fatalf("GetKeyDetails len = %d, want 2", len(details))
	}
	if details[0]["name"] != "主key" {
		t.Errorf("details[0].name = %q, want %q", details[0]["name"], "主key")
	}
	if details[1]["name"] != "" {
		t.Errorf("details[1].name = %q, want empty", details[1]["name"])
	}
}

func TestAddKeyWithName(t *testing.T) {
	p := NewKeyPool([]string{"key-a"}, []string{"original"})
	idx := p.AddKey("key-b", "新key")
	if idx != 1 {
		t.Errorf("AddKey index = %d, want 1", idx)
	}
	if n := p.Name(1); n != "新key" {
		t.Errorf("Name(1) after AddKey = %q, want %q", n, "新key")
	}
}

func BenchmarkKeyPoolNext(b *testing.B) {
	keySet := []string{"ka", "kb", "kc", "kd", "ke", "kf", "kg", "kh", "ki", "kj"}
	for _, n := range []int{1, 5, 10} {
		p := NewKeyPool(keySet[:n], nil)
		b.Run(fmt.Sprintf("keys-%d", n), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				p.Next()
			}
		})
	}
}
