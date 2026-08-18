package lcw

import (
	"fmt"
	"sync"
)

// loadGroup makes sure only one load function per key runs at a time.
// concurrent calls for the same key wait for the in-flight one and share its result,
// so a cold key is loaded once instead of once per caller.
// the loader must not call Get for the same key on the same cache, it would wait for itself.
type loadGroup struct {
	mu    sync.Mutex
	calls map[string]*loadCall
}

type loadCall struct {
	wg  sync.WaitGroup
	val any
	err error
}

// do calls fn for the given key unless the same key is already being loaded,
// in that case it waits for the in-flight load and returns its result.
func (g *loadGroup) do(key string, fn func() (any, error)) (any, error) {
	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[string]*loadCall)
	}
	if call, ok := g.calls[key]; ok {
		g.mu.Unlock()
		call.wg.Wait()
		return call.val, call.err
	}

	call := &loadCall{}
	call.wg.Add(1)
	g.calls[key] = call
	g.mu.Unlock()

	// waiters are released even if fn panics, and get an error instead of a zero value
	// with no error at all. The panic keeps propagating in the calling goroutine, as it
	// would without the load coordination.
	defer func() {
		if p := recover(); p != nil {
			call.err = fmt.Errorf("cache loader panic: %v", p)
			g.done(key, call)
			panic(p)
		}
		g.done(key, call)
	}()

	call.val, call.err = fn()
	return call.val, call.err
}

// done drops the in-flight call and releases everybody waiting for it
func (g *loadGroup) done(key string, call *loadCall) {
	g.mu.Lock()
	delete(g.calls, key)
	g.mu.Unlock()
	call.wg.Done()
}
