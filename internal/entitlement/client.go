package entitlement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ErrUnknownTenant is returned when the platform does not recognise the API
// key. It is the one failure that IS a denial: an unknown key is a failed
// authentication, not a lookup we could not complete.
var ErrUnknownTenant = errors.New("entitlement: unknown tenant key")

// Client fetches entitlements from tenantcore and caches them.
//
// Everything interesting about this type is in how it fails.
//
// An entitlement lookup sits on the request path of every call this service
// serves. If an unreachable platform meant "not entitled", tenantcore would
// become a single point of failure for every product at once — strictly
// worse than the monolith the split replaced, and it would tell paying
// customers they had not paid. So: on any transport or server failure the
// client answers from its last-known value with Stale set, and reports the
// staleness loudly (see Degraded, surfaced on /readyz).
//
// The cache is bounded by TTL, not by size. One entry per tenant per
// deployment is a handful of small structs; a tenant that stops calling stops
// being refreshed and its entry is evicted by the janitor.
type Client struct {
	baseURL    string
	serviceKey string
	ttl        time.Duration
	// graceWindow is how long a stale answer may be served after the TTL has
	// passed and the platform is unreachable. Past it the entry is dropped
	// and the next call fails honestly rather than serving an entitlement
	// from an hour ago forever.
	graceWindow time.Duration

	http *http.Client
	log  *zap.Logger

	mu     sync.RWMutex
	cache  map[string]*cacheEntry // keyed by tenant API key
	stop   chan struct{}
	closer sync.Once

	// degraded records the last time a fetch failed and we fell back to
	// cache. Read by Degraded for /readyz.
	degradedSince *time.Time
}

type cacheEntry struct {
	ent       Entitlement
	fetchedAt time.Time
}

// Config for the client.
type Config struct {
	BaseURL     string // e.g. http://tenantcore:8090
	ServiceKey  string // this service's own key, from POST /admin/service-clients
	TTL         time.Duration
	GraceWindow time.Duration
	Timeout     time.Duration
	Log         *zap.Logger
}

// NewClient builds the client. A blank BaseURL or ServiceKey returns nil —
// this dependency is optional in the same sense as every other: the service
// starts, says so at startup and on /readyz, and the routes that need it
// report FEATURE_UNAVAILABLE rather than the whole process refusing to boot.
func NewClient(cfg Config) *Client {
	if cfg.BaseURL == "" || cfg.ServiceKey == "" {
		return nil
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 60 * time.Second
	}
	if cfg.GraceWindow <= 0 {
		cfg.GraceWindow = 15 * time.Minute
	}
	if cfg.Timeout <= 0 {
		// Short on purpose. This is on the request path; a platform that is
		// slow must degrade to cache quickly rather than making every car
		// wash request wait for it.
		cfg.Timeout = 3 * time.Second
	}

	c := &Client{
		baseURL:     cfg.BaseURL,
		serviceKey:  cfg.ServiceKey,
		ttl:         cfg.TTL,
		graceWindow: cfg.GraceWindow,
		http:        &http.Client{Timeout: cfg.Timeout},
		log:         cfg.Log,
		cache:       make(map[string]*cacheEntry),
		stop:        make(chan struct{}),
	}
	go c.janitor()
	return c
}

// Available reports whether the platform link is configured. Safe on a nil
// receiver so callers need no nil check of their own.
func (c *Client) Available() bool { return c != nil }

// Close stops the janitor.
func (c *Client) Close() {
	if c == nil {
		return
	}
	c.closer.Do(func() { close(c.stop) })
}

func (c *Client) janitor() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			return
		case now := <-t.C:
			c.mu.Lock()
			for k, e := range c.cache {
				if now.Sub(e.fetchedAt) > c.ttl+c.graceWindow {
					delete(c.cache, k)
				}
			}
			c.mu.Unlock()
		}
	}
}

