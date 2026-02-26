#!/bin/sh
# Filename: cocoon-boot.sh
# Target path: /etc/initramfs-tools/scripts/cocoon

. /scripts/functions

resolve_disk() {
    local serial="$1" timeout="${COCOON_TIMEOUT:-10}" i=0
    case "$timeout" in ''|*[!0-9]*) timeout=10 ;; esac

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

    # Wait for udev to finish processing all pending events once, before any disk lookups.
    udevadm settle 2>/dev/null || true

    COCOON_INTERNAL="/.cocoon"
    mkdir -p "$COCOON_INTERNAL"

    # Mount read-only EROFS layers
    LOWER=""
    LAYER_DEVS=""
    IFS=,
    for serial in $LAYERS; do
        dev=$(resolve_disk "$serial") || panic "device ${serial} not found"
        mnt="${COCOON_INTERNAL}/layers/${serial}"
        mkdir -p "$mnt"
        mount -t erofs -o ro "$dev" "$mnt" || panic "mount ${serial} failed"
        [ -n "$LOWER" ] && LOWER="${LOWER}:"
        LOWER="${LOWER}${mnt}"
        LAYER_DEVS="${LAYER_DEVS} ${dev}"
    done
    unset IFS

    # Mount COW disk
    cow_dev=$(resolve_disk "$COW") || panic "COW device ${COW} not found"
    mkdir -p "${COCOON_INTERNAL}/cow"
    # [Performance] Added noatime to reduce unnecessary write operations on the COW disk.
    mount -t ext4 -o noatime "$cow_dev" "${COCOON_INTERNAL}/cow" || panic "mount COW failed"
    mkdir -p "${COCOON_INTERNAL}/cow/upper" "${COCOON_INTERNAL}/cow/work"

    # Assemble Overlayfs
    # [Optimized OverlayFS Options]
    # index=on: Prevents broken file handles and ensures inode consistency during copy-up.
    # redirect_dir=on: Enables renaming of directories that exist in the lower (read-only) layers.
    # metacopy=on: Optimizes metadata-only changes (like chmod/chown) to avoid full file copy-up.
    OVL_OPTS="lowerdir=${LOWER},upperdir=${COCOON_INTERNAL}/cow/upper,workdir=${COCOON_INTERNAL}/cow/work,index=on,redirect_dir=on,metacopy=on,xino=on"
    
    mount -t overlay overlay -o "$OVL_OPTS" "${rootmnt}" || panic "overlay failed"

    mkdir -p "${rootmnt}/dev" "${rootmnt}/proc" "${rootmnt}/sys" "${rootmnt}/run"

    # [IO Performance Optimization]
    # EROFS layers are read-only; "none" removes guest-side scheduling overhead entirely
    # since ordering is irrelevant for pure reads and direct=on already handles host scheduling.
    # COW disk gets mq-deadline to prevent write starvation under mixed read/write load.
    for dev in $LAYER_DEVS; do
        blk="${dev##*/}"
        [ -e "/sys/block/${blk}/queue/scheduler" ] && echo "none" > "/sys/block/${blk}/queue/scheduler" 2>/dev/null || true
    done
    cow_blk="${cow_dev##*/}"
    [ -e "/sys/block/${cow_blk}/queue/scheduler" ] && echo "mq-deadline" > "/sys/block/${cow_blk}/queue/scheduler" 2>/dev/null || true

    # Note: The systemd compatibility hacks (clearing fstab, masking fsck) 
    # are handled natively in the Dockerfile. The rootfs is clean here.

    # The only remaining requirement is Machine-ID isolation for cloned VMs.
    rm -f "${rootmnt}/etc/machine-id" 2>/dev/null || true
    : > "${rootmnt}/etc/machine-id"

    # Parse kernel ip= parameters and write systemd-networkd configs.
    # Format: ip=<client-ip>:<server>:<gw-ip>:<netmask>:<hostname>:<device>:<autoconf>
    for x in $(cat /proc/cmdline); do
        case $x in
            ip=*)
                IFS=: read -r cip _ gw mask _ dev _ <<EOF
${x#ip=}
EOF
                if [ -n "$cip" ] && [ -n "$dev" ]; then
                    # Convert dotted netmask to prefix length.
                    prefix=0
                    IFS=. read -r a b c d <<EOF2
$mask
EOF2
                    for octet in $a $b $c $d; do
                        case $octet in
                            255) prefix=$((prefix + 8)) ;;
                            254) prefix=$((prefix + 7)) ;;
                            252) prefix=$((prefix + 6)) ;;
                            248) prefix=$((prefix + 5)) ;;
                            240) prefix=$((prefix + 4)) ;;
                            224) prefix=$((prefix + 3)) ;;
                            192) prefix=$((prefix + 2)) ;;
                            128) prefix=$((prefix + 1)) ;;
                        esac
                    done

                    mkdir -p "${rootmnt}/etc/systemd/network"
                    cat > "${rootmnt}/etc/systemd/network/10-${dev}.network" <<NETEOF
[Match]
Name=${dev}

[Network]
Address=${cip}/${prefix}
Gateway=${gw}
DNS=8.8.8.8
DNS=8.8.4.4
NETEOF
                fi
                ;;
        esac
    done

    log_success_msg "Cocoon: stealth overlay rootfs ready"
}