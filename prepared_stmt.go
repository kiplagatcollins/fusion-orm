package fusion

import (
	"container/list"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"time"
)

type stmtEntry struct {
	stmt    *sql.Stmt
	expires time.Time
	elem    *list.Element
	// refs counts in-flight acquisitions of this entry; retired marks it
	// as gone from the cache. Both are guarded by stmtCache.mu, and the
	// underlying *sql.Stmt is only Closed when retired is set and refs
	// reaches zero — so a statement handed to a caller can never be
	// closed under it by a concurrent LRU eviction (GORM #7831 class).
	refs    int
	retired bool
}

type stmtCache struct {
	mu    sync.Mutex
	cap   int
	ttl   time.Duration
	items map[string]*stmtEntry
	order *list.List
}

func newStmtCache(capacity int, ttl time.Duration) *stmtCache {
	if capacity <= 0 {
		capacity = 1000
	}
	return &stmtCache{cap: capacity, ttl: ttl, items: make(map[string]*stmtEntry), order: list.New()}
}

// get returns a cached (or freshly prepared) statement plus a release func
// the caller must invoke exactly once (usually via defer). Until release
// runs the statement is protected from eviction: it leaves the cache
// immediately on eviction but its Close is deferred to the last release.
func (c *stmtCache) get(ctx context.Context, pool ConnPool, query string) (*sql.Stmt, func(), error) {
	now := time.Now()
	c.mu.Lock()
	if entry, ok := c.items[query]; ok {
		if c.ttl == 0 || now.Before(entry.expires) {
			c.order.MoveToFront(entry.elem)
			entry.refs++
			c.mu.Unlock()
			return entry.stmt, c.releaser(entry), nil
		}
		c.dropLocked(query, entry)
	}
	c.mu.Unlock()

	stmt, err := pool.PrepareContext(ctx, query)
	if err != nil {
		return nil, nil, err
	}
	c.mu.Lock()
	if existing, ok := c.items[query]; ok && (c.ttl == 0 || time.Now().Before(existing.expires)) {
		c.order.MoveToFront(existing.elem)
		existing.refs++
		c.mu.Unlock()
		_ = stmt.Close() // lost the prepare race; nobody acquired it
		return existing.stmt, c.releaser(existing), nil
	}
	if existing, ok := c.items[query]; ok {
		c.dropLocked(query, existing)
	}
	expires := time.Time{}
	if c.ttl > 0 {
		expires = time.Now().Add(c.ttl)
	}
	entry := &stmtEntry{stmt: stmt, expires: expires, elem: c.order.PushFront(query), refs: 1}
	c.items[query] = entry
	for c.order.Len() > c.cap {
		oldest := c.order.Back()
		key, _ := oldest.Value.(string)
		victim := c.items[key]
		if victim == nil {
			c.order.Remove(oldest)
			continue
		}
		if victim == entry {
			break // never evict the statement just inserted
		}
		c.dropLocked(key, victim)
	}
	c.mu.Unlock()
	return stmt, c.releaser(entry), nil
}

// releaser returns an idempotent func that drops one acquisition reference,
// closing the statement if it has since been retired. The once guard is per
// acquisition: one entry can be held by several callers at once.
func (c *stmtCache) releaser(entry *stmtEntry) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			entry.refs--
			if entry.retired && entry.refs == 0 {
				_ = entry.stmt.Close()
			}
		})
	}
}

// dropLocked retires an entry: it leaves the cache immediately, but the
// underlying statement is only closed once every in-flight holder has
// released it. The caller must hold c.mu.
func (c *stmtCache) dropLocked(query string, entry *stmtEntry) {
	delete(c.items, query)
	c.order.Remove(entry.elem)
	entry.retired = true
	if entry.refs == 0 {
		_ = entry.stmt.Close()
	}
}

func (c *stmtCache) remove(query string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry := c.items[query]; entry != nil {
		c.dropLocked(query, entry)
	}
}

func (c *stmtCache) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for query, entry := range c.items {
		c.dropLocked(query, entry)
	}
}

func (c *stmtCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

type preparedPool struct {
	pool  ConnPool
	cache *stmtCache
}

func newPreparedPool(pool ConnPool, capacity int, ttl time.Duration) *preparedPool {
	return &preparedPool{pool: pool, cache: newStmtCache(capacity, ttl)}
}

func (p *preparedPool) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return p.pool.PrepareContext(ctx, query)
}

