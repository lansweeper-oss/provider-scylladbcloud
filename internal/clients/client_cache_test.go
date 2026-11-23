package clients

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// countingConfigure records how often a client is built and returns a distinct
// value per build, so tests can tell a cached client from a rebuilt one. When
// release is set, builds block until it is closed.
type countingConfigure struct {
	mu      sync.Mutex
	calls   int
	err     error
	release chan struct{}
}

func (c *countingConfigure) configure(_ context.Context, _ map[string]any) (any, error) {
	if c.release != nil {
		<-c.release
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return c.calls, nil
}

func (c *countingConfigure) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
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

		if cc.callCount() != 1 {
			t.Errorf("want 1 client build, got %d", cc.callCount())
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

		if cc.callCount() != 2 {
			t.Errorf("want 2 client builds, got %d", cc.callCount())
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

		if cc.callCount() != 2 {
			t.Errorf("want 2 client builds, got %d", cc.callCount())
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

		if cc.callCount() != 2 {
			t.Errorf("want 2 client builds, got %d", cc.callCount())
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

func TestClientCacheConcurrency(t *testing.T) {
	cfg := map[string]any{"token": "a"}

	t.Run("BuildsOnceForConcurrentMisses", func(t *testing.T) {
		cc := &countingConfigure{release: make(chan struct{})}
		cache := newClientCache(time.Minute, cc.configure, time.Now)

		const callers = 20
		var wg sync.WaitGroup
		results := make([]any, callers)
		for i := range callers {
			wg.Go(func() {
				results[i], _ = cache.Get(context.Background(), cfg)
			})
		}
		// Let every caller reach the cache before the single build finishes.
		time.Sleep(50 * time.Millisecond)
		close(cc.release)
		wg.Wait()

		if cc.callCount() != 1 {
			t.Errorf("want 1 client build for %d concurrent callers, got %d", callers, cc.callCount())
		}
		for i, r := range results {
			if r != results[0] {
				t.Errorf("caller %d got client %v, want the shared %v", i, r, results[0])
			}
		}
	})

	t.Run("CallerReturnsWhenItsContextIsDone", func(t *testing.T) {
		cc := &countingConfigure{release: make(chan struct{})}
		cache := newClientCache(time.Minute, cc.configure, time.Now)

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := cache.Get(ctx, cfg)
			done <- err
		}()
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("want context.Canceled, got %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Get did not return after its context was cancelled")
		}

		// The build keeps running for other callers and lands in the cache.
		close(cc.release)
		got, err := cache.Get(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Get after the build finished: %v", err)
		}
		if got != 1 || cc.callCount() != 1 {
			t.Errorf("want the single build's client (1) and 1 build, got %v and %d builds", got, cc.callCount())
		}
	})
}
