#!/bin/sh
# Filename: hack.sh
# Target path: /etc/initramfs-tools/scripts/cocoon

. /scripts/functions

resolve_disk() {
    local serial="$1" timeout="${COCOON_TIMEOUT:-10}" i=0
    case "$timeout" in ''|*[!0-9]*) timeout=10 ;; esac

    # Trigger udev to ensure device nodes are created before polling
    udevadm settle 2>/dev/null || true

    while [ $i -lt $timeout ]; do
        for sysdev in /sys/block/vd*; do
            [ -d "$sysdev" ] || continue
            local s=""
            [ -f "$sysdev/serial" ] && s=$(cat "$sysdev/serial")
            [ -f "$sysdev/device/serial" ] && s=$(cat "$sysdev/device/serial")

            # Trim trailing whitespace
            while :; do case "$s" in *[[:space:]]) s="${s%[[:space:]]}" ;; *) break ;; esac; done

            if [ "$s" = "$serial" ]; then
                echo "/dev/${sysdev##*/}"
                return 0
            fi
        done
        sleep 1
        i=$((i + 1))
    done
    return 1
}

mountroot() {
    log_begin_msg "Cocoon: mounting stealth overlay rootfs"

    # Native environment: modprobe automatically resolves all underlying dependencies.
    modprobe erofs 2>/dev/null || true
    modprobe overlay 2>/dev/null || true
    modprobe ext4 2>/dev/null || true

    for x in $(cat /proc/cmdline); do
        case $x in
            cocoon.layers=*) LAYERS="${x#cocoon.layers=}" ;;
            cocoon.cow=*)    COW="${x#cocoon.cow=}" ;;
            cocoon.timeout=*) COCOON_TIMEOUT="${x#cocoon.timeout=}" ;;
        esac
    done

    [ -z "$LAYERS" ] && panic "cocoon.layers= not set"
    [ -z "$COW" ]    && panic "cocoon.cow= not set"

    COCOON_INTERNAL="/.cocoon"
    mkdir -p "$COCOON_INTERNAL"

    # Mount read-only EROFS layers
    LOWER=""
    IFS=,
    for serial in $LAYERS; do
        dev=$(resolve_disk "$serial") || panic "device ${serial} not found"
        mnt="${COCOON_INTERNAL}/layers/${serial}"
        mkdir -p "$mnt"
        mount -t erofs -o ro "$dev" "$mnt" || panic "mount ${serial} failed"
        [ -n "$LOWER" ] && LOWER="${LOWER}:"
        LOWER="${LOWER}${mnt}"
    done
    unset IFS

    # Mount COW disk
    cow_dev=$(resolve_disk "$COW") || panic "COW device ${COW} not found"
    mkdir -p "${COCOON_INTERNAL}/cow"
    mount -t ext4 "$cow_dev" "${COCOON_INTERNAL}/cow" || panic "mount COW failed"
    mkdir -p "${COCOON_INTERNAL}/cow/upper" "${COCOON_INTERNAL}/cow/work"

    # Assemble Overlayfs
    # [Optimized OverlayFS Options]
    # index=on: Prevents broken file handles and ensures inode consistency during copy-up.
    # redirect_dir=on: Enables renaming of directories that exist in the lower (read-only) layers.
    OVL_OPTS="lowerdir=${LOWER},upperdir=${COCOON_INTERNAL}/cow/upper,workdir=${COCOON_INTERNAL}/cow/work,index=on,redirect_dir=on"
    
    mount -t overlay overlay -o "$OVL_OPTS" "${rootmnt}" || panic "overlay failed"

    mkdir -p "${rootmnt}/dev" "${rootmnt}/proc" "${rootmnt}/sys" "${rootmnt}/run"

    # [IO Performance Optimization]
    # Set the deadline scheduler to minimize guest-side CPU overhead for Virtio-Block devices.
    # Host-side IO optimization (direct=on) already handles the physical scheduling.
    for dev in /sys/block/vd*; do
        [ -e "$dev/queue/scheduler" ] && echo "mq-deadline" > "$dev/queue/scheduler" 2>/dev/null || true
    done

    # Note: The systemd compatibility hacks (clearing fstab, masking fsck) 
    # are handled natively in the Dockerfile. The rootfs is clean here.

    # The only remaining requirement is Machine-ID isolation for cloned VMs.
    rm -f "${rootmnt}/etc/machine-id" 2>/dev/null || true
    : > "${rootmnt}/etc/machine-id"

    log_success_msg "Cocoon: stealth overlay rootfs ready"
}