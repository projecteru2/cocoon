# cocoon OCI image: Ubuntu 24.04 + Docker + redroid (Android-in-container).
#
# Architecture: Host → cocoon VM (this image) → Docker → redroid container
# Remote access: scrcpy/adb connects to <vm-ip>:5555
#
# Key difference from base 24.04: uses linux-image-generic (not virtual)
# because redroid requires binder_linux and ashmem_linux kernel modules,
# which are only available in linux-modules-extra-*-generic.
#
# Notes:
# - Docker uses vfs storage driver (VM root is overlayfs, can't nest overlay2)
# - Docker runs with --iptables=false (no bridge NAT, redroid uses --network=host)
# - First boot pulls the redroid Docker image (~1-2 GB), requires internet
# - Subsequent boots reuse the cached image from the COW disk

FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

RUN --mount=type=secret,id=cocoon_overlay \
    --mount=type=secret,id=cocoon_network \
    apt-get update && apt-get install -y --no-install-recommends \
    linux-image-generic \
    initramfs-tools \
    systemd systemd-sysv systemd-timesyncd systemd-resolved \
    udev kmod iproute2 iputils-ping curl wget ca-certificates net-tools \
    htop ncdu nload tree \
    docker.io \
    && \
    # Install extra modules for binder/ashmem — needs KVER from installed kernel.
    apt-get install -y --no-install-recommends \
        linux-modules-extra-$(ls /lib/modules/ | head -1) \
    && \
    # --- cocoon initramfs hooks ---
    cp /run/secrets/cocoon_overlay /etc/initramfs-tools/scripts/cocoon-overlay && \
    chmod 0755 /etc/initramfs-tools/scripts/cocoon-overlay && \
    cp /run/secrets/cocoon_network /etc/initramfs-tools/scripts/init-bottom/cocoon-network && \
    chmod 0755 /etc/initramfs-tools/scripts/init-bottom/cocoon-network && \
    printf "erofs\noverlay\next4\nvirtio_blk\nvirtio_pci\nvirtio_ring\nvirtio_net\n" >> /etc/initramfs-tools/modules && \
    sed -i 's/^COMPRESS=.*/COMPRESS=gzip/' /etc/initramfs-tools/initramfs.conf && \
    echo 'IP=dhcp' >> /etc/initramfs-tools/initramfs.conf && \
    # Regenerate initramfs with cocoon hooks + extra modules.
    update-initramfs -u -k all && \
    # Verify boot files exist.
    ls /boot/vmlinuz-* /boot/initrd.img-* && \
    # --- systemd ---
    truncate -s 0 /etc/fstab && \
    systemctl mask systemd-fsck-root.service systemd-remount-fs.service systemd-fsck@.service && \
    systemctl enable systemd-networkd systemd-resolved systemd-timesyncd docker && \
    mkdir -p /etc/systemd/network && \
    printf "[Match]\nName=e* v*\n[Network]\nDHCP=yes\n" > /etc/systemd/network/20-wired.network && \
    # --- Docker daemon config ---
    # vfs: VM root is overlayfs, overlay2 can't nest as upperdir
    # iptables=false: no bridge NAT rules, redroid uses --network=host
    mkdir -p /etc/docker && \
    printf '{"storage-driver":"vfs","iptables":false}\n' > /etc/docker/daemon.json && \
    # --- redroid kernel module loading ---
    # binder_linux + ashmem_linux loaded at boot via systemd-modules-load
    printf "binder_linux\nashmem_linux\n" > /etc/modules-load.d/redroid.conf && \
    echo 'options binder_linux devices="binder,hwbinder,vndbinder"' > /etc/modprobe.d/redroid.conf && \
    # --- redroid systemd service ---
    # --network=host: container shares VM network, port 5555 directly on VM IP
    # --privileged + --cgroupns=host: required for Android init
    printf '[Unit]\nDescription=Redroid Android Container\nAfter=docker.service systemd-modules-load.service\nRequires=docker.service\n\n[Service]\nType=simple\nExecStartPre=-/usr/bin/docker rm -f redroid\nExecStart=/usr/bin/docker run --name redroid --rm --privileged --cgroupns=host --network=host -v /data/redroid:/data redroid/redroid:14.0.0-latest androidboot.redroid_gpu_mode=guest\nExecStop=/usr/bin/docker stop -t 10 redroid\nRestart=on-failure\nRestartSec=10\n\n[Install]\nWantedBy=multi-user.target\n' > /etc/systemd/system/redroid.service && \
    systemctl enable redroid && \
    # --- access ---
    echo 'root:cocoon' | chpasswd && \
    rm -rf /var/lib/apt/lists/*

CMD ["/sbin/init"]
