package buffer

import "testing"

func TestRingPushAndDrain(t *testing.T) {
	r := New[int](3)

	r.Push(1)
	r.Push(2)
	r.Push(3)

	if r.Len() != 3 {
		t.Fatalf("expected len 3, got %d", r.Len())
	}

	items := r.Drain()
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[0] != 1 || items[1] != 2 || items[2] != 3 {
		t.Fatalf("unexpected items: %v", items)
	}

	if r.Len() != 0 {
		t.Fatalf("expected empty after drain, got %d", r.Len())
	}
}

func TestRingOverflow(t *testing.T) {
	r := New[int](3)

	r.Push(1)
	r.Push(2)
	r.Push(3)
	r.Push(4) // should drop 1
	r.Push(5) // should drop 2

	items := r.Drain()
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[0] != 3 || items[1] != 4 || items[2] != 5 {
		t.Fatalf("expected [3,4,5], got %v", items)
	}
}

func TestRingDrainEmpty(t *testing.T) {
	r := New[string](5)
	items := r.Drain()
	if items != nil {
		t.Fatalf("expected nil from empty drain, got %v", items)
	}
}
