package document

import "sync"

// Kinds of model work the gate keeps apart.
const (
	gateClassify = iota + 1
	gateOCR
)

// modelGate lets any number of calls of one kind run together and makes a
// call of the other kind wait until they are all done. With pages in flight
// this gives turns: the waiting pages are classified together, then read
// together, so a model server that holds one model at a time swaps models
// once per turn instead of once per page.
type modelGate struct {
	mu   sync.Mutex
	free *sync.Cond
	kind int
	n    int
}

func (g *modelGate) enter(kind int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.free == nil {
		g.free = sync.NewCond(&g.mu)
	}
	for g.n > 0 && g.kind != kind {
		g.free.Wait()
	}
	g.kind = kind
	g.n++
}

func (g *modelGate) leave() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.n--; g.n == 0 {
		g.free.Broadcast()
	}
}
