// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the lists_LICENSE file.

package cache

// Next returns the next list element or nil.
func (e *cacheItem) Next() *cacheItem {
	if p := e.next; e.list != nil && p != &e.list.root {
		return p
	}
	return nil
}

// Prev returns the previous list element or nil.
func (e *cacheItem) Prev() *cacheItem {
	if p := e.prev; e.list != nil && p != &e.list.root {
		return p
	}
	return nil
}

// list represents a doubly linked list.
// The zero value for list is an empty list ready to use.
type list struct {
	root cacheItem // sentinel list element, only &root, root.prev, and root.next are used
	len  int       // current list length excluding (this) sentinel element
}

// Init initializes or clears list l.
func (l *list) Init() *list {
	l.root.next = &l.root
	l.root.prev = &l.root
	l.len = 0
	return l
}

// newList returns an initialized list.
func newList() *list { return new(list).Init() }

// Len returns the number of elements of list l.
// The complexity is O(1).
func (l *list) Len() int { return l.len }

// Back returns the last element of list l or nil if the list is empty.
func (l *list) Back() *cacheItem {
	if l.len == 0 {
		return nil
	}
	return l.root.prev
}

// lazyInit lazily initializes a zero list value.
func (l *list) lazyInit() {
	if l.root.next == nil {
		l.Init()
	}
}

// insert inserts e after at and increments l.len
func (l *list) insert(e, at *cacheItem) {
	n := at.next
	at.next = e
	e.prev = at
	e.next = n
	n.prev = e
	e.list = l
	l.len++
}

// insertValue is a convenience wrapper for insert(&cacheItem{value: v}, at).
func (l *list) insertValue(c, at *cacheItem) {
	l.insert(c, at)
}

// remove removes e from its list, decrements l.len, and returns e.
func (l *list) remove(e *cacheItem) *cacheItem {
	e.prev.next = e.next
	e.next.prev = e.prev
	e.next = nil // avoid memory leaks
	e.prev = nil // avoid memory leaks
	e.list = nil
	l.len--
	return e
}

// move moves e to next to at and returns e.
func (l *list) move(e, at *cacheItem) *cacheItem {
	if e == at {
		return e
	}
	e.prev.next = e.next
	e.next.prev = e.prev

	n := at.next
	at.next = e
	e.prev = at
	e.next = n
	n.prev = e

	return e
}

// Remove removes e from l if e is an element of list l.
// It returns the element value e.value.
// The element must not be nil.
func (l *list) Remove(e *cacheItem) interface{} {
	if e.list == l {
		// if e.list == l, l must have been initialized when e was inserted
		// in l or l == nil (e is a zero cacheItem) and l.remove will crash
		l.remove(e)
	}
	return e.value
}

// PushFront inserts a new element e with value v at the front of list l and returns e.
func (l *list) PushFront(c *cacheItem) {
	l.lazyInit()
	l.insertValue(c, &l.root)
}

// MoveToFront moves element e to the front of list l.
// If e is not an element of l, the list is not modified.
// The element must not be nil.
func (l *list) MoveToFront(e *cacheItem) {
	if e.list != l || l.root.next == e {
		return
	}
	// see comment in list.Remove about initialization of l
	l.move(e, &l.root)
}
