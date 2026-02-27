# Use the latest Ubuntu 24.04 (Noble) LTS
FROM ubuntu:24.04

# Define TARGETARCH to allow BuildKit to pass the architecture
ARG TARGETARCH
ENV DEBIAN_FRONTEND=noninteractive

# Combined System Setup, Optimization, Desktop, and VNC (Single Layer)
RUN --mount=type=secret,id=cocoon_overlay \
    --mount=type=secret,id=cocoon_network \
    apt-get update && apt-get install -y --no-install-recommends \
    # [Base System & Kernel]
    linux-image-virtual \
    initramfs-tools \
    systemd systemd-sysv systemd-timesyncd systemd-resolved \
    udev kmod iproute2 iputils-ping curl wget arping tcpdump \
    software-properties-common \
    # [Desktop Environment, Drivers & Headless Support]
    xfce4 xfce4-terminal xfce4-screenshooter \
    lightdm xserver-xorg x11-xserver-utils dbus-x11 \
    xserver-xorg-video-dummy accountsservice \
    # [VNC Server]
    x11vnc \
    && \
    # [Install Browser: Chrome for AMD64, Chromium for ARM64]
    ARCH=${TARGETARCH:-$(dpkg --print-architecture)}; \
    if [ "$ARCH" = "amd64" ]; then \
        wget "https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb" && \
        apt-get install -y "./google-chrome-stable_current_amd64.deb" && \
        rm "google-chrome-stable_current_amd64.deb" && \
        sed -i 's/exec -a "$0" "$HERE\/chrome" "$@"/exec -a "$0" "$HERE\/chrome" "$@" --no-sandbox --user-data-dir/ ' /opt/google/chrome/google-chrome && \
        mkdir -p /etc/xdg/autostart && \
        cp /usr/share/applications/google-chrome.desktop /etc/xdg/autostart/; \
    else \
        add-apt-repository -y ppa:xtradeb/apps && \
        apt-get update && \
        apt-get install -y chromium && \
        sed -i 's/^Exec=chromium.*/Exec=chromium --no-sandbox --user-data-dir %U/' /usr/share/applications/chromium.desktop && \
        mkdir -p /etc/xdg/autostart && \
        cp /usr/share/applications/chromium.desktop /etc/xdg/autostart/; \
    fi && \
    # [Inject Custom Initramfs Hooks]
    cp /run/secrets/cocoon_overlay /etc/initramfs-tools/scripts/cocoon-overlay && \
    chmod 0755 /etc/initramfs-tools/scripts/cocoon-overlay && \
    cp /run/secrets/cocoon_network /etc/initramfs-tools/scripts/init-bottom/cocoon-network && \
    chmod 0755 /etc/initramfs-tools/scripts/init-bottom/cocoon-network && \
    # [Kernel Setup] Force critical modules (added compression modules for EROFS)
    printf "erofs\nlz4\nlz4_compress\nzstd\nzstd_compress\noverlay\next4\nvirtio_blk\nvirtio_pci\nvirtio_ring\nvirtio_net\nvirtio_gpu\n" >> /etc/initramfs-tools/modules && \
    sed -i 's/^COMPRESS=.*/COMPRESS=gzip/' /etc/initramfs-tools/initramfs.conf && \
    echo 'IP=dhcp' >> /etc/initramfs-tools/initramfs.conf && \
    update-initramfs -u -k all && \
    # [Shift-Left Hacks] Neuter systemd to avoid boot-time filesystem checks
    truncate -s 0 /etc/fstab && \
    systemctl mask systemd-fsck-root.service systemd-remount-fs.service systemd-fsck@.service && \
    # [Networking] Enable networkd/resolved and configure DHCP
    systemctl enable systemd-networkd systemd-resolved systemd-timesyncd && \
    mkdir -p /etc/systemd/network && \
    printf "[Match]\nName=e* v*\n[Network]\nDHCP=yes\n" > /etc/systemd/network/20-wired.network && \
    # [Headless Display Setup] Create a dummy Xorg config (1920x1080) for VNC
    mkdir -p /etc/X11/xorg.conf.d && \
    printf 'Section "Device"\n    Identifier "DummyDevice"\n    Driver "dummy"\n    VideoRam 256000\nEndSection\n\nSection "Monitor"\n    Identifier "DummyMonitor"\n    HorizSync 28.0-80.0\n    VertRefresh 48.0-75.0\nEndSection\n\nSection "Screen"\n    Identifier "DummyScreen"\n    Device "DummyDevice"\n    Monitor "DummyMonitor"\n    DefaultDepth 24\n    SubSection "Display"\n        Depth 24\n        Modes "1920x1080"\n    EndSubSection\nEndSection\n' > /etc/X11/xorg.conf.d/10-dummy.conf && \
    # [Desktop Setup] Configure LightDM to auto-login as root into XFCE
    mkdir -p /etc/lightdm/lightdm.conf.d/ && \
    printf "[Seat:*]\nautologin-user=root\nautologin-session=xfce\nuser-session=xfce\n" > /etc/lightdm/lightdm.conf.d/autologin.conf && \
    # [VNC Setup] Configure password and Systemd service for x11vnc
    mkdir -p /etc/x11vnc && \
    x11vnc -storepasswd cocoon /etc/x11vnc/vncpwd && \
    printf "[Unit]\nDescription=x11vnc VNC Server for X11\nRequires=lightdm.service\nAfter=lightdm.service network.target\n\n[Service]\nExecStart=/usr/bin/x11vnc -display :0 -auth guess -forever -loop -noxdamage -repeat -rfbauth /etc/x11vnc/vncpwd -rfbport 5900 -shared\nRestart=always\nRestartSec=2\n\n[Install]\nWantedBy=graphical.target\n" > /etc/systemd/system/x11vnc.service && \
    systemctl enable x11vnc.service && \
    # [Access] Set root password
    echo 'root:cocoon' | chpasswd && \
    # [Cleanup] Purge APT cache but KEEP the partial directories to prevent OverlayFS EIO errors
    rm -rf /var/lib/apt/lists/* && \
    mkdir -p /var/lib/apt/lists/partial /var/cache/apt/archives/partial

# Final configuration: Standard init for VM boot
CMD ["/sbin/init"]