# Cocoon

Lightweight MicroVM engine built on [Cloud Hypervisor](https://github.com/cloud-hypervisor/cloud-hypervisor).

## Features

- **OCI VM images** -- pull OCI images with kernel + rootfs layers, content-addressed blob cache with SHA-256 deduplication
- **Cloud image support** -- pull from HTTP/HTTPS URLs (e.g. Ubuntu cloud images), automatic qcow2 conversion
- **UEFI boot** -- CLOUDHV.fd firmware by default; direct kernel boot for OCI images (auto-detected)
- **COW overlays** -- copy-on-write disks backed by shared base images (raw for OCI, qcow2 for cloud images)
- **CNI networking** -- automatic NIC creation via CNI plugins, multi-NIC support, per-VM IP allocation
- **DNS configuration** -- custom DNS servers injected into VMs via kernel cmdline
- **Cloud-init metadata** -- automatic NoCloud cidata disk for cloudimg VMs (hostname, root password)
- **Interactive console** -- `cocoon vm console` for bidirectional PTY access, SSH-style escape sequences
- **Docker-like CLI** -- `create`, `run`, `start`, `stop`, `list`, `inspect`, `console`, `rm`
- **Zero-daemon architecture** -- one Cloud Hypervisor process per VM, no long-running daemon
- **Garbage collection** -- automatic lock-safe GC of unreferenced images, orphaned overlays, and expired temp entries
- **Doctor script** -- pre-flight environment check and one-command dependency installation

## Requirements

- Linux with KVM (x86_64 or aarch64)
- Root access (sudo)
- [Cloud Hypervisor](https://github.com/cloud-hypervisor/cloud-hypervisor) v38.0+
- `qemu-img` (from qemu-utils, for cloud images)
- UEFI firmware (`CLOUDHV.fd`, for cloud images)
- CNI plugins (`bridge`, `host-local`, `loopback`)
- Go 1.25+ (build only)

## Installation

### GitHub Releases

Download pre-built binaries from [GitHub Releases](https://github.com/projecteru2/cocoon/releases):

```bash
# Linux amd64
curl -fsSL -o cocoon https://github.com/projecteru2/cocoon/releases/latest/download/cocoon_Linux_x86_64.tar.gz
tar -xzf cocoon_Linux_x86_64.tar.gz
install -m 0755 cocoon /usr/local/bin/

# Or use go install
go install github.com/projecteru2/cocoon@latest
```

### Build from source

```bash
git clone https://github.com/projecteru2/cocoon.git
cd cocoon
make build
```

This produces a `cocoon` binary in the project root. Use `make install` to install into `$GOPATH/bin`.

## Doctor

Cocoon ships a diagnostic script that checks your environment and can auto-install all dependencies:

```bash
# Check only — reports PASS/FAIL for each requirement
./doctor/check.sh

# Check and fix — creates directories, sets sysctl, adds iptables rules
./doctor/check.sh --fix

# Full setup — install cloud-hypervisor, firmware, and CNI plugins
./doctor/check.sh --upgrade
```

The `--upgrade` flag downloads and installs:
- Cloud Hypervisor + ch-remote (static binaries)
- CLOUDHV.fd firmware (rust-hypervisor-firmware)
- CNI plugins (bridge, host-local, loopback, etc.)

## Quick Start

```bash
# Set up the environment (first time)
sudo ./doctor/check.sh --upgrade

# Pull an OCI VM image
cocoon image pull ubuntu:24.04

# Or pull a cloud image from URL
cocoon image pull https://cloud-images.ubuntu.com/releases/24.04/release/ubuntu-24.04-server-cloudimg-amd64.img

# Create and start a VM
cocoon vm run --name my-vm --cpu 2 --memory 1G ubuntu:24.04

# Attach interactive console
cocoon vm console my-vm

# List running VMs
cocoon vm list

# Stop and delete
cocoon vm stop my-vm
cocoon vm rm my-vm
```

## CLI Commands

```
cocoon
├── image
│   ├── pull IMAGE [IMAGE...]      Pull OCI image(s) or cloud image URL(s)
│   ├── list (alias: ls)           List locally stored images
│   ├── rm ID [ID...]              Delete locally stored image(s)
│   └── inspect IMAGE              Show detailed image info (JSON)
├── vm
│   ├── create [flags] IMAGE       Create a VM from an image
│   ├── run [flags] IMAGE          Create and start a VM
│   ├── start VM [VM...]           Start created/stopped VM(s)
│   ├── stop VM [VM...]            Stop running VM(s)
│   ├── list (alias: ls)           List VMs with status
│   ├── inspect VM                 Show detailed VM info (JSON)
│   ├── console VM                 Attach interactive console
│   ├── rm [flags] VM [VM...]      Delete VM(s) (--force to stop first)
│   └── debug [flags] IMAGE        Generate CH launch command (dry run)
├── gc                             Remove unreferenced blobs and VM dirs
├── version                        Show version, revision, and build time
└── completion [bash|zsh|fish|powershell]
```

## Global Flags

| Flag | Env Variable | Default | Description |
|------|-------------|---------|-------------|
| `--config` | | | Config file path |
| `--root-dir` | `COCOON_ROOT_DIR` | `/var/lib/cocoon` | Root directory for persistent data |
| `--run-dir` | `COCOON_RUN_DIR` | `/var/run/cocoon` | Runtime directory for sockets and PIDs |
| `--log-dir` | `COCOON_LOG_DIR` | `/var/log/cocoon` | Log directory for VM serial logs |
| `--cni-conf-dir` | `COCOON_CNI_CONF_DIR` | `/etc/cni/net.d` | CNI plugin config directory |
| `--cni-bin-dir` | `COCOON_CNI_BIN_DIR` | `/opt/cni/bin` | CNI plugin binary directory |
| `--root-password` | `COCOON_DEFAULT_ROOT_PASSWORD` | | Default root password for cloudimg VMs |
| `--dns` | `COCOON_DNS` | `8.8.8.8,1.1.1.1` | DNS servers for VMs (comma separated) |

## VM Flags

Applies to `cocoon vm create`, `cocoon vm run`, and `cocoon vm debug`:

| Flag | Default | Description |
|------|---------|-------------|
| `--name` | `cocoon-<image>` | VM name |
| `--cpu` | `2` | Boot CPUs |
| `--memory` | `1G` | Memory size (e.g., 512M, 2G) |
| `--storage` | `10G` | COW disk size (e.g., 10G, 20G) |
| `--nics` | `1` | Number of network interfaces (0 = no network) |

## Networking

Cocoon uses [CNI](https://www.cni.dev/) for VM networking. Each NIC is backed by a TAP device wired through the CNI plugin chain.

- **Default**: 1 NIC with automatic IP assignment via CNI
- **No network**: `--nics 0` creates a VM with no network interfaces
- **Multi-NIC**: `--nics N` creates N interfaces; for cloudimg VMs all NICs are auto-configured, for OCI images only the last NIC is auto-configured (others need manual setup inside the guest)
- **DNS**: Use `--dns` to set custom DNS servers (comma separated), injected via kernel cmdline

CNI configuration is read from `--cni-conf-dir` (default `/etc/cni/net.d`). A typical bridge config:

```json
{
  "cniVersion": "1.0.0",
  "name": "cocoon",
  "type": "bridge",
  "bridge": "cni0",
  "isGateway": true,
  "ipMasq": true,
  "ipam": {
    "type": "host-local",
    "subnet": "10.22.0.0/16",
    "routes": [{ "dst": "0.0.0.0/0" }]
  }
}
```

## OS Images

Pre-built OCI VM images (Ubuntu 22.04, 24.04) are published to GHCR and auto-built by GitHub Actions when `os-image/` changes:

```bash
cocoon image pull ghcr.io/projecteru2/cocoon/ubuntu:24.04
cocoon image pull ghcr.io/projecteru2/cocoon/ubuntu:22.04
```

These images include kernel, initramfs, and a systemd-based rootfs with an overlayfs boot script.

## Shell Completion

```bash
# Bash
cocoon completion bash > /etc/bash_completion.d/cocoon

# Zsh
cocoon completion zsh > "${fpath[1]}/_cocoon"

# Fish
cocoon completion fish > ~/.config/fish/completions/cocoon.fish
```

## Development

```bash
make build    # Build cocoon binary (CGO_ENABLED=0)
make test     # Run tests with race detector and coverage
make lint     # Run golangci-lint
make vet      # Run go vet for linux and darwin
make fmt      # Format code with gofumpt + goimports
make ci       # Full CI pipeline: fmt-check + vet + lint + test + build
```

See `make help` for all available targets.

## License

This project is licensed under the MIT License. See [`LICENSE`](./LICENSE).
