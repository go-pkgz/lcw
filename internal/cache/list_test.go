// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the lists_LICENSE file.

package cache

import (
	"testing"
)

func checkListLen(t *testing.T, l *list, length int) bool {
	if n := l.Len(); n != length {
		t.Errorf("l.Len() = %d, want %d", n, length)
		return false
	}
	return true
}

func checkListPointers(t *testing.T, l *list, es []*cacheItem) {
	root := &l.root

	if !checkListLen(t, l, len(es)) {
		return
	}

	// zero length lists must be the zero value or properly initialized (sentinel circle)
	if len(es) == 0 {
		if l.root.next != nil && l.root.next != root || l.root.prev != nil && l.root.prev != root {
			t.Errorf("l.root.next = %p, l.root.prev = %p; both should both be nil or %p", l.root.next, l.root.prev, root)
		}
		return
	}
	// len(es) > 0

	// check internal and external prev/next connections
	for i, e := range es {
		prev := root
		Prev := (*cacheItem)(nil)
		if i > 0 {
			prev = es[i-1]
			Prev = prev
		}
		if p := e.prev; p != prev {
			t.Errorf("elt[%d](%p).prev = %p, want %p", i, e, p, prev)
		}
		if p := e.Prev(); p != Prev {
			t.Errorf("elt[%d](%p).Prev() = %p, want %p", i, e, p, Prev)
		}

		next := root
		Next := (*cacheItem)(nil)
		if i < len(es)-1 {
			next = es[i+1]
			Next = next
		}
		if n := e.next; n != next {
			t.Errorf("elt[%d](%p).next = %p, want %p", i, e, n, next)
		}
		if n := e.Next(); n != Next {
			t.Errorf("elt[%d](%p).Next() = %p, want %p", i, e, n, Next)
		}
	}
}

func TestList(t *testing.T) {
	l := newList()
	checkListPointers(t, l, []*cacheItem{})

	// Single element list
	e := &cacheItem{value: "a"}
	l.PushFront(e)
	checkListPointers(t, l, []*cacheItem{e})
	l.MoveToFront(e)
	checkListPointers(t, l, []*cacheItem{e})
	l.Remove(e)
	checkListPointers(t, l, []*cacheItem{})

	// Bigger list
	e4 := &cacheItem{value: 2}
	e3 := &cacheItem{value: 2}
	e2 := &cacheItem{value: 2}
	e1 := &cacheItem{value: 1}
	l.PushFront(e4)
	l.PushFront(e3)
	l.PushFront(e2)
	l.PushFront(e1)
	checkListPointers(t, l, []*cacheItem{e1, e2, e3, e4})

	l.Remove(e2)
	checkListPointers(t, l, []*cacheItem{e1, e3, e4})

	// Check standard iteration.
	sum := 0
	for e := l.Back(); e != nil; e = e.Prev() {
		if i, ok := e.value.(int); ok {
			sum += i
		}
	}
	if sum != 5 {
		t.Errorf("sum over l = %d, want 4", sum)
	}

	// Clear all elements by iterating
	var next *cacheItem
	for e := l.Back(); e != nil; e = next {
		next = e.Prev()
		l.Remove(e)
	}
	checkListPointers(t, l, []*cacheItem{})
}
