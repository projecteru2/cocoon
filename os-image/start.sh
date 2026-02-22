#!/bin/bash
set -e

echo "=== Cocoon MicroVM Engine: Bootstrapping (Multi-Layer OCI) ==="

# -----------------------------------------------------------------------------
# 0. Prerequisites Check
# -----------------------------------------------------------------------------
if [ ! -w "/dev/kvm" ]; then
    echo "Error: You do not have write access to /dev/kvm."
    echo "Please run: sudo usermod -aG kvm \$USER && newgrp kvm"
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
    else
        CH_URL="https://github.com/cloud-hypervisor/cloud-hypervisor/releases/download/${CH_VERSION}/cloud-hypervisor-static-aarch64"
    fi
    wget -qO "$CH_BIN" "$CH_URL"
    chmod +x "$CH_BIN"
fi

if ! getcap "$CH_BIN" | grep -q "cap_net_admin"; then
    echo "      Setting CAP_NET_ADMIN capabilities (requires sudo password once)..."
    sudo setcap cap_net_admin+ep "$CH_BIN"
fi

# -----------------------------------------------------------------------------
# 2. Daemonless Multi-Layer Pull & EROFS Extraction
# -----------------------------------------------------------------------------
if [ -n "$IMAGE_NAME" ]; then
    echo "[2/5] Daemonless pulling and separating layers for '$IMAGE_NAME'..."
    
    # Clean up any old layers to prevent collision
    rm -f layer*.erofs boot/vmlinuz* boot/initrd.img*
    
    CRANE_BIN="./crane"
    if [ ! -x "$CRANE_BIN" ]; then
        echo "      Downloading crane..."
        [ "$ARCH" = "x86_64" ] && CRANE_ARCH="x86_64" || CRANE_ARCH="arm64"
        CRANE_URL="https://github.com/google/go-containerregistry/releases/download/v0.19.1/go-containerregistry_Linux_${CRANE_ARCH}.tar.gz"
        wget -qO crane.tar.gz "$CRANE_URL"
        tar -xzf crane.tar.gz crane
        rm -f crane.tar.gz
        chmod +x "$CRANE_BIN"
    fi

    echo "      Pulling raw image layers to image.tar..."
    "$CRANE_BIN" pull "$IMAGE_NAME" image.tar

    echo "      Extracting and sorting OCI layers..."
    rm -rf image_temp && mkdir -p image_temp boot
    tar -xf image.tar -C image_temp

    # Parse Docker manifest.json to get layer paths in correct order (Base -> Top)
    LAYERS=$(python3 -c '
import json, sys
try:
    with open("image_temp/manifest.json") as f:
        for l in json.load(f)[0]["Layers"]: print(l)
except Exception as e:
    sys.exit(1)
')

    LAYER_INDEX=0
    for LAYER_PATH in $LAYERS; do
        echo "      Processing layer $LAYER_INDEX: $LAYER_PATH"
        LAYER_FILE="image_temp/$LAYER_PATH"
        
        # 1. Extract boot/ contents if present (newer layers overwrite older ones)
        tar -xf "$LAYER_FILE" boot/ 2>/dev/null || true
        
        # 2. Convert each layer dynamically to an individual EROFS disk.
        # Check if the layer is gzipped (Crane sometimes compresses them).
        if gzip -t "$LAYER_FILE" 2>/dev/null; then
            gzip -dc "$LAYER_FILE" | mkfs.erofs --tar=f -zlz4hc -C65536 -T0 -U 00000000-0000-0000-0000-000000000000 "layer${LAYER_INDEX}.erofs"
        else
            mkfs.erofs --tar=f -zlz4hc -C65536 -T0 -U 00000000-0000-0000-0000-000000000000 "layer${LAYER_INDEX}.erofs" "$LAYER_FILE"
        fi
        
        LAYER_INDEX=$((LAYER_INDEX+1))
    done

    echo "      Cleaning up raw tarballs..."
    rm -rf image_temp image.tar
    ls -lh layer*.erofs boot/vmlinuz* boot/initrd.img*
else
    echo "[2/5] IMAGE_NAME not set, assuming existing layers."
    if ! ls layer*.erofs 1> /dev/null 2>&1; then
        echo "Error: No layer*.erofs files found! Pass IMAGE_NAME to generate."
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
# 4. Dynamically Construct Cloud Hypervisor Arguments
# -----------------------------------------------------------------------------
echo "[4/5] Constructing CH arguments dynamically..."

KERNEL=$(ls boot/vmlinuz-* | head -n 1)
INITRD=$(ls boot/initrd.img-* | head -n 1)
LAYER_FILES=$(ls -1 layer*.erofs | sort -V)

CH_DISKS=()
COCOON_LAYERS=""
INDEX=0

for file in $LAYER_FILES; do
    # Add each layer as a separate Read-Only disk
    CH_DISKS+=("--disk")
    CH_DISKS+=("path=$file,readonly=on,serial=cocoon-layer${INDEX}")
    
    # MAGIC: OverlayFS `lowerdir` stacks right-to-left. 
    # To make sure top layers override base layers, we must PREPEND the layers.
    if [ -z "$COCOON_LAYERS" ]; then
        COCOON_LAYERS="cocoon-layer${INDEX}"
    else
        COCOON_LAYERS="cocoon-layer${INDEX},${COCOON_LAYERS}"
    fi
    INDEX=$((INDEX+1))
done

# Finally, append the Read-Write COW disk
CH_DISKS+=("--disk")
CH_DISKS+=("path=cow.raw,readonly=off,direct=off,image_type=raw,serial=cocoon-cow")

CMDLINE="console=ttyS0 boot=cocoon cocoon.layers=${COCOON_LAYERS} cocoon.cow=cocoon-cow"

# -----------------------------------------------------------------------------
# 5. Ignite Cloud Hypervisor (Multi-Disk Mode)
# -----------------------------------------------------------------------------
echo "[5/5] Igniting Cloud Hypervisor with $INDEX base layers..."
echo "      CMDLINE: $CMDLINE"

"$CH_BIN" \
    --kernel "$KERNEL" \
    --initramfs "$INITRD" \
    "${CH_DISKS[@]}" \
    --cmdline "$CMDLINE" \
    --cpus boot=2 \
    --memory size=1024M \
    --serial tty \
    --console off