// ForKey resolves a tenant API key to its entitlement.
//
// This is both the tenant resolution and the subscription lookup: the
// platform owns the tenants collection, so the answer to "which business is
// this" and "what have they bought" arrives together, in one call, and this
// service keeps no tenants of its own to drift out of step.
func (c *Client) ForKey(ctx context.Context, tenantKey string) (Entitlement, error) {
	if !c.Available() {
		return Entitlement{}, errors.New("entitlement: platform link is not configured")
	}

	if e, ok := c.fresh(tenantKey); ok {
		return e, nil
	}

	ent, err := c.fetch(ctx, tenantKey)
	if err == nil {
		c.store(tenantKey, ent)
		c.clearDegraded()
		return ent, nil
	}

	// An unknown key is a real answer, not a failed lookup. Cache nothing
	// and do not fall back — falling back would let a revoked key keep
	// working for as long as its old entry survived.
	if errors.Is(err, ErrUnknownTenant) {
		c.forget(tenantKey)
		return Entitlement{}, err
	}

	// Everything else is "we could not find out". Serve last-known state.
	if e, ok := c.stale(tenantKey); ok {
		c.markDegraded()
		c.logOnce("serving a cached entitlement: the platform is unreachable", err)
		e.Stale = true
		return e, nil
	}

	// Nothing cached for this tenant and the platform is down. There is no
	// honest answer, so say so rather than inventing one in either
	// direction — the caller turns this into a 503, never a 402.
	c.markDegraded()
	return Entitlement{}, fmt.Errorf("entitlement: platform unreachable and nothing cached: %w", err)
}

func (c *Client) fetch(ctx context.Context, tenantKey string) (Entitlement, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/svc/entitlements", nil)
	if err != nil {
		return Entitlement{}, err
	}
	// Two different credentials, deliberately: the service key proves which
	// product is asking, the tenant key says which tenant it is asking
	// about. Neither substitutes for the other.
	req.Header.Set("X-Service-Key", c.serviceKey)
	req.Header.Set("X-Tenant-Key", tenantKey)

	res, err := c.http.Do(req)
	if err != nil {
		return Entitlement{}, err
	}
	defer res.Body.Close()

	switch {
	case res.StatusCode == http.StatusOK:
	case res.StatusCode == http.StatusUnauthorized, res.StatusCode == http.StatusForbidden:
		// Ambiguous on its own: it could be OUR service key being rejected
		// rather than the tenant's. The envelope's domain tells them apart,
		// and the difference matters — one is a misconfigured deployment,
		// the other is a bad request.
		if c.isTenantDomain(res) {
			return Entitlement{}, ErrUnknownTenant
		}
		return Entitlement{}, fmt.Errorf("entitlement: platform rejected OUR service key (%d) — check TENANTCORE_SERVICE_KEY", res.StatusCode)
	default:
		return Entitlement{}, fmt.Errorf("entitlement: platform returned %d", res.StatusCode)
	}

	var body struct {
		Success bool        `json:"success"`
		Data    Entitlement `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return Entitlement{}, fmt.Errorf("entitlement: decode: %w", err)
	}
	if !body.Success {
		return Entitlement{}, errors.New("entitlement: platform reported failure")
	}
	return body.Data, nil
}

// isTenantDomain reads the error envelope to see whether the platform was
// complaining about the tenant key or about ours.
func (c *Client) isTenantDomain(res *http.Response) bool {
	var body struct {
		Error struct {
			Domain string `json:"domain"`
		} `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return false
	}
	return body.Error.Domain == "TENANT"
}

func (c *Client) fresh(key string) (Entitlement, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.cache[key]
	if !ok || time.Since(e.fetchedAt) > c.ttl {
		return Entitlement{}, false
	}
	return e.ent, true
}

func (c *Client) stale(key string) (Entitlement, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.cache[key]
	if !ok || time.Since(e.fetchedAt) > c.ttl+c.graceWindow {
		return Entitlement{}, false
	}
	return e.ent, true
}

func (c *Client) store(key string, ent Entitlement) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[key] = &cacheEntry{ent: ent, fetchedAt: time.Now()}
}

func (c *Client) forget(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cache, key)
}

func (c *Client) markDegraded() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.degradedSince == nil {
		now := time.Now()
		c.degradedSince = &now
	}
}

func (c *Client) clearDegraded() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.degradedSince = nil
}

// Degraded reports whether the platform link is currently failing, and since
// when.
//
// This is the loud half of serving stale state. Falling back to cache is the
// right behaviour and a liability on its own: without this, a platform that
// has been down for a day looks exactly like one that is fine, right up
// until a cache entry ages out and a tenant is refused for no visible
// reason. Surfaced on /readyz so a monitor can alert on it.
func (c *Client) Degraded() (bool, *time.Time) {
	if c == nil {
		return false, nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.degradedSince != nil, c.degradedSince
}

// logOnce keeps a platform outage from writing a line per request.
func (c *Client) logOnce(msg string, err error) {
	if c.log == nil {
		return
	}
	degraded, since := c.Degraded()
	if degraded && since != nil && time.Since(*since) > time.Second {
		return
	}
	c.log.Warn(msg, zap.Error(err), zap.String("platform", c.baseURL))
}
