#!/usr/bin/env bash
# doctor/check.sh — Pre-flight check and repair tool for Cocoon.
#
# Usage:
#   ./doctor/check.sh              # Check only
#   ./doctor/check.sh --fix        # Check and fix issues
#   ./doctor/check.sh --upgrade    # Check, fix, and upgrade dependencies

set -euo pipefail

# ---------------------------------------------------------------------------
# Configuration (override via environment)
# ---------------------------------------------------------------------------
COCOON_ROOT_DIR="${COCOON_ROOT_DIR:-/var/lib/cocoon}"
COCOON_RUN_DIR="${COCOON_RUN_DIR:-/var/run/cocoon}"
COCOON_LOG_DIR="${COCOON_LOG_DIR:-/var/log/cocoon}"
COCOON_CNI_CONF_DIR="${COCOON_CNI_CONF_DIR:-/etc/cni/net.d}"
COCOON_CNI_BIN_DIR="${COCOON_CNI_BIN_DIR:-/opt/cni/bin}"

# Dependency versions
CH_VERSION="${CH_VERSION:-v51.1}"
FW_VERSION="${FW_VERSION:-0.5.0}"
CNI_VERSION="${CNI_VERSION:-v1.9.0}"

# Architecture detection
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  GO_ARCH="amd64"; CH_SUFFIX=""; FW_SUFFIX="" ;;
    aarch64) GO_ARCH="arm64";  CH_SUFFIX="-aarch64"; FW_SUFFIX="-aarch64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

FIRMWARE_DIR="${COCOON_ROOT_DIR}/firmware"
FIRMWARE_PATH="${FIRMWARE_DIR}/CLOUDHV.fd"

# ---------------------------------------------------------------------------
# Flags
# ---------------------------------------------------------------------------
FIX=false
UPGRADE=false
for arg in "$@"; do
    case "$arg" in
        --fix)     FIX=true ;;
        --upgrade) FIX=true; UPGRADE=true ;;
        -h|--help)
            cat <<EOF
Usage: $0 [--fix] [--upgrade]

Options:
  --fix      Attempt to fix detected issues (dirs, sysctl, iptables)
  --upgrade  Fix issues and install/upgrade dependencies:
               cloud-hypervisor ${CH_VERSION}
               hypervisor-fw    ${FW_VERSION}
               CNI plugins      ${CNI_VERSION}

Environment variables:
  CH_VERSION    Cloud Hypervisor version    (default: ${CH_VERSION})
  FW_VERSION    Firmware version            (default: ${FW_VERSION})
  CNI_VERSION   CNI plugins version         (default: ${CNI_VERSION})
  COCOON_ROOT_DIR / COCOON_RUN_DIR / COCOON_LOG_DIR
  COCOON_CNI_CONF_DIR / COCOON_CNI_BIN_DIR
EOF
            exit 0
            ;;
    esac
done

# ---------------------------------------------------------------------------
# Output helpers
# ---------------------------------------------------------------------------
PASS=0; WARN=0; FAIL=0

pass()   { PASS=$((PASS + 1)); printf "  \033[32m[PASS]\033[0m %s\n" "$1"; }
warn()   { WARN=$((WARN + 1)); printf "  \033[33m[WARN]\033[0m %s\n" "$1"; }
fail()   { FAIL=$((FAIL + 1)); printf "  \033[31m[FAIL]\033[0m %s\n" "$1"; }
info()   { printf "  \033[36m[INFO]\033[0m %s\n" "$1"; }
fixed()  { printf "  \033[32m[FIXED]\033[0m %s\n" "$1"; }
header() { printf "\n\033[1m==> %s\033[0m\n" "$1"; }

# ---------------------------------------------------------------------------
# 1. Binary dependencies
# ---------------------------------------------------------------------------
header "Binary dependencies"

check_binary() {
    local name="$1"
    if command -v "$name" &>/dev/null; then
        local ver=""
        case "$name" in
            cloud-hypervisor) ver=$("$name" --version 2>/dev/null | head -1) ;;
            ch-remote)        ver=$("$name" --version 2>/dev/null | head -1) ;;
            qemu-img)         ver=$("$name" --version 2>/dev/null | head -1) ;;
            mkfs.ext4)        ver=$("$name" -V 2>&1 | head -1) ;;
            mkfs.erofs)       ver=$("$name" --version 2>&1 | head -1) ;;
        esac
        pass "${name}${ver:+ ($ver)}"
    else
        fail "$name not found in PATH"
    fi
}

check_binary cloud-hypervisor
check_binary ch-remote
check_binary qemu-img
check_binary mkfs.ext4
check_binary mkfs.erofs

# ---------------------------------------------------------------------------
# 2. Firmware
# ---------------------------------------------------------------------------
header "Firmware"

