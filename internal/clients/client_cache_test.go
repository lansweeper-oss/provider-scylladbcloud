package clients

import (
	"context"
	"errors"
	"testing"
	"time"
)

// countingConfigure records how often a client is built and returns a distinct
// value per build, so tests can tell a cached client from a rebuilt one.
type countingConfigure struct {
	calls int
	err   error
}

func (c *countingConfigure) configure(_ context.Context, _ map[string]any) (any, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return c.calls, nil
}

type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time { return f.t }

func TestClientCacheGet(t *testing.T) {
	cfgA := map[string]any{"token": "a"}
	cfgB := map[string]any{"token": "b"}

	t.Run("ReusesClientForSameConfigWithinTTL", func(t *testing.T) {
		cc := &countingConfigure{}
		clock := &fakeClock{t: time.Unix(0, 0)}
		cache := newClientCache(time.Minute, cc.configure, clock.now)

		first, err := cache.Get(context.Background(), cfgA)
		if err != nil {
			t.Fatalf("first Get: %v", err)
		}
		clock.t = clock.t.Add(59 * time.Second)
		second, err := cache.Get(context.Background(), map[string]any{"token": "a"})
		if err != nil {
			t.Fatalf("second Get: %v", err)
		}

		if cc.calls != 1 {
			t.Errorf("want 1 client build, got %d", cc.calls)
		}
		if first != second {
			t.Errorf("want the cached client %v, got %v", first, second)
		}
	})

	t.Run("RebuildsClientAfterTTL", func(t *testing.T) {
		cc := &countingConfigure{}
		clock := &fakeClock{t: time.Unix(0, 0)}
		cache := newClientCache(time.Minute, cc.configure, clock.now)

		first, _ := cache.Get(context.Background(), cfgA)
		clock.t = clock.t.Add(time.Minute)
		second, err := cache.Get(context.Background(), cfgA)
		if err != nil {
			t.Fatalf("Get after TTL: %v", err)
		}

		if cc.calls != 2 {
			t.Errorf("want 2 client builds, got %d", cc.calls)
		}
		if first == second {
			t.Errorf("want a rebuilt client, got the cached one %v", first)
		}
	})

	t.Run("KeepsSeparateClientsPerConfig", func(t *testing.T) {
		cc := &countingConfigure{}
		clock := &fakeClock{t: time.Unix(0, 0)}
		cache := newClientCache(time.Minute, cc.configure, clock.now)

		a, _ := cache.Get(context.Background(), cfgA)
		b, _ := cache.Get(context.Background(), cfgB)
		aAgain, _ := cache.Get(context.Background(), cfgA)

		if cc.calls != 2 {
			t.Errorf("want 2 client builds, got %d", cc.calls)
		}
		if a == b {
			t.Errorf("want different clients per config, both are %v", a)
		}
		if a != aAgain {
			t.Errorf("want the cached client %v for config A, got %v", a, aAgain)
		}
	})

	t.Run("DoesNotCacheFailures", func(t *testing.T) {
		cc := &countingConfigure{err: errors.New("boom")}
		clock := &fakeClock{t: time.Unix(0, 0)}
		cache := newClientCache(time.Minute, cc.configure, clock.now)

		if _, err := cache.Get(context.Background(), cfgA); err == nil {
			t.Fatal("want error from failing configure, got nil")
		}
		cc.err = nil
		got, err := cache.Get(context.Background(), cfgA)
		if err != nil {
			t.Fatalf("Get after recovery: %v", err)
		}

		if cc.calls != 2 {
			t.Errorf("want 2 client builds, got %d", cc.calls)
		}
		if got == nil {
			t.Error("want a client after recovery, got nil")
		}
	})

	t.Run("EvictsExpiredEntries", func(t *testing.T) {
		cc := &countingConfigure{}
		clock := &fakeClock{t: time.Unix(0, 0)}
		cache := newClientCache(time.Minute, cc.configure, clock.now)

		_, _ = cache.Get(context.Background(), cfgA)
		clock.t = clock.t.Add(time.Minute)
		_, _ = cache.Get(context.Background(), cfgB)

		if n := len(cache.entries); n != 1 {
			t.Errorf("want only the live entry left, got %d entries", n)
		}
	})
}
