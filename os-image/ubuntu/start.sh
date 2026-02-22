#!/bin/bash
set -e

echo "=== Cocoon MicroVM Engine: Bootstrapping (Rootless & Daemonless) ==="

# -----------------------------------------------------------------------------
# 0. Prerequisites Check: KVM Access
# -----------------------------------------------------------------------------
if [ ! -w "/dev/kvm" ]; then
    echo "Error: You do not have write access to /dev/kvm."
    echo "Please run the following commands and then re-run this script:"
    echo "  sudo usermod -aG kvm \$USER"
    echo "  newgrp kvm"
    exit 1
fi

ARCH=$(uname -m)

# -----------------------------------------------------------------------------
# 1. Download Cloud Hypervisor & Setup Capabilities
# -----------------------------------------------------------------------------
CH_VERSION="v51.0"
CH_BIN="./cloud-hypervisor"

if [ ! -x "$CH_BIN" ]; then
    echo "[1/5] Downloading Cloud Hypervisor ${CH_VERSION} for ${ARCH}..."
    if [ "$ARCH" = "x86_64" ]; then
        CH_URL="https://github.com/cloud-hypervisor/cloud-hypervisor/releases/download/${CH_VERSION}/cloud-hypervisor-static"
    elif [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
        CH_URL="https://github.com/cloud-hypervisor/cloud-hypervisor/releases/download/${CH_VERSION}/cloud-hypervisor-static-aarch64"
    else
        echo "Error: Unsupported architecture $ARCH"
        exit 1
    fi
    
    wget -qO "$CH_BIN" "$CH_URL"
    chmod +x "$CH_BIN"
    echo "      Download complete!"
else
    echo "[1/5] Native Cloud Hypervisor binary found, skipping download."
fi

# Ensure the binary has the required capabilities for network setup (Rootless)
if ! getcap "$CH_BIN" | grep -q "cap_net_admin"; then
    echo "      Setting CAP_NET_ADMIN capabilities (requires sudo password once)..."
    sudo setcap cap_net_admin+ep "$CH_BIN"
fi

# -----------------------------------------------------------------------------
# 2. Daemonless Extract Image to EROFS (Triggered by IMAGE_NAME env var)
# -----------------------------------------------------------------------------
if [ -n "$IMAGE_NAME" ]; then
    echo "[2/5] Daemonless extracting image '$IMAGE_NAME' to EROFS..."
    
    CRANE_BIN="./crane"
    if [ ! -x "$CRANE_BIN" ]; then
        echo "      Downloading crane for daemonless image pull..."
        CRANE_VER="v0.19.1"
        if [ "$ARCH" = "x86_64" ]; then
            CRANE_ARCH="x86_64"
        elif [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
            CRANE_ARCH="arm64"
        else
            echo "Error: Unsupported architecture for crane: $ARCH"
            exit 1
        fi
        
        CRANE_URL="https://github.com/google/go-containerregistry/releases/download/${CRANE_VER}/go-containerregistry_Linux_${CRANE_ARCH}.tar.gz"
        wget -qO crane.tar.gz "$CRANE_URL"
        tar -xzf crane.tar.gz crane
        rm -f crane.tar.gz
        chmod +x "$CRANE_BIN"
    fi

    echo "      Pulling and exporting image to rootfs.tar via Crane..."
    "$CRANE_BIN" export "$IMAGE_NAME" rootfs.tar

    echo "      Extracting boot/ directory from tarball..."
    mkdir -p boot
    # Extract only the boot directory from the tarball
    tar -xf rootfs.tar boot/

    echo "      Compressing to EROFS (this may take a moment)..."
    mkfs.erofs --tar=f -zlz4hc -C65536 -T0 -U 00000000-0000-0000-0000-000000000000 rootfs.erofs rootfs.tar
    
    echo "      Cleaning up temporary files..."
    rm -f rootfs.tar
    
    echo "      Extraction complete. Generated files:"
    ls -lh boot/vmlinuz* boot/initrd.img* rootfs.erofs
else
    echo "[2/5] IMAGE_NAME not set, skipping extraction."
    if [ ! -f "rootfs.erofs" ] || [ ! -d "boot" ]; then
        echo "Error: 'rootfs.erofs' or 'boot/' directory not found!"
        echo "Please run with IMAGE_NAME env var to generate them. Example:"
        echo "  IMAGE_NAME=\"cmgs/ubuntu:24.04\" ./start.sh"
        exit 1
    fi
fi

# -----------------------------------------------------------------------------
# 3. Recreate a clean 10G COW disk before each boot.
# -----------------------------------------------------------------------------
echo "[3/5] Preparing sparse COW disk..."
rm -f cow.raw
truncate -s 10G cow.raw
mkfs.ext4 -F -m 0 -q cow.raw

# -----------------------------------------------------------------------------
# 4. Automatically locate the kernel and initramfs in the boot/ directory.
# -----------------------------------------------------------------------------
echo "[4/5] Resolving Kernel & Initramfs..."
KERNEL=$(ls boot/vmlinuz-* | head -n 1)
INITRD=$(ls boot/initrd.img-* | head -n 1)

echo "      Kernel: $KERNEL"
echo "      Initrd: $INITRD"

# -----------------------------------------------------------------------------
# 5. Ignite Cloud Hypervisor (Rootless)
# -----------------------------------------------------------------------------
echo "[5/5] Igniting Cloud Hypervisor (Rootless Mode)..."

# Execute WITHOUT sudo!
"$CH_BIN" \
    --kernel "$KERNEL" \
    --initramfs "$INITRD" \
    --disk "path=rootfs.erofs,readonly=on,serial=cocoon-layer0" \
           "path=cow.raw,readonly=off,direct=off,image_type=raw,serial=cocoon-cow" \
    --cmdline "console=ttyS0 boot=cocoon cocoon.layers=cocoon-layer0 cocoon.cow=cocoon-cow" \
    --cpus boot=2 \
    --memory size=1024M \
    --serial tty \
    --console off