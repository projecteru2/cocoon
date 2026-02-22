# Use the latest Ubuntu 24.04 (Noble) LTS
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

# Combined System Setup & Optimization (Single Layer)
# Secret mounts are read-only, so we copy it to the target path first.
RUN --mount=type=secret,id=cocoon_script \
    cp /run/secrets/cocoon_script /etc/initramfs-tools/scripts/cocoon && \
    chmod 0755 /etc/initramfs-tools/scripts/cocoon && \
    apt-get update && apt-get install -y --no-install-recommends \
        linux-image-virtual \
        initramfs-tools \
        systemd \
        systemd-sysv \
        systemd-resolved \
        udev \
        kmod \
        iproute2 \
    && \
    # [Kernel Setup] Force critical modules and set gzip compression
    printf "erofs\noverlay\next4\nvirtio_blk\nvirtio_pci\nvirtio_ring\nvirtio_net\n" >> /etc/initramfs-tools/modules && \
    sed -i 's/^COMPRESS=.*/COMPRESS=gzip/' /etc/initramfs-tools/initramfs.conf && \
    # Regenerate initramfs inside this layer (consumes the secret script)
    update-initramfs -u -k all && \
    # [Shift-Left Hacks] Neuter systemd to avoid boot-time filesystem checks
    truncate -s 0 /etc/fstab && \
    systemctl mask systemd-fsck-root.service systemd-remount-fs.service systemd-fsck@.service && \
    # [Networking] Enable networkd/resolved and configure DHCP
    systemctl enable systemd-networkd systemd-resolved && \
    mkdir -p /etc/systemd/network && \
    printf "[Match]\nName=e* v*\n[Network]\nDHCP=yes\n" > /etc/systemd/network/20-wired.network && \
    # [Access] Set root password
    echo 'root:cocoon' | chpasswd && \
    # [Cleanup] Purge APT cache to minimize EROFS size
    rm -rf /var/lib/apt/lists/*

# Final configuration
CMD ["/sbin/init"]