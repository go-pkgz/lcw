package lcw

import "sync"

// loadGroup makes sure only one load function per key runs at a time.
// concurrent calls for the same key wait for the in-flight one and share its result,
// so a cold key is loaded once instead of once per caller.
// the loader must not call Get for the same key on the same cache, it would wait for itself.
type loadGroup[V any] struct {
	mu    sync.Mutex
	calls map[string]*loadCall[V]
}

type loadCall[V any] struct {
	wg  sync.WaitGroup
	val V
	err error
}

// do calls fn for the given key unless the same key is already being loaded,
// in that case it waits for the in-flight load and returns its result.
func (g *loadGroup[V]) do(key string, fn func() (V, error)) (V, error) {
	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[string]*loadCall[V])
	}
	if call, ok := g.calls[key]; ok {
		g.mu.Unlock()
		call.wg.Wait()
		return call.val, call.err
	}

	call := &loadCall[V]{}
	call.wg.Add(1)
	g.calls[key] = call
	g.mu.Unlock()

	// released even if fn panics, otherwise waiters would block forever
	defer func() {
		g.mu.Lock()
		delete(g.calls, key)
		g.mu.Unlock()
		call.wg.Done()
	}()

	call.val, call.err = fn()
	return call.val, call.err
}
