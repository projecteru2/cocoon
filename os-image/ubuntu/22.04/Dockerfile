# Use the latest Ubuntu 22.04 (Jammy) LTS
FROM ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive

# 1. Install core components
# linux-image-virtual: The standard minimal kernel for VM guests in Ubuntu.
# initramfs-tools: Native initrd generation tool for Debian/Ubuntu.
# systemd / systemd-sysv / udev: Ensure proper device management and lifecycle.
# systemd-resolved: Required for DNS resolution in modern Ubuntu.
RUN apt-get update && apt-get install -y --no-install-recommends \
    linux-image-virtual \
    initramfs-tools \
    systemd \
    systemd-sysv \
    udev \
    kmod \
    iproute2 \
    && rm -rf /var/lib/apt/lists/*

# 2. Inject the Cocoon hook script
# Placing it in /etc/initramfs-tools/scripts/ ensures it gets packed 
# into the /scripts/ directory of the generated initrd.
COPY cocoon-boot.sh /etc/initramfs-tools/scripts/cocoon
RUN chmod 0755 /etc/initramfs-tools/scripts/cocoon

# 3. Force critical storage and network modules into the initramfs
# Ensures these drivers are available during early boot.
# Using printf ensures newlines are processed correctly across all shells.
RUN printf "erofs\noverlay\next4\nvirtio_blk\nvirtio_pci\nvirtio_ring\nvirtio_net\n" >> /etc/initramfs-tools/modules

# 4. Regenerate initramfs (using gzip for maximum compatibility)
RUN sed -i 's/^COMPRESS=.*/COMPRESS=gzip/' /etc/initramfs-tools/initramfs.conf && \
    update-initramfs -u -k all

# 5. [Shift-Left System Hacks]: Neuter systemd directly in the base image
# Eliminate fstab and fsck conflicts at build time, as defined in RFC 0061.
RUN truncate -s 0 /etc/fstab && \
    systemctl mask systemd-fsck-root.service systemd-remount-fs.service systemd-fsck@.service

# 6. VM Network and Console adaptation
# Enable systemd-networkd by default and configure DHCP for eth0 (virtio-net).
RUN systemctl enable systemd-networkd systemd-resolved && \
    mkdir -p /etc/systemd/network && \
    printf "[Match]\nName=e* v*\n[Network]\nDHCP=yes\n" > /etc/systemd/network/20-wired.network

# 7. Set root password to 'cocoon' (for console login/debugging only)
RUN echo 'root:cocoon' | chpasswd

# 8. Set default systemd boot target
CMD ["/sbin/init"]
