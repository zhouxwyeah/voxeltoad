package cache

import (
	"context"
	"testing"
	"time"
)

func TestMemoryCache_SetGet(t *testing.T) {
	c := NewMemoryCache()
	ctx := context.Background()

	if err := c.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v, ok, err := c.Get(ctx, "k")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v, want hit", ok, err)
	}
	if string(v) != "v" {
		t.Errorf("value = %q, want %q", v, "v")
	}
}

func TestMemoryCache_Miss(t *testing.T) {
	c := NewMemoryCache()
	if _, ok, _ := c.Get(context.Background(), "absent"); ok {
		t.Error("expected miss for absent key")
	}
}

func TestMemoryCache_Expiry(t *testing.T) {
	c := NewMemoryCache()
	now := time.Unix(0, 0)
	c.now = func() time.Time { return now }
	ctx := context.Background()

	_ = c.Set(ctx, "k", []byte("v"), time.Second)
	now = now.Add(2 * time.Second) // past TTL

	if _, ok, _ := c.Get(ctx, "k"); ok {
		t.Error("expected expired entry to be a miss")
	}
}
