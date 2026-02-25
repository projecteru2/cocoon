package cni

import (
	"fmt"
	"syscall"

	"github.com/vishvananda/netns"
)

// mountNetns bind-mounts the netns fd to path so the namespace persists.
func mountNetns(ns netns.NsHandle, path string) error {
	src := fmt.Sprintf("/proc/self/fd/%d", int(ns))
	return syscall.Mount(src, path, "", syscall.MS_BIND, "")
}

// unmountNetns unmounts a previously bind-mounted netns.
func unmountNetns(path string) error {
	return syscall.Unmount(path, syscall.MNT_DETACH)
}
