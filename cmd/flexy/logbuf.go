package main

import "sync"

type LogLine struct {
	Seq  int64  `json:"seq"`
	Text string `json:"text"`
}

type LogBuffer struct {
	mu   sync.Mutex
	ring []LogLine
	head int  // write index (mod len(ring))
	full bool // ring has wrapped at least once
	next int64
}

var logBuf = newLogBuffer(500)

func newLogBuffer(cap int) *LogBuffer {
	return &LogBuffer{ring: make([]LogLine, cap)}
}

// Write implements io.Writer for zerolog.
func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ring[b.head] = LogLine{Seq: b.next, Text: string(p)}
	b.next++
	b.head++
	if b.head == len(b.ring) {
		b.head = 0
		b.full = true
	}
	return len(p), nil
}

func (b *LogBuffer) Since(since int64) ([]LogLine, int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []LogLine
	n := b.count()
	start := b.head - n
	if start < 0 {
		start += len(b.ring)
	}
	for i := range n {
		l := b.ring[(start+i)%len(b.ring)]
		if l.Seq >= since {
			out = append(out, l)
		}
	}
	last := b.next - 1
	if last < 0 {
		last = 0
	}
	return out, last
}

func (b *LogBuffer) count() int {
	if b.full {
		return len(b.ring)
	}
	return b.head
}