if [ -f "$FIRMWARE_PATH" ]; then
    local_size=$(stat -c%s "$FIRMWARE_PATH" 2>/dev/null || stat -f%z "$FIRMWARE_PATH" 2>/dev/null || echo 0)
    pass "CLOUDHV.fd (${local_size} bytes) at $FIRMWARE_PATH"
else
    fail "CLOUDHV.fd not found at $FIRMWARE_PATH"
fi

# ---------------------------------------------------------------------------
# 3. KVM access
# ---------------------------------------------------------------------------
header "KVM"

if [ -e /dev/kvm ]; then
    if [ -r /dev/kvm ] && [ -w /dev/kvm ]; then
        pass "/dev/kvm accessible"
    else
        fail "/dev/kvm exists but not readable/writable by $(whoami)"
        if $FIX; then
            chmod 666 /dev/kvm 2>/dev/null && fixed "chmod 666 /dev/kvm" || warn "failed to fix (need root?)"
        fi
    fi
else
    fail "/dev/kvm not found (nested virtualization or bare-metal required)"
fi

# ---------------------------------------------------------------------------
# 4. Runtime directories
# ---------------------------------------------------------------------------
header "Directories"

check_dir() {
    local dir="$1"
    if [ -d "$dir" ]; then
        pass "$dir"
    else
        fail "$dir does not exist"
        if $FIX; then
            mkdir -p "$dir" && fixed "created $dir" || warn "failed to create $dir"
        fi
    fi
}

check_dir "$COCOON_ROOT_DIR"
check_dir "$COCOON_RUN_DIR"
check_dir "$COCOON_LOG_DIR"
check_dir "${COCOON_ROOT_DIR}/cloudhypervisor/db"
check_dir "${COCOON_ROOT_DIR}/cni/db"
check_dir "${COCOON_ROOT_DIR}/cni/cache"
check_dir "${COCOON_ROOT_DIR}/oci/db"
check_dir "${COCOON_ROOT_DIR}/oci/blobs"
check_dir "${COCOON_ROOT_DIR}/oci/boot"
check_dir "${COCOON_ROOT_DIR}/cloudimg/db"
check_dir "${COCOON_ROOT_DIR}/cloudimg/blobs"
check_dir "${FIRMWARE_DIR}"
check_dir /var/run/netns

# ---------------------------------------------------------------------------
# 5. Sysctl
# ---------------------------------------------------------------------------
header "Sysctl"

check_sysctl() {
    local key="$1"
    local expected="$2"
    local actual
    actual=$(sysctl -n "$key" 2>/dev/null || echo "")
    if [ "$actual" = "$expected" ]; then
        pass "$key = $expected"
    else
        fail "$key = ${actual:-<unset>} (expected $expected)"
        if $FIX; then
            sysctl -w "${key}=${expected}" &>/dev/null && fixed "sysctl -w ${key}=${expected}" || warn "failed to set $key"
        fi
    fi
}

check_sysctl net.ipv4.ip_forward 1
check_sysctl net.bridge.bridge-nf-call-iptables 1

# ---------------------------------------------------------------------------
# 6. iptables FORWARD rules for CNI bridge
# ---------------------------------------------------------------------------
header "iptables FORWARD (cni0)"

check_iptables_rule() {
    local desc="$1"
    shift
    if iptables -C "$@" 2>/dev/null; then
        pass "$desc"
    else
        fail "$desc"
        if $FIX; then
            iptables -A "$@" 2>/dev/null && fixed "iptables -A $*" || warn "failed to add rule"
        fi
    fi
}

check_iptables_rule "FORWARD -i cni0 -j ACCEPT" \
    FORWARD -i cni0 -j ACCEPT
check_iptables_rule "FORWARD -o cni0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT" \
    FORWARD -o cni0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT

# ---------------------------------------------------------------------------
# 7. CNI configuration
# ---------------------------------------------------------------------------
header "CNI configuration"

if [ -d "$COCOON_CNI_CONF_DIR" ]; then
    conflist_count=$(find "$COCOON_CNI_CONF_DIR" -maxdepth 1 -name '*.conflist' 2>/dev/null | wc -l)
    if [ "$conflist_count" -gt 0 ]; then
        first=$(find "$COCOON_CNI_CONF_DIR" -maxdepth 1 -name '*.conflist' 2>/dev/null | sort | head -1)
        pass "conflist: $(basename "$first")"
    else
        fail "no .conflist files in $COCOON_CNI_CONF_DIR"
    fi
else
    fail "$COCOON_CNI_CONF_DIR does not exist"
    if $FIX; then
        mkdir -p "$COCOON_CNI_CONF_DIR" && fixed "created $COCOON_CNI_CONF_DIR" || warn "failed"
    fi
