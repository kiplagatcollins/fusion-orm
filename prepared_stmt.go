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

func (c *stmtCache) get(ctx context.Context, pool ConnPool, query string) (*sql.Stmt, error) {
	now := time.Now()
	c.mu.Lock()
	if entry, ok := c.items[query]; ok {
		if c.ttl == 0 || now.Before(entry.expires) {
			c.order.MoveToFront(entry.elem)
			c.mu.Unlock()
			return entry.stmt, nil
		}
		c.dropLocked(query, entry)
	}
	c.mu.Unlock()

	stmt, err := pool.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if existing, ok := c.items[query]; ok && (c.ttl == 0 || time.Now().Before(existing.expires)) {
		c.mu.Unlock()
		_ = stmt.Close()
		c.order.MoveToFront(existing.elem)
		return existing.stmt, nil
	}
	if existing, ok := c.items[query]; ok {
		c.dropLocked(query, existing)
	}
	expires := time.Time{}
	if c.ttl > 0 {
		expires = time.Now().Add(c.ttl)
	}
	entry := &stmtEntry{stmt: stmt, expires: expires, elem: c.order.PushFront(query)}
	c.items[query] = entry
	for c.order.Len() > c.cap {
		oldest := c.order.Back()
		key, _ := oldest.Value.(string)
		if entry := c.items[key]; entry != nil {
			c.dropLocked(key, entry)
		}
	}
	c.mu.Unlock()
	return stmt, nil
}

func (c *stmtCache) dropLocked(query string, entry *stmtEntry) {
	delete(c.items, query)
	c.order.Remove(entry.elem)
	_ = entry.stmt.Close()
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
	stmt, err := p.cache.get(ctx, p.pool, query)
	if err != nil {
		return nil, err
	}
	result, err := stmt.ExecContext(ctx, args...)
	if errors.Is(err, driver.ErrBadConn) {
		p.cache.remove(query)
		return p.pool.ExecContext(ctx, query, args...)
	}
	return result, err
}

func (p *preparedPool) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	stmt, err := p.cache.get(ctx, p.pool, query)
	if err != nil {
		return nil, err
	}
	rows, err := stmt.QueryContext(ctx, args...)
	if errors.Is(err, driver.ErrBadConn) {
		p.cache.remove(query)
		return p.pool.QueryContext(ctx, query, args...)
	}
	return rows, err
}

func (p *preparedPool) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	stmt, err := p.cache.get(ctx, p.pool, query)
	if err != nil {
		if ctx == nil {
			ctx = context.TODO()
		}
		return p.pool.QueryRowContext(ctx, query, args...)
	}
	return stmt.QueryRowContext(ctx, args...)
}

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
	stmt, err := t.cache.get(ctx, t.tx, query)
	if err != nil {
		return nil, err
	}
	result, err := stmt.ExecContext(ctx, args...)
	if errors.Is(err, driver.ErrBadConn) {
		t.cache.remove(query)
		return t.tx.ExecContext(ctx, query, args...)
	}
	return result, err
}

func (t *preparedTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	stmt, err := t.cache.get(ctx, t.tx, query)
	if err != nil {
		return nil, err
	}
	rows, err := stmt.QueryContext(ctx, args...)
	if errors.Is(err, driver.ErrBadConn) {
		t.cache.remove(query)
		return t.tx.QueryContext(ctx, query, args...)
	}
	return rows, err
}

func (t *preparedTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	stmt, err := t.cache.get(ctx, t.tx, query)
	if err != nil {
		return t.tx.QueryRowContext(ctx, query, args...)
	}
	return stmt.QueryRowContext(ctx, args...)
}

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
