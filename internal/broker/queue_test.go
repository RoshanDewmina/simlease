package broker

import "testing"

func TestSpecKey(t *testing.T) {
	a := DeviceSpec{Platform: "iOS", Model: "iPhone 17 Pro", Runtime: "27.0"}
	b := DeviceSpec{Platform: "iOS", Model: "iPhone 17 Pro", Runtime: "27.0"}
	c := DeviceSpec{Platform: "iOS", Model: "iPhone 16", Runtime: "27.0"}
	if SpecKey(a) != SpecKey(b) {
		t.Fatalf("identical specs should share key")
	}
	if SpecKey(a) == SpecKey(c) {
		t.Fatalf("different models should differ")
	}
}

func TestFIFOOrder(t *testing.T) {
	var q fifoQueue
	q.enqueue("a")
	q.enqueue("b")
	q.enqueue("c")
	if got := q.position("a"); got != 1 {
		t.Fatalf("a position=%d want 1", got)
	}
	if got := q.position("b"); got != 2 {
		t.Fatalf("b position=%d want 2", got)
	}
	if id, ok := q.dequeue(); !ok || id != "a" {
		t.Fatalf("dequeue got %q ok=%v want a", id, ok)
	}
	if !q.remove("c") {
		t.Fatal("remove c failed")
	}
	if id, ok := q.dequeue(); !ok || id != "b" {
		t.Fatalf("dequeue got %q ok=%v want b", id, ok)
	}
	if _, ok := q.dequeue(); ok {
		t.Fatal("expected empty")
	}
}
