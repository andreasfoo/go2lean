// Package stream assembles a stream of events into one message, the way an
// SSE adapter assembles a chat completion from content-block events. Its
// properties are about sequences of calls, not one call: they are written as
// go2lean:sequence specs, which drive the converter over an input event
// sequence and check a condition after every step ("always") and at the end
// of the stream ("eventually", for a finite stream: when it ends).
package stream

// Event kinds.
const (
	Start = 0 // a content block opens
	Delta = 1 // text arrives for the open block
	Stop  = 2 // the open block closes
)

// MaxChunk is the largest delta the converter accepts; larger ones are
// dropped (so that counts cannot overflow over a stream of bounded length).
const MaxChunk = 1 << 16

// Event is one stream event: its kind and, for a delta, how many bytes of
// text it carries.
type Event struct {
	Kind int
	N    int
}

// Converter accumulates the text of a stream of content blocks.
type Converter struct {
	open   bool
	cur    int   // bytes in the open block
	blocks []int // bytes per closed block
	total  int   // bytes received
}

// Process handles one event. Bug: a Start while a block is still open
// starts afresh and drops the open block's text instead of closing it
// first.
func (c *Converter) Process(e Event) {
	switch e.Kind {
	case Start:
		c.open = true
		c.cur = 0
	case Delta:
		if c.open && e.N > 0 && e.N <= MaxChunk {
			c.cur += e.N
			c.total += e.N
		}
	case Stop:
		if c.open {
			c.blocks = append(c.blocks, c.cur)
			c.open = false
		}
	}
}

// Finish closes a block left open at the end of the stream and returns the
// size of every block.
func (c *Converter) Finish() []int {
	if c.open {
		c.blocks = append(c.blocks, c.cur)
		c.open = false
	}
	return c.blocks
}

// SeqComplete: whatever the order of the events, every byte received is in
// the assembled message when the stream ends. Violated by Start, Delta,
// Start, ...: the first block's text is lost.
//
// go2lean:sequence Converter.Process
func SeqComplete(events []Event) bool {
	var c Converter
	for _, e := range events {
		c.Process(e)
	}
	sum := 0
	for _, n := range c.Finish() {
		sum += n
	}
	return sum == c.total
}

// SeqBounded: after every event, the open block holds no more than was
// received, and no block is negative. Holds.
//
// go2lean:sequence Converter.Process
func SeqBounded(events []Event) bool {
	var c Converter
	for _, e := range events {
		c.Process(e)
		if c.cur < 0 || c.cur > c.total {
			return false
		}
	}
	return true
}
