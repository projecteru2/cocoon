package firecracker

import "github.com/cocoonstack/cocoon/hypervisor"

var runtimeFiles = []string{hypervisor.APISocketName, "fc.pid", hypervisor.ConsoleSockName}
