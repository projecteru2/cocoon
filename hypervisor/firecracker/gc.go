package firecracker

import "github.com/cocoonstack/cocoon/gc"

// RegisterGC registers the Firecracker GC module with the given Orchestrator.
func (fc *Firecracker) RegisterGC(orch *gc.Orchestrator) {
	gc.Register(orch, fc.BuildGCModule())
}
