package fusion

import (
	"errors"
	"fmt"

	"github.com/kiplagatcollins/fusion-orm/callbacks"
)

var (
	// ErrDuplicateCallback is returned when a callback name is already used
	// in the same processor.
	ErrDuplicateCallback = errors.New("duplicate callback")
	// ErrCallbackNotFound is returned by Replace/Remove/Before/After when
	// the anchor name is not registered.
	ErrCallbackNotFound = errors.New("callback not found")
	// ErrCallbackPanic records a callback panic on db.Error so the chain's
	// finalizers release the transaction before the panic is re-thrown.
	ErrCallbackPanic = errors.New("callback panicked")
)

// finalizerName is the chain entry that owns the transaction boundary; it is
// marked as a finalizer so it runs even after an error or a panic.
const finalizerName = "fusion:commit_or_rollback"

// Callback is one step in a CRUD processor.
type Callback func(*DB)

type callbackEntry struct {
	name      string
	fn        Callback
	match     func(*DB) bool
	finalizer bool
}

// CallbackProcessor is an ordered, name-addressable callback list.
type CallbackProcessor struct {
	entries []callbackEntry
}

// Register appends a callback.
func (p *CallbackProcessor) Register(name string, fn Callback) error {
	for _, entry := range p.entries {
		if entry.name == name {
			return fmt.Errorf("%w: %s", ErrDuplicateCallback, name)
		}
	}
	p.entries = append(p.entries, callbackEntry{name: name, fn: fn})
	return nil
}

// MarkFinalizer flags name as a finalizer: it runs even when the chain
// stopped early because of an error or recovered from a panic.
func (p *CallbackProcessor) MarkFinalizer(name string) error {
	for i := range p.entries {
		if p.entries[i].name == name {
			p.entries[i].finalizer = true
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrCallbackNotFound, name)
}

// Replace swaps the implementation of an existing callback.
func (p *CallbackProcessor) Replace(name string, fn Callback) error {
	for i := range p.entries {
		if p.entries[i].name == name {
			p.entries[i].fn = fn
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrCallbackNotFound, name)
}

// Remove deletes a callback by name.
func (p *CallbackProcessor) Remove(name string) error {
	for i := range p.entries {
		if p.entries[i].name == name {
			p.entries = append(p.entries[:i], p.entries[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrCallbackNotFound, name)
}

// Before inserts a callback immediately before anchor.
func (p *CallbackProcessor) Before(anchor, name string, fn Callback) error {
	return p.insert(anchor, name, fn, false)
}

// After inserts a callback immediately after anchor.
func (p *CallbackProcessor) After(anchor, name string, fn Callback) error {
	return p.insert(anchor, name, fn, true)
}

func (p *CallbackProcessor) insert(anchor, name string, fn Callback, after bool) error {
	for _, entry := range p.entries {
		if entry.name == name {
			return fmt.Errorf("%w: %s", ErrDuplicateCallback, name)
		}
	}
	for i, entry := range p.entries {
		if entry.name != anchor {
			continue
		}
		at := i
		if after {
			at++
		}
		p.entries = append(p.entries, callbackEntry{})
		copy(p.entries[at+1:], p.entries[at:])
		p.entries[at] = callbackEntry{name: name, fn: fn}
		return nil
	}
	return fmt.Errorf("%w: %s", ErrCallbackNotFound, anchor)
}

// Match attaches a predicate to the most recently registered callback.
func (p *CallbackProcessor) Match(match func(*DB) bool) error {
	if len(p.entries) == 0 {
		return fmt.Errorf("%w: no callback to match", ErrCallbackNotFound)
	}
	p.entries[len(p.entries)-1].match = match
	return nil
}

// Execute runs the chain in order, stopping non-finalizer entries when the
// error slot is set. Finalizers always run — after success, after an error,
// and from panic recovery — so the transaction boundary is never skipped. A
// panic is recovered only long enough to roll back: db.Error is set when
// still nil, finalizers run, and the original value is re-panicked.
func (p *CallbackProcessor) Execute(db *DB) {
	executed := make(map[string]bool, len(p.entries))
	defer func() {
		if r := recover(); r != nil {
			if db.Error == nil {
				db.Error = fmt.Errorf("%w: %v", ErrCallbackPanic, r)
			}
			p.runFinalizers(db, executed)
			panic(r)
		}
	}()
	for _, entry := range p.entries {
		if db.Error != nil {
			break
		}
		if entry.match != nil && !entry.match(db) {
			continue
		}
		executed[entry.name] = true
		entry.fn(db)
	}
	p.runFinalizers(db, executed)
}

// runFinalizers executes finalizer entries that have not run yet. Entries are
// marked before invocation so a panicking finalizer cannot run twice.
func (p *CallbackProcessor) runFinalizers(db *DB, executed map[string]bool) {
	for _, entry := range p.entries {
		if !entry.finalizer || executed[entry.name] {
			continue
		}
		if entry.match != nil && !entry.match(db) {
			continue
		}
		executed[entry.name] = true
		entry.fn(db)
	}
}

// CallbackManager exposes the six CRUD processors for plugin registration.
type CallbackManager struct {
	create *CallbackProcessor
	query  *CallbackProcessor
	update *CallbackProcessor
	delete *CallbackProcessor
	row    *CallbackProcessor
	raw    *CallbackProcessor
}

// Create returns the create processor.
func (m *CallbackManager) Create() *CallbackProcessor { return m.create }

// Query returns the query processor.
func (m *CallbackManager) Query() *CallbackProcessor { return m.query }

// Update returns the update processor.
func (m *CallbackManager) Update() *CallbackProcessor { return m.update }

// Delete returns the delete processor.
func (m *CallbackManager) Delete() *CallbackProcessor { return m.delete }

// Row returns the row processor.
func (m *CallbackManager) Row() *CallbackProcessor { return m.row }

// Raw returns the raw processor.
func (m *CallbackManager) Raw() *CallbackProcessor { return m.raw }

func newCallbackManager() *CallbackManager {
	m := &CallbackManager{
		create: &CallbackProcessor{}, query: &CallbackProcessor{},
		update: &CallbackProcessor{}, delete: &CallbackProcessor{},
		row: &CallbackProcessor{}, raw: &CallbackProcessor{},
	}
	for _, spec := range []struct {
		names []string
		p     *CallbackProcessor
	}{
		{callbacks.Create(), m.create},
		{callbacks.Query(), m.query},
		{callbacks.Update(), m.update},
		{callbacks.Delete(), m.delete},
		{callbacks.Row(), m.row},
		{callbacks.Raw(), m.raw},
	} {
		for _, name := range spec.names {
			fn := defaultCallback(name)
			if fn == nil {
				continue
			}
			_ = spec.p.Register(name, fn)
			if name == finalizerName {
				_ = spec.p.MarkFinalizer(name)
			}
		}
	}
	return m
}

// Callback returns the session's callback manager. Registration changes are
// shared by all derivatives of the session.
func (db *DB) Callback() *CallbackManager { return db.callbacks }
