package cloudhypervisor

import "github.com/cocoonstack/cocoon/gc"

// RegisterGC registers the Cloud Hypervisor GC module with the given Orchestrator.
func (ch *CloudHypervisor) RegisterGC(orch *gc.Orchestrator) {
	gc.Register(orch, ch.BuildGCModule())
}
