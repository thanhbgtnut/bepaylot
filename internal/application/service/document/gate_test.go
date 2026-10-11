package document

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Calls of the two kinds never overlap; calls of one kind do.
func TestModelGateKeepsKindsApart(t *testing.T) {
	var g modelGate
	var in [3]atomic.Int32
	var together atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		kind := gateClassify + i%2
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.enter(kind)
			if in[3-kind].Load() != 0 {
				t.Error("both kinds ran at once")
			}
			if in[kind].Add(1) > 1 {
				together.Add(1)
			}
			time.Sleep(time.Millisecond)
			in[kind].Add(-1)
			g.leave()
		}()
	}
	wg.Wait()
	if together.Load() == 0 {
		t.Error("calls of one kind never ran together")
	}
}
