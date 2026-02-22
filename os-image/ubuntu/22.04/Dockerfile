# Use the latest Ubuntu 22.04 (Jammy) LTS
FROM ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive

# 1. Inject the Cocoon hook script FIRST
# This allows us to integrate the script into the initramfs in a single RUN layer.
COPY cocoon-boot.sh /etc/initramfs-tools/scripts/cocoon

# 2. Combined System Setup
# This giant RUN command:
#   - Installs all necessary packages
#   - Configures initramfs modules and compression
#   - Regenerates initramfs
#   - Performs [Shift-Left] system neutering (masking services, clearing fstab)
#   - Configures networking and passwords
RUN chmod 0755 /etc/initramfs-tools/scripts/cocoon && \
    apt-get update && apt-get install -y --no-install-recommends \
        linux-image-virtual \
        initramfs-tools \
        systemd \
        systemd-sysv \
        udev \
        kmod \
        iproute2 \
    && \
    # Force critical storage and network modules into the initramfs
    printf "erofs\noverlay\next4\nvirtio_blk\nvirtio_pci\nvirtio_ring\nvirtio_net\n" >> /etc/initramfs-tools/modules && \
    # Set gzip compression for initrd
    sed -i 's/^COMPRESS=.*/COMPRESS=gzip/' /etc/initramfs-tools/initramfs.conf && \
    # Regenerate initramfs
    update-initramfs -u -k all && \
    # [Shift-Left System Hacks]: Neuter systemd and clean up
    truncate -s 0 /etc/fstab && \
    systemctl mask systemd-fsck-root.service systemd-remount-fs.service systemd-fsck@.service && \
    systemctl enable systemd-networkd && \
    mkdir -p /etc/systemd/network && \
    printf "[Match]\nName=e* v*\n[Network]\nDHCP=yes\n" > /etc/systemd/network/20-wired.network && \
    # Set root password
    echo 'root:cocoon' | chpasswd && \
    # Cleanup APT cache to save space
    rm -rf /var/lib/apt/lists/*

# 3. Final configuration
# Since it's a VM rootfs, the CMD is the entry point for the guest OS init.
CMD ["/sbin/init"]