fi

# ---------------------------------------------------------------------------
# 8. CNI plugins
# ---------------------------------------------------------------------------
header "CNI plugins (${COCOON_CNI_BIN_DIR})"

CNI_REQUIRED="bridge host-local loopback"

if [ -d "$COCOON_CNI_BIN_DIR" ]; then
    for plugin in $CNI_REQUIRED; do
        if [ -x "${COCOON_CNI_BIN_DIR}/${plugin}" ]; then
            pass "$plugin"
        else
            fail "$plugin not found"
        fi
    done
else
    fail "$COCOON_CNI_BIN_DIR does not exist"
fi

# ---------------------------------------------------------------------------
# 9. Upgrade / Install
# ---------------------------------------------------------------------------
if $UPGRADE; then
    tmpdir=$(mktemp -d)
    trap 'rm -rf "$tmpdir"' EXIT

    # -- cloud-hypervisor --------------------------------------------------
    header "Install cloud-hypervisor ${CH_VERSION}"

    ch_url="https://github.com/cloud-hypervisor/cloud-hypervisor/releases/download/${CH_VERSION}/cloud-hypervisor-static${CH_SUFFIX}"
    ch_dest="/usr/local/bin/cloud-hypervisor"
    info "downloading ${ch_url}"
    if curl -fsSL -o "${tmpdir}/cloud-hypervisor" "$ch_url"; then
        install -m 0755 "${tmpdir}/cloud-hypervisor" "$ch_dest"
        # virtio-net requires CAP_NET_ADMIN for tap devices
        setcap cap_net_admin+ep "$ch_dest" 2>/dev/null || true
        fixed "cloud-hypervisor ${CH_VERSION} -> ${ch_dest}"
    else
        fail "failed to download cloud-hypervisor from ${ch_url}"
    fi

    # -- ch-remote ----------------------------------------------------------
    header "Install ch-remote ${CH_VERSION}"

    chr_url="https://github.com/cloud-hypervisor/cloud-hypervisor/releases/download/${CH_VERSION}/ch-remote-static${CH_SUFFIX}"
    chr_dest="/usr/local/bin/ch-remote"
    info "downloading ${chr_url}"
    if curl -fsSL -o "${tmpdir}/ch-remote" "$chr_url"; then
        install -m 0755 "${tmpdir}/ch-remote" "$chr_dest"
        fixed "ch-remote ${CH_VERSION} -> ${chr_dest}"
    else
        fail "failed to download ch-remote from ${chr_url}"
    fi

    # -- firmware -----------------------------------------------------------
    header "Install hypervisor-fw ${FW_VERSION}"

    fw_url="https://github.com/cloud-hypervisor/rust-hypervisor-firmware/releases/download/${FW_VERSION}/hypervisor-fw${FW_SUFFIX}"
    mkdir -p "$FIRMWARE_DIR"
    info "downloading ${fw_url}"
    if curl -fsSL -o "${FIRMWARE_PATH}" "$fw_url"; then
        fixed "hypervisor-fw ${FW_VERSION} -> ${FIRMWARE_PATH}"
    else
        fail "failed to download firmware from ${fw_url}"
    fi

    # -- CNI plugins --------------------------------------------------------
    header "Install CNI plugins ${CNI_VERSION}"

    cni_tarball="cni-plugins-linux-${GO_ARCH}-${CNI_VERSION}.tgz"
    cni_url="https://github.com/containernetworking/plugins/releases/download/${CNI_VERSION}/${cni_tarball}"
    info "downloading ${cni_url}"
    if curl -fsSL -o "${tmpdir}/${cni_tarball}" "$cni_url"; then
        mkdir -p "$COCOON_CNI_BIN_DIR"
        tar -xzf "${tmpdir}/${cni_tarball}" -C "$COCOON_CNI_BIN_DIR"
        fixed "CNI plugins ${CNI_VERSION} -> ${COCOON_CNI_BIN_DIR}"
        info "installed plugins:"
        for p in "$COCOON_CNI_BIN_DIR"/*; do
            [ -x "$p" ] && info "  $(basename "$p")"
        done
    else
        fail "failed to download CNI plugins from ${cni_url}"
    fi
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
printf "\n\033[1m--- Summary ---\033[0m\n"
printf "  Pass: %d  Warn: %d  Fail: %d\n\n" "$PASS" "$WARN" "$FAIL"

if [ "$FAIL" -gt 0 ] && ! $FIX; then
    info "Run '$0 --fix' to attempt automatic fixes"
    info "Run '$0 --upgrade' to install/upgrade cloud-hypervisor, firmware, and CNI plugins"
fi

[ "$FAIL" -eq 0 ] || exit 1
