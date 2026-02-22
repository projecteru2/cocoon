# Cocoon OS Images

Pre-built OS images are hosted on [GitHub Container Registry](https://github.com/orgs/projecteru2/packages?repo_name=cocoon), supporting both `linux/amd64` and `linux/arm64` architectures.

## Available Images

| Image | Tag | IMAGE_NAME |
|-------|-----|------------|
| Ubuntu 22.04 (Jammy) | `22.04` | `ghcr.io/projecteru2/cocoon/ubuntu:22.04` |
| Ubuntu 24.04 (Noble) | `24.04` | `ghcr.io/projecteru2/cocoon/ubuntu:24.04` |
| Ubuntu (latest build) | `latest` | `ghcr.io/projecteru2/cocoon/ubuntu:latest` |

> All tags are multi-arch manifests. `docker pull` will automatically select the correct architecture for your machine.

## Quick Start

```bash
IMAGE_NAME="ghcr.io/projecteru2/cocoon/ubuntu:22.04" bash start.sh
```

To use 24.04:

```bash
IMAGE_NAME="ghcr.io/projecteru2/cocoon/ubuntu:24.04" bash start.sh
```

## Prerequisites

- Linux with KVM access (`/dev/kvm` must be writable)
- `wget`, `mkfs.erofs`, `mkfs.ext4` installed
- `sudo` required on first run to set `CAP_NET_ADMIN`

## What start.sh Does

1. Downloads [Cloud Hypervisor](https://github.com/cloud-hypervisor/cloud-hypervisor) and sets capabilities
2. Pulls the container image specified by `IMAGE_NAME` in a daemonless manner via [crane](https://github.com/google/go-containerregistry)
3. Extracts the kernel (`vmlinuz`) and initramfs (`initrd.img`) from the image, and compresses the rootfs into EROFS
4. Creates a 10G COW (Copy-on-Write) disk as the writable layer
5. Launches a Cloud Hypervisor MicroVM (rootless, no daemon required)

## Browse All Available Images

Visit the GitHub Packages page for the full list of images and tags:

https://github.com/orgs/projecteru2/packages?repo_name=cocoon
