package buffer

import "sync"

// Ring is a thread-safe ring buffer for storing events during disconnection.
// When full, the oldest events are dropped.
type Ring[T any] struct {
	mu    sync.Mutex
	items []T
	cap   int
}

func New[T any](capacity int) *Ring[T] {
	return &Ring[T]{
		items: make([]T, 0, capacity),
		cap:   capacity,
	}
}

// Push adds an event to the buffer. If full, the oldest event is dropped.
func (r *Ring[T]) Push(item T) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.items) >= r.cap {
		r.items = r.items[1:]
	}
	r.items = append(r.items, item)
}

// Drain returns all buffered events and clears the buffer.
func (r *Ring[T]) Drain() []T {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.items) == 0 {
		return nil
	}

	out := make([]T, len(r.items))
	copy(out, r.items)
	r.items = r.items[:0]
	return out
}

// Len returns the current number of buffered events.
func (r *Ring[T]) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.items)
}
