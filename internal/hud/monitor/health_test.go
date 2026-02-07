package monitor

import (
	"testing"
)

func TestRingBuffer_PushAndValues(t *testing.T) {
	rb := NewRingBuffer(5)

	rb.Push(1.0)
	rb.Push(2.0)
	rb.Push(3.0)

	vals := rb.Values()
	if len(vals) != 3 {
		t.Fatalf("expected 3 values, got %d", len(vals))
	}
	expected := []float64{1.0, 2.0, 3.0}
	for i, v := range vals {
		if v != expected[i] {
			t.Errorf("index %d: expected %f, got %f", i, expected[i], v)
		}
	}
}

func TestRingBuffer_Overflow(t *testing.T) {
	rb := NewRingBuffer(3)

	// Push 5 values into a 3-capacity buffer.
	rb.Push(1.0)
	rb.Push(2.0)
	rb.Push(3.0)
	rb.Push(4.0)
	rb.Push(5.0)

	vals := rb.Values()
	if len(vals) != 3 {
		t.Fatalf("expected 3 values, got %d", len(vals))
	}

	// Should contain the 3 most recent values in oldest-first order.
	expected := []float64{3.0, 4.0, 5.0}
	for i, v := range vals {
		if v != expected[i] {
			t.Errorf("index %d: expected %f, got %f", i, expected[i], v)
		}
	}
}

func TestRingBuffer_Len(t *testing.T) {
	rb := NewRingBuffer(10)

	if rb.Len() != 0 {
		t.Fatalf("expected len 0, got %d", rb.Len())
	}

	rb.Push(1.0)
	rb.Push(2.0)
	if rb.Len() != 2 {
		t.Fatalf("expected len 2, got %d", rb.Len())
	}

	// Fill to capacity.
	for i := 0; i < 10; i++ {
		rb.Push(float64(i))
	}
	if rb.Len() != 10 {
		t.Fatalf("expected len 10, got %d", rb.Len())
	}
}

func TestRingBuffer_Empty(t *testing.T) {
	rb := NewRingBuffer(5)

	vals := rb.Values()
	if vals != nil {
		t.Fatalf("expected nil for empty buffer, got %v", vals)
	}

	if rb.Len() != 0 {
		t.Fatalf("expected len 0, got %d", rb.Len())
	}
}

func TestRingBuffer_SingleElement(t *testing.T) {
	rb := NewRingBuffer(1)

	rb.Push(42.0)
	vals := rb.Values()
	if len(vals) != 1 || vals[0] != 42.0 {
		t.Fatalf("expected [42.0], got %v", vals)
	}

	// Overflow.
	rb.Push(99.0)
	vals = rb.Values()
	if len(vals) != 1 || vals[0] != 99.0 {
		t.Fatalf("expected [99.0], got %v", vals)
	}
}

func TestRingBuffer_ExactFill(t *testing.T) {
	rb := NewRingBuffer(4)

	rb.Push(1.0)
	rb.Push(2.0)
	rb.Push(3.0)
	rb.Push(4.0)

	vals := rb.Values()
	expected := []float64{1.0, 2.0, 3.0, 4.0}
	if len(vals) != 4 {
		t.Fatalf("expected 4 values, got %d", len(vals))
	}
	for i, v := range vals {
		if v != expected[i] {
			t.Errorf("index %d: expected %f, got %f", i, expected[i], v)
		}
	}
}

func TestRingBuffer_DefaultSize(t *testing.T) {
	// 0 or negative size should use DefaultRingSize (60).
	rb := NewRingBuffer(0)
	if rb.size != DefaultRingSize {
		t.Fatalf("expected size %d, got %d", DefaultRingSize, rb.size)
	}

	rb = NewRingBuffer(-1)
	if rb.size != DefaultRingSize {
		t.Fatalf("expected size %d, got %d", DefaultRingSize, rb.size)
	}
}

func TestRingBuffer_WrapAroundMultipleTimes(t *testing.T) {
	rb := NewRingBuffer(3)

	// Wrap around several times.
	for i := 0; i < 20; i++ {
		rb.Push(float64(i))
	}

	vals := rb.Values()
	if len(vals) != 3 {
		t.Fatalf("expected 3 values, got %d", len(vals))
	}

	// Should be last 3 values: 17, 18, 19.
	expected := []float64{17.0, 18.0, 19.0}
	for i, v := range vals {
		if v != expected[i] {
			t.Errorf("index %d: expected %f, got %f", i, expected[i], v)
		}
	}
}
