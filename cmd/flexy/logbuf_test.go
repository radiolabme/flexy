package main

import "testing"

func TestNewLogBuffer(t *testing.T) {
	b := newLogBuffer(3)
	if b.cap != 3 {
		t.Fatalf("cap = %d, want 3", b.cap)
	}
	lines, last := b.Since(0)
	if len(lines) != 0 {
		t.Errorf("empty buffer returned %d lines", len(lines))
	}
	if last != 0 {
		t.Errorf("last = %d, want 0", last)
	}
}

func TestLogBufferWrite(t *testing.T) {
	b := newLogBuffer(5)
	for i := range 3 {
		msg := []byte("line" + string(rune('0'+i)))
		if _, err := b.Write(msg); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	lines, last := b.Since(0)
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	if last != 2 {
		t.Errorf("last = %d, want 2", last)
	}
	for i, l := range lines {
		if l.Seq != int64(i) {
			t.Errorf("lines[%d].Seq = %d, want %d", i, l.Seq, i)
		}
	}
}

func TestLogBufferOverflow(t *testing.T) {
	b := newLogBuffer(3)
	for i := range 5 {
		msg := []byte("line" + string(rune('A'+i)))
		b.Write(msg)
	}
	lines, last := b.Since(0)
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (capacity)", len(lines))
	}
	if last != 4 {
		t.Errorf("last = %d, want 4", last)
	}
	// Should have lines 2,3,4 (oldest two evicted).
	if lines[0].Seq != 2 {
		t.Errorf("oldest line Seq = %d, want 2", lines[0].Seq)
	}
}

func TestLogBufferSinceFilter(t *testing.T) {
	b := newLogBuffer(10)
	for range 5 {
		b.Write([]byte("x"))
	}
	lines, _ := b.Since(3)
	if len(lines) != 2 {
		t.Fatalf("Since(3) returned %d lines, want 2 (seq 3,4)", len(lines))
	}
	if lines[0].Seq != 3 || lines[1].Seq != 4 {
		t.Errorf("got seqs %d,%d; want 3,4", lines[0].Seq, lines[1].Seq)
	}
}
