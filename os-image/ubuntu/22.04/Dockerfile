# Use the latest Ubuntu 22.04 (Jammy) LTS
FROM ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive

# Combined System Setup (No COPY used)
# Secret mounts are read-only, so we copy it to the target path first.
RUN --mount=type=secret,id=cocoon_script \
    cp /run/secrets/cocoon_script /etc/initramfs-tools/scripts/cocoon && \
    chmod 0755 /etc/initramfs-tools/scripts/cocoon && \
    apt-get update && apt-get install -y --no-install-recommends \
        linux-image-virtual \
        initramfs-tools \
        systemd \
        systemd-sysv \
        udev \
        kmod \
        iproute2 \
    && \
    # [Kernel Config]
    printf "erofs\noverlay\next4\nvirtio_blk\nvirtio_pci\nvirtio_ring\nvirtio_net\n" >> /etc/initramfs-tools/modules && \
    sed -i 's/^COMPRESS=.*/COMPRESS=gzip/' /etc/initramfs-tools/initramfs.conf && \
    # [Generate Initramfs] The secret is mounted and used here
    update-initramfs -u -k all && \
    # [Shift-Left System Hacks]
    truncate -s 0 /etc/fstab && \
    systemctl mask systemd-fsck-root.service systemd-remount-fs.service systemd-fsck@.service && \
    systemctl enable systemd-networkd && \
    mkdir -p /etc/systemd/network && \
    printf "[Match]\nName=e* v*\n[Network]\nDHCP=yes\n" > /etc/systemd/network/20-wired.network && \
    # [Final Touches]
    echo 'root:cocoon' | chpasswd && \
    rm -rf /var/lib/apt/lists/*

CMD ["/sbin/init"]