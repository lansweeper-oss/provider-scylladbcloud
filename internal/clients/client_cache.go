package clients

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// configureFn builds the Terraform provider meta (the ScyllaDB Cloud client)
// for a provider configuration.
type configureFn func(ctx context.Context, cfg map[string]any) (any, error)

// ClientCache keeps one configured ScyllaDB Cloud client per provider
// configuration. Building a client preloads deployment metadata (Scylla
// versions, cloud providers and their regions) and the account ID from the
// API, which is too expensive to repeat on every reconcile. Entries expire
// after a TTL so that metadata changes (e.g. new Scylla versions) are picked up.
type ClientCache struct {
	ttl       time.Duration
	configure configureFn
	now       func() time.Time

	mu      sync.Mutex
	entries map[string]cachedClient
	builds  singleflight.Group
}

type cachedClient struct {
	meta    any
	expires time.Time
}

// NewClientCache returns a ClientCache that builds clients with the upstream
// provider's configure logic and keeps them for ttl.
func NewClientCache(ttl time.Duration) *ClientCache {
	return newClientCache(ttl, configureScyllaClient, time.Now)
}

func newClientCache(ttl time.Duration, configure configureFn, now func() time.Time) *ClientCache {
	return &ClientCache{
		ttl:       ttl,
		configure: configure,
		now:       now,
		entries:   map[string]cachedClient{},
	}
}

// Get returns the cached client for cfg, building a new one if there is none
// or it has expired. Concurrent callers for the same configuration share one
// build. Failed builds are not cached.
func (c *ClientCache) Get(ctx context.Context, cfg map[string]any) (any, error) {
	key, err := cacheKey(cfg)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	entry, ok := c.entries[key]
	c.mu.Unlock()
	if ok && c.now().Before(entry.expires) {
		return entry.meta, nil
	}

	// The build calls the API with retries and can take minutes, so it runs
	// detached from the caller's cancellation: a cancelled reconcile returns
	// right away while the build completes for the other callers waiting on it.
	buildCtx := context.WithoutCancel(ctx)
	res := c.builds.DoChan(key, func() (any, error) {
		return c.build(buildCtx, key, cfg)
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-res:
		return r.Val, r.Err
	}
}

// build configures a client and caches it, evicting expired entries.
func (c *ClientCache) build(ctx context.Context, key string, cfg map[string]any) (any, error) {
	meta, err := c.configure(ctx, cfg)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for k, e := range c.entries {
		if !now.Before(e.expires) {
			delete(c.entries, k)
		}
	}
	c.entries[key] = cachedClient{meta: meta, expires: now.Add(c.ttl)}
	return meta, nil
}

// cacheKey hashes the configuration so that credentials are not kept as map
// keys. encoding/json sorts map keys, so equal configurations hash equally.
func cacheKey(cfg map[string]any) (string, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("cannot compute client cache key: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
