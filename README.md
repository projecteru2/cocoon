# Cocoon

Lightweight MicroVM engine built on [Cloud Hypervisor](https://github.com/cloud-hypervisor/cloud-hypervisor).

## Features

- **OCI VM images** — pull OCI images with kernel + rootfs layers, content-addressed blob cache with SHA-256 deduplication
- **Cloud image support** — pull from HTTP/HTTPS URLs (e.g. Ubuntu cloud images), automatic qcow2 conversion
- **UEFI boot** — CLOUDHV.fd firmware by default; direct kernel boot for OCI images (auto-detected)
- **COW overlays** — copy-on-write disks backed by shared base images (raw for OCI, qcow2 for cloud images)
- **CNI networking** — automatic NIC creation via CNI plugins, multi-NIC support, per-VM IP allocation
- **Multi-queue virtio-net** — TAP devices created with per-vCPU queue pairs; TSO/UFO/csum offload enabled by default
- **TC redirect I/O path** — veth ↔ TAP wired via ingress qdisc + mirred redirect (no bridge in the data path)
- **DNS configuration** — custom DNS servers injected into VMs via kernel cmdline (OCI) or cloud-init network-config (cloudimg)
- **Cloud-init metadata** — automatic NoCloud cidata FAT12 disk for cloudimg VMs (hostname, root password, multi-NIC Netplan v2 network-config); cidata is automatically skipped on subsequent boots
- **Hugepages** — automatic detection of host hugepage configuration; VM memory backed by hugepages when available
- **Memory balloon** — 25% of memory returned via virtio-balloon (deflate-on-OOM, free-page reporting) when memory >= 256 MiB
- **Graceful shutdown** — ACPI power-button for UEFI VMs with configurable timeout, fallback to SIGTERM → SIGKILL
- **Interactive console** — `cocoon vm console` with bidirectional PTY relay, SSH-style escape sequences (`~.` disconnect, `~?` help), configurable escape character, SIGWINCH propagation
- **Docker-like CLI** — `create`, `run`, `start`, `stop`, `list`, `inspect`, `console`, `rm`, `debug`
- **Structured logging** — configurable log level (`--log-level`), log rotation (max size / age / backups)
- **Debug command** — `cocoon vm debug` generates a copy-pasteable `cloud-hypervisor` command for manual debugging
- **Zero-daemon architecture** — one Cloud Hypervisor process per VM, no long-running daemon
- **Garbage collection** — modular lock-safe GC with cross-module snapshot resolution; protects blobs referenced by running VMs
- **Doctor script** — pre-flight environment check and one-command dependency installation

## Requirements

- Linux with KVM (x86_64 or aarch64)
- Root access (sudo)
- [Cloud Hypervisor](https://github.com/cloud-hypervisor/cloud-hypervisor) v51.0+
- `qemu-img` (from qemu-utils, for cloud images)
- UEFI firmware (`CLOUDHV.fd`, for cloud images)
- CNI plugins (`bridge`, `host-local`, `loopback`)
- Go 1.25+ (build only)

## Installation

### GitHub Releases

Download pre-built binaries from [GitHub Releases](https://github.com/projecteru2/cocoon/releases):

```bash
# Linux amd64
curl -fsSL -o cocoon https://github.com/projecteru2/cocoon/releases/download/v0.1.5/cocoon_0.1.5_Linux_x86_64.tar.gz
tar -xzf cocoon_0.1.5_Linux_x86_64.tar.gz
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
# Get script
curl -fsSL -o cocoon-check https://raw.githubusercontent.com/projecteru2/cocoon/refs/heads/master/doctor/check.sh
install -m 0755 cocoon-check /usr/local/bin/

# Check only — reports PASS/FAIL for each requirement
cocoon-check

# Check and fix — creates directories, sets sysctl, adds iptables rules
cocoon-check --fix

# Full setup — install cloud-hypervisor, firmware, and CNI plugins
cocoon-check --upgrade
```

The `--upgrade` flag downloads and installs:
- Cloud Hypervisor + ch-remote (static binaries)
- CLOUDHV.fd firmware (rust-hypervisor-firmware)
- CNI plugins (bridge, host-local, loopback, etc.)

## Quick Start

```bash
# Set up the environment (first time)
sudo cocoon-check --upgrade

# Pull an OCI VM image
cocoon image pull ghcr.io/projecteru2/cocoon/ubuntu:24.04

# Or pull a cloud image from URL
cocoon image pull https://cloud-images.ubuntu.com/releases/22.04/release/ubuntu-22.04-server-cloudimg-amd64.img

# Create and start a VM
cocoon vm run --name my-vm --cpu 2 --memory 1G ghcr.io/projecteru2/cocoon/ubuntu:24.04

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
│   ├── console [flags] VM         Attach interactive console
│   ├── rm [flags] VM [VM...]      Delete VM(s) (--force to stop first)
│   └── debug [flags] IMAGE        Generate CH launch command (dry run)
├── gc                             Remove unreferenced blobs and VM dirs
├── version                        Show version, revision, and build time
└── completion [bash|zsh|fish|powershell]
```

## Global Flags

| Flag              | Env Variable                   | Default            | Description                            |
| ----------------- | ------------------------------ | ------------------ | -------------------------------------- |
| `--config`        |                                |                    | Config file path                       |
| `--root-dir`      | `COCOON_ROOT_DIR`              | `/var/lib/cocoon`  | Root directory for persistent data     |
| `--run-dir`       | `COCOON_RUN_DIR`               | `/var/lib/cocoon/run` | Runtime directory for sockets and PIDs |
| `--log-dir`       | `COCOON_LOG_DIR`               | `/var/log/cocoon`  | Log directory for VM and process logs  |
| `--log-level`     | `COCOON_LOG_LEVEL`             | `info`             | Log level: debug, info, warn, error    |
| `--cni-conf-dir`  | `COCOON_CNI_CONF_DIR`          | `/etc/cni/net.d`   | CNI plugin config directory            |
| `--cni-bin-dir`   | `COCOON_CNI_BIN_DIR`           | `/opt/cni/bin`     | CNI plugin binary directory            |
| `--root-password` | `COCOON_DEFAULT_ROOT_PASSWORD` |                    | Default root password for cloudimg VMs |
| `--dns`           | `COCOON_DNS`                   | `8.8.8.8,1.1.1.1`  | DNS servers for VMs (comma separated)  |

## VM Flags

Applies to `cocoon vm create`, `cocoon vm run`, and `cocoon vm debug`:

| Flag        | Default          | Description                                   |
| ----------- | ---------------- | --------------------------------------------- |
| `--name`    | `cocoon-<image>` | VM name                                       |
| `--cpu`     | `2`              | Boot CPUs                                     |
| `--memory`  | `1G`             | Memory size (e.g., 512M, 2G)                  |
| `--storage` | `10G`            | COW disk size (e.g., 10G, 20G)                |
| `--nics`    | `1`              | Number of network interfaces (0 = no network) |

### Debug-only Flags

Applies to `cocoon vm debug`:

| Flag        | Default              | Description                                        |
| ----------- | -------------------- | -------------------------------------------------- |
| `--max-cpu` | `8`                  | Max CPUs for the generated command                  |
| `--balloon` | `0`                  | Balloon size in MB (0 = auto)                       |
| `--cow`     |                      | COW disk path (default: auto-generated)             |
| `--ch`      | `cloud-hypervisor`   | cloud-hypervisor binary path                        |

### Console Flags

| Flag             | Default  | Description                                       |
| ---------------- | -------- | ------------------------------------------------- |
| `--escape-char`  | `^]`     | Escape character (single char or `^X` caret notation) |

## Networking

Cocoon uses [CNI](https://www.cni.dev/) for VM networking. Each NIC is backed by a TAP device wired to the CNI veth via TC ingress redirect — no bridge sits in the data path.

### Architecture

```
Guest virtio-net  ←→  TAP (multi-queue)  ←TC redirect→  veth  ←→  CNI bridge/overlay
```

- **Multi-queue**: each TAP device is created with one queue pair per boot vCPU (`num_queues = 2 × vCPU` in Cloud Hypervisor), enabling per-CPU TX/RX rings for better throughput
- **Offload**: TSO, UFO, and checksum offload are enabled on the virtio-net device; TAP uses `VNET_HDR` for zero-copy GSO passthrough
- **MAC passthrough**: the guest NIC inherits the CNI veth's MAC address, satisfying anti-spoofing requirements of Cilium, Calico eBPF, and VPC ENI plugins
- **MTU sync**: TAP MTU is automatically synced to the veth to prevent silent large-packet drops in overlay or jumbo-frame setups

### Options

- **Default**: 1 NIC with automatic IP assignment via CNI
- **No network**: `--nics 0` creates a VM with no network interfaces
- **Multi-NIC**: `--nics N` creates N interfaces; for cloudimg VMs all NICs are auto-configured via Netplan, for OCI images only the last NIC is auto-configured (others need manual setup inside the guest)
- **DNS**: Use `--dns` to set custom DNS servers (comma separated)

### CNI Configuration

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

## Cloud-init & First Boot

Cloudimg VMs receive a NoCloud cidata disk (FAT12 with `CIDATA` volume label) containing:

- **meta-data**: instance ID and hostname
- **user-data**: `#cloud-config` with optional root password (`--root-password`)
- **network-config**: Netplan v2 format with MAC-matched ethernets, static IP/gateway/DNS per NIC

The cidata disk is **automatically excluded on subsequent boots** — after the first successful start, the VM record is marked as `first_booted` and the cidata disk is no longer attached, preventing cloud-init from re-running.

## VM Lifecycle

| State      | Description                                              |
| ---------- | -------------------------------------------------------- |
| `creating` | DB placeholder written, disks being prepared             |
| `created`  | Registered, cloud-hypervisor process not yet started     |
| `running`  | Cloud-hypervisor process alive, guest is up              |
| `stopped`  | Cloud-hypervisor process exited cleanly                  |
| `error`    | Start or stop failed                                     |

### Shutdown Behavior

- **UEFI VMs (cloudimg)**: ACPI power-button → poll for graceful exit → timeout (default 30s, configurable via `stop_timeout_seconds` in config) → SIGTERM → 5s → SIGKILL
- **Direct-boot VMs (OCI)**: `vm.shutdown` API → SIGTERM → 5s → SIGKILL (no ACPI support)
- PID ownership is verified before sending signals to prevent killing unrelated processes

## Performance Tuning

- **Hugepages**: automatically detected from `/proc/sys/vm/nr_hugepages`; when available, VM memory is backed by 2 MiB hugepages for reduced TLB pressure
- **Disk I/O**: multi-queue virtio-blk with `num_queues` matching boot CPUs and `queue_size=256`; direct I/O for EROFS layers and COW raw disks
- **Balloon**: 25% of memory auto-returned via virtio-balloon with deflate-on-OOM and free-page reporting (VMs with < 256 MiB memory skip balloon)
- **Watchdog**: hardware watchdog enabled by default for automatic guest reset on hang

## Garbage Collection

`cocoon gc` performs cross-module garbage collection:

1. **Lock** all modules (images, VMs, network) — if any module is busy, the entire GC cycle is skipped to maintain consistency
2. **Snapshot** all module indexes under lock
3. **Resolve** each module identifies unreferenced resources using the full snapshot set (e.g., image GC checks VM snapshots for blob references)
4. **Collect** — delete identified targets

This ensures blobs referenced by running VMs are never deleted.

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

## Known Limitations

### Cloud image UEFI boot compatibility

Cocoon uses [rust-hypervisor-firmware](https://github.com/cloud-hypervisor/rust-hypervisor-firmware) (`CLOUDHV.fd`) for cloud image UEFI boot. This firmware implements a minimal EFI specification and does **not** support the `InstallMultipleProtocolInterfaces()` call required by newer distributions.

**Affected images** (kernel panic on boot — GRUB loads kernel but not initrd):

- Ubuntu 24.04 (Noble) and later
- Debian 13 (Trixie) and later

**Working images**:

- Ubuntu 22.04 (Jammy)

This is an upstream issue tracked in [rust-hypervisor-firmware#333](https://github.com/cloud-hypervisor/rust-hypervisor-firmware/issues/333) and [cloud-hypervisor#7356](https://github.com/cloud-hypervisor/cloud-hypervisor/issues/7356). As a workaround, use **OCI VM images** for Ubuntu 24.04 — OCI images use direct kernel boot and are not affected.

## License

This project is licensed under the MIT License. See [`LICENSE`](./LICENSE).
