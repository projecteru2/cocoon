#!/bin/sh
# Filename: cocoon-boot.sh
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
    log_begin_msg "Cocoon: mounting native overlay rootfs"

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

    COCOON="/run/cocoon/storage"
    mkdir -p "$COCOON"

    # Mount read-only EROFS layers
    LOWER=""
    IFS=,
    for serial in $LAYERS; do
        dev=$(resolve_disk "$serial") || panic "device ${serial} not found"
        mnt="${COCOON}/layers/${serial}"
        mkdir -p "$mnt"
        mount -t erofs -o ro "$dev" "$mnt" || panic "mount ${serial} failed"
        [ -n "$LOWER" ] && LOWER="${LOWER}:"
        LOWER="${LOWER}${mnt}"
    done
    unset IFS

    # Mount COW disk
    cow_dev=$(resolve_disk "$COW") || panic "COW device ${COW} not found"
    mkdir -p "${COCOON}/cow"
    mount -t ext4 "$cow_dev" "${COCOON}/cow" || panic "mount COW failed"
    mkdir -p "${COCOON}/cow/upper" "${COCOON}/cow/work"

    # Assemble Overlayfs
    OVL_OPTS="lowerdir=${LOWER},upperdir=${COCOON}/cow/upper,workdir=${COCOON}/cow/work"
    mount -t overlay overlay -o "$OVL_OPTS" "${rootmnt}" || panic "overlay failed"

    mkdir -p "${rootmnt}/dev" "${rootmnt}/proc" "${rootmnt}/sys" "${rootmnt}/run"

    # Note: The systemd compatibility hacks (clearing fstab, masking fsck) 
    # are handled natively in the Dockerfile. The rootfs is clean here.

    # The only remaining requirement is Machine-ID isolation for cloned VMs.
    rm -f "${rootmnt}/etc/machine-id" 2>/dev/null || true
    : > "${rootmnt}/etc/machine-id"

    log_success_msg "Cocoon: native overlay rootfs ready"
}
