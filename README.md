# Cocoon

Lightweight VM manager built on Cloud Hypervisor.

## Features

- **UEFI boot** -- CLOUDHV.fd UEFI firmware by default; direct kernel boot for OCI VM images is also supported (auto-detected from image metadata)
- **OCI VM images** -- pull OCI images with kernel + rootfs layers, content-addressed blob cache with SHA-256 deduplication
- **Cloud image support** -- pull from HTTP/HTTPS URLs, automatic qcow2 conversion
- **COW overlays** -- copy-on-write disks backed by shared base images (raw for OCI, qcow2 for cloud images)
- **Interactive console** -- `cocoon vm console` for bidirectional PTY access to running VMs, SSH-style escape sequences
- **Docker-like CLI** -- `cocoon vm create`, `cocoon vm start`, `cocoon vm stop`, `cocoon vm list`, `cocoon vm rm`
- **Zero-daemon architecture** -- one Cloud Hypervisor process per VM, no long-running daemon
- **Garbage collection** -- automatic tracking and lock-safe GC of unreferenced images, orphaned overlays, and expired temp entries

## Requirements

- Linux with KVM (x86_64)
- Root access (sudo)
- [Cloud Hypervisor](https://github.com/cloud-hypervisor/cloud-hypervisor) v38.0+
- `qemu-img` (from qemu-utils package, for cloud images)
- UEFI firmware (`CLOUDHV.fd`, for cloud images)
- Go 1.25+ (build only)

## Installation

### go install

```bash
go install github.com/projecteru2/cocoon@latest
```

### Build from source

```bash
git clone https://github.com/projecteru2/cocoon.git
cd cocoon
make build
```

This produces a `cocoon` binary in the project root. Use `make install` to install it into `$GOPATH/bin`.

## Quick Start

```bash
# Pull an OCI VM image
cocoon image pull ubuntu:24.04

# Or pull a cloud image from URL
cocoon image pull https://cloud-images.ubuntu.com/releases/24.04/release/ubuntu-24.04-server-cloudimg-amd64.img

# List cached images
cocoon image list

# Create and start a VM in one step
cocoon vm run --name my-vm --cpu 2 --memory 1G ubuntu:24.04

# Or create then start separately
cocoon vm create --name my-vm ubuntu:24.04
cocoon vm start my-vm

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
│   ├── run [flags] IMAGE          Create and start a VM from an image
│   ├── start VM [VM...]           Start created/stopped VM(s)
│   ├── stop VM [VM...]            Stop running VM(s)
│   ├── list (alias: ls)           List VMs with status
│   ├── inspect VM                 Show detailed VM info (JSON)
│   ├── console VM                 Attach interactive console to a running VM
│   ├── rm [flags] VM [VM...]      Delete VM(s) (--force to stop running VMs first)
│   └── debug [flags] IMAGE        Generate cloud-hypervisor launch command (dry run)
├── gc                             Remove unreferenced blobs, boot files, and VM dirs
├── version                        Show version, git revision, and build timestamp
└── completion [bash|zsh|fish|powershell]   Generate shell completion script
```

## Global Flags

| Flag | Env Variable | Default | Description |
|------|-------------|---------|-------------|
| `--config` | | | Config file path |
| `--root-dir` | `COCOON_ROOT_DIR` | `/var/lib/cocoon` | Root directory for persistent data |
| `--run-dir` | `COCOON_RUN_DIR` | `/var/run/cocoon` | Runtime directory for sockets and PIDs |
| `--log-dir` | `COCOON_LOG_DIR` | `/var/log/cocoon` | Log directory for VM serial logs |

## VM Flags

Applies to `cocoon vm create`, `cocoon vm run`, and `cocoon vm debug`:

| Flag | Default | Description |
|------|---------|-------------|
| `--name` | `cocoon-<image>` | VM name |
| `--cpu` | `2` | Boot CPUs |
| `--memory` | `1G` | Memory size (e.g., 512M, 2G) |
| `--storage` | `10G` | COW disk size (e.g., 10G, 20G) |

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
make lint     # Run golangci-lint (auto-downloads v2.9.0)
make vet      # Run go vet for linux and darwin
make fmt      # Format code with gofumpt + goimports
make ci       # Full CI pipeline: fmt-check + vet + lint + test + build
```

See `make help` for all available targets.

## License

This project is licensed under the MIT License. See [`LICENSE`](./LICENSE).