func (p *preparedPool) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	stmt, release, err := p.cache.get(ctx, p.pool, query)
	if err != nil {
		return nil, err
	}
	defer release()
	result, err := stmt.ExecContext(ctx, args...)
	if errors.Is(err, driver.ErrBadConn) {
		p.cache.remove(query)
		return p.pool.ExecContext(ctx, query, args...)
	}
	return result, err
}

func (p *preparedPool) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	stmt, release, err := p.cache.get(ctx, p.pool, query)
	if err != nil {
		return nil, err
	}
	defer release()
	rows, err := stmt.QueryContext(ctx, args...)
	if errors.Is(err, driver.ErrBadConn) {
		p.cache.remove(query)
		return p.pool.QueryContext(ctx, query, args...)
	}
	return rows, err
}

func (p *preparedPool) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	stmt, release, err := p.cache.get(ctx, p.pool, query)
	if err != nil {
		if ctx == nil {
			ctx = context.TODO()
		}
		return p.pool.QueryRowContext(ctx, query, args...)
	}
	defer release()
	return stmt.QueryRowContext(ctx, args...)
}

// Ping forwards to the wrapped pool, keeping Open's automatic ping and
// caller-initiated pings working when statement caching is enabled
// (GORM #6181 class).
func (p *preparedPool) Ping() error {
	if pinger, ok := p.pool.(interface{ Ping() error }); ok {
		return pinger.Ping()
	}
	return nil
}

// InvalidatePrepared drops every cached statement, e.g. after DDL may have
// changed the schema under a cached query (GORM #7255 class). Statements
// still held by in-flight callers are closed on their release.
func (p *preparedPool) InvalidatePrepared() { p.cache.close() }

func (p *preparedPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (ConnPool, error) {
	return beginPrepared(ctx, p.pool, opts)
}

func (p *preparedPool) Underlying() ConnPool { return p.pool }

func beginPrepared(ctx context.Context, pool ConnPool, opts *sql.TxOptions) (ConnPool, error) {
	switch beginner := pool.(type) {
	case TxBeginner:
		tx, err := beginner.BeginTx(ctx, opts)
		if err != nil {
			return nil, err
		}
		return &preparedTx{tx: tx, cache: newStmtCache(0, 0)}, nil
	case ConnPoolBeginner:
		conn, err := beginner.BeginTx(ctx, opts)
		if err != nil {
			return nil, err
		}
		return &preparedTx{tx: conn, cache: newStmtCache(0, 0)}, nil
	default:
		return nil, ErrInvalidTransaction
	}
}

type preparedTx struct {
	tx    ConnPool
	cache *stmtCache
}

func (t *preparedTx) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return t.tx.PrepareContext(ctx, query)
}

func (t *preparedTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	stmt, release, err := t.cache.get(ctx, t.tx, query)
	if err != nil {
		return nil, err
	}
	defer release()
	result, err := stmt.ExecContext(ctx, args...)
	if errors.Is(err, driver.ErrBadConn) {
		t.cache.remove(query)
		return t.tx.ExecContext(ctx, query, args...)
	}
	return result, err
}

func (t *preparedTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	stmt, release, err := t.cache.get(ctx, t.tx, query)
	if err != nil {
		return nil, err
	}
	defer release()
	rows, err := stmt.QueryContext(ctx, args...)
	if errors.Is(err, driver.ErrBadConn) {
		t.cache.remove(query)
		return t.tx.QueryContext(ctx, query, args...)
	}
	return rows, err
}

func (t *preparedTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	stmt, release, err := t.cache.get(ctx, t.tx, query)
	if err != nil {
		return t.tx.QueryRowContext(ctx, query, args...)
	}
	defer release()
	return stmt.QueryRowContext(ctx, args...)
}

// InvalidatePrepared drops the transaction's cached statements; see
// preparedPool.InvalidatePrepared.
func (t *preparedTx) InvalidatePrepared() { t.cache.close() }

func (t *preparedTx) Commit() error {
	committer, ok := t.tx.(TxCommitter)
	if !ok {
		return ErrInvalidTransaction
	}
	err := committer.Commit()
	t.cache.close()
	return err
}

func (t *preparedTx) Rollback() error {
	committer, ok := t.tx.(TxCommitter)
	if !ok {
		return ErrInvalidTransaction
	}
	err := committer.Rollback()
	t.cache.close()
	return err
}

func (t *preparedTx) Underlying() ConnPool { return t.tx }
