package oci

import (
	"os"

	"github.com/cocoonstack/cocoon/gc"
	"github.com/cocoonstack/cocoon/images"
	"github.com/cocoonstack/cocoon/utils"
)

// GCModule returns a typed gc.Module for the OCI backend.
func (o *OCI) GCModule() gc.Module[images.ImageGCSnapshot] {
	return images.BuildGCModule(images.GCModuleConfig[imageIndex]{
		Name:      typ,
		Locker:    o.locker,
		Store:     o.store,
		ReadRefs:  func(idx *imageIndex) map[string]struct{} { return images.ReferencedDigests(idx.Images) },
		ScanDisk:  func() ([]string, error) { return utils.ScanFileStems(o.conf.BlobsDir(), ".erofs") },
		ExtraDisk: func() ([]string, error) { return utils.ScanSubdirs(o.conf.BootBaseDir()) },
		Removers: []func(string) error{
			func(hex string) error { return os.Remove(o.conf.BlobPath(hex)) },
			func(hex string) error { return os.RemoveAll(o.conf.BootDir(hex)) },
		},
		TempDir: o.conf.TempDir(),
		DirOnly: true,
	})
}

// RegisterGC registers the OCI GC module with the given Orchestrator.
func (o *OCI) RegisterGC(orch *gc.Orchestrator) {
	gc.Register(orch, o.GCModule())
}
