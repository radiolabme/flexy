package main

import "sync"

// LogLine holds one log entry (raw zerolog JSON) with a monotonic sequence number.
type LogLine struct {
	Seq  int64  `json:"seq"`
	Text string `json:"text"`
}

// LogBuffer is a fixed-capacity ring buffer of recent log lines.
type LogBuffer struct {
	mu    sync.Mutex
	lines []LogLine
	cap   int
	next  int64
}

var logBuf = newLogBuffer(500)

func newLogBuffer(cap int) *LogBuffer {
	return &LogBuffer{lines: make([]LogLine, 0, cap), cap: cap}
}

// Write implements io.Writer; called by zerolog with raw JSON per log line.
func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	line := LogLine{Seq: b.next, Text: string(p)}
	b.next++
	if len(b.lines) < b.cap {
		b.lines = append(b.lines, line)
	} else {
		copy(b.lines, b.lines[1:])
		b.lines[len(b.lines)-1] = line
	}
	return len(p), nil
}

// Since returns all lines with Seq >= since, plus the current last sequence number.
func (b *LogBuffer) Since(since int64) ([]LogLine, int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []LogLine
	for _, l := range b.lines {
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
