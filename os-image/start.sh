#!/bin/bash
set -e

echo "=== Cocoon MicroVM Engine: Bootstrapping (Multi-Layer OCI) ==="

# 0. Check KVM
if [ ! -w "/dev/kvm" ]; then
    echo "Error: Access to /dev/kvm denied."
    exit 1
fi
ARCH=$(uname -m)

# 1. Download Cloud Hypervisor
CH_VERSION="v51.0"
CH_BIN="./cloud-hypervisor"
if [ ! -x "$CH_BIN" ]; then
    [ "$ARCH" = "x86_64" ] && CH_URL="https://github.com/cloud-hypervisor/cloud-hypervisor/releases/download/${CH_VERSION}/cloud-hypervisor-static" \
    || CH_URL="https://github.com/cloud-hypervisor/cloud-hypervisor/releases/download/${CH_VERSION}/cloud-hypervisor-static-aarch64"
    wget -qO "$CH_BIN" "$CH_URL" && chmod +x "$CH_BIN"
fi
sudo setcap cap_net_admin+ep "$CH_BIN"

# 2. Extract Layers via Crane
if [ -n "$IMAGE_NAME" ]; then
    echo "[2/5] Extracting layers for '$IMAGE_NAME'..."
    rm -f layer*.erofs boot/vmlinuz* boot/initrd.img*
    
    # Download Crane
    if [ ! -x "./crane" ]; then
        [ "$ARCH" = "x86_64" ] && CR_ARCH="x86_64" || CR_ARCH="arm64"
        wget -qO- "https://github.com/google/go-containerregistry/releases/download/v0.19.1/go-containerregistry_Linux_${CR_ARCH}.tar.gz" | tar -xz crane
    fi

    ./crane pull "$IMAGE_NAME" image.tar
    rm -rf image_temp && mkdir -p image_temp boot
    tar -xf image.tar -C image_temp

    # Process layers (Bottom up)
    LAYERS=$(python3 -c 'import json; print("\n".join(json.load(open("image_temp/manifest.json"))[0]["Layers"]))')
    
    IDX=0
    for L in $LAYERS; do
        echo "      Layer $IDX -> layer${IDX}.erofs"
        tar -xf "image_temp/$L" boot/ 2>/dev/null || true
        # Compress layer to EROFS
        if gzip -t "image_temp/$L" 2>/dev/null; then
            gzip -dc "image_temp/$L" | mkfs.erofs --tar=f -zlz4hc -C65536 -T0 -U 00000000-0000-0000-0000-000000000000 "layer${IDX}.erofs"
        else
            mkfs.erofs --tar=f -zlz4hc -C65536 -T0 -U 00000000-0000-0000-0000-000000000000 "layer${IDX}.erofs" "image_temp/$L"
        fi
        IDX=$((IDX+1))
    done
    rm -rf image_temp image.tar
fi

# 3. Prepare COW (Optimized for performance and sparse efficiency)
# lazy_itable_init=1 speeds up formatting on large sparse files
echo "[3/5] Preparing optimized sparse COW disk..."
truncate -s 10G cow.raw
mkfs.ext4 -F -m 0 -q -E lazy_itable_init=1,discard cow.raw

# 4. Build Arguments
KERNEL=$(ls boot/vmlinuz-* | head -1)
INITRD=$(ls boot/initrd.img-* | head -1)
LAYER_FILES=$(ls -1 layer*.erofs | sort -V)

DISK_CONFIGS=()
COCOON_LAYERS=""
I=0
for f in $LAYER_FILES; do
    DISK_CONFIGS+=("path=$f,readonly=on,serial=cocoon-layer${I}")
    # OverlayFS order: Top, ..., Bottom
    if [ -z "$COCOON_LAYERS" ]; then COCOON_LAYERS="cocoon-layer${I}"; else COCOON_LAYERS="cocoon-layer${I},${COCOON_LAYERS}"; fi
    I=$((I+1))
done

# COW Disk Optimization: 
# direct=on (Bypass host cache), sparse=on (VMM sparse awareness), 
# num_queues=2 (Match boot CPUs), queue_size=256 (Deep queue for better throughput)
DISK_CONFIGS+=("path=cow.raw,readonly=off,direct=on,sparse=on,image_type=raw,num_queues=2,queue_size=256,serial=cocoon-cow")

# 5. Ignite (Added RNG and Ballooning for production stability)
echo "[5/5] Igniting Cloud Hypervisor..."
"$CH_BIN" --kernel "$KERNEL" --initramfs "$INITRD" \
    --disk "${DISK_CONFIGS[@]}" \
    --cmdline "console=ttyS0 boot=cocoon cocoon.layers=${COCOON_LAYERS} cocoon.cow=cocoon-cow" \
    --cpus boot=2 --memory size=1024M \
    --rng \
    --balloon "size=1024M,deflate_on_oom=on,free_page_reporting=on" \
    --serial tty --console off