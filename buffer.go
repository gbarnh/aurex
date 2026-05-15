package main

import "sync"

// OutputBuffer is a thread-safe byte ring buffer with a monotonically-increasing
// global cursor. Bytes appended past the buffer's capacity push out the oldest
// bytes, but the cursor never resets — clients use it to request "everything
// since cursor=N" and the buffer returns whatever portion is still in memory.
//
// This is the protocol primitive that lets aurex sessions feel like opencode's:
// disconnect, refresh, switch devices — each client passes its last cursor and
// gets the bytes it missed (or as many as still fit in the ring).
type OutputBuffer struct {
	mu    sync.Mutex
	data  []byte
	start int64 // global cursor of data[0]
	end   int64 // global cursor of data[len(data)] (== start + len(data))
	max   int
}

func NewOutputBuffer(max int) *OutputBuffer {
	if max <= 0 {
		max = 2 << 20 // 2 MiB default (matches opencode)
	}
	return &OutputBuffer{max: max, data: make([]byte, 0, 64*1024)}
}

// Append writes bytes and returns the new end cursor. Evicts from the front
// when the buffer would exceed max bytes.
func (b *OutputBuffer) Append(p []byte) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	b.end += int64(len(p))
	if len(b.data) > b.max {
		excess := len(b.data) - b.max
		// Shift in-place to keep the underlying array stable-ish.
		copy(b.data, b.data[excess:])
		b.data = b.data[:len(b.data)-excess]
		b.start += int64(excess)
	}
	return b.end
}

// ReadFrom returns all bytes from cursor to end, clamped to what's still in
// the ring. If cursor < start, replay begins at start (the oldest available
// byte); the client will see a gap but xterm renders the stream linearly.
// Returns the new end cursor so callers can update their position.
func (b *OutputBuffer) ReadFrom(cursor int64) ([]byte, int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if cursor < b.start {
		cursor = b.start
	}
	if cursor > b.end {
		cursor = b.end
	}
	out := make([]byte, b.end-cursor)
	copy(out, b.data[cursor-b.start:])
	return out, b.end
}

// Cursor returns the current end position.
func (b *OutputBuffer) Cursor() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.end
}

// Start returns the cursor of the oldest byte still in the ring.
func (b *OutputBuffer) Start() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.start
}

// Clear drops all buffered bytes and advances start to end. The cursor itself
// is preserved so new writes continue the monotonic sequence — clients that
// reconnect with their old cursor will find start > theirCursor and naturally
// catch up to live.
//
// Called on PTY resize: bytes captured at the old terminal dimensions can't
// be replayed correctly at the new dimensions (line wraps are baked into the
// bytes), so dropping them is preferable to showing garbled history.
func (b *OutputBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = b.data[:0]
	b.start = b.end
}
