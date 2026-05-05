#!/bin/sh
# Install cocoon-agent (vsock exec daemon, replaces SSH for control plane)
# and enable a systemd unit so the VM auto-starts the agent on boot.
set -eu

VERSION="${COCOON_AGENT_VERSION:-0.1.0}"

case "$(dpkg --print-architecture)" in
    amd64) ARCH="x86_64" ;;
    arm64) ARCH="arm64" ;;
    *) echo "cocoon-agent: unsupported arch $(dpkg --print-architecture)" >&2; exit 1 ;;
esac

URL="https://github.com/cocoonstack/cocoon-agent/releases/download/v${VERSION}/cocoon-agent_${VERSION}_Linux_${ARCH}.tar.gz"
curl -fsSL "$URL" | tar xz -C /usr/local/bin/ cocoon-agent
chmod 0755 /usr/local/bin/cocoon-agent

cat > /etc/systemd/system/cocoon-agent.service <<'EOF'
[Unit]
Description=Cocoon agent (vsock command exec)
Documentation=https://github.com/cocoonstack/cocoon-agent

[Service]
Type=simple
User=root
Group=root
# vsock transport modules are loaded by the kernel when the host attaches
# a virtio-vsock device; the modprobe is best-effort for kernels that
# build them as on-demand modules.
ExecStartPre=-/sbin/modprobe vhost_vsock
ExecStart=/usr/local/bin/cocoon-agent serve
Environment=AGENT_LOG_LEVEL=info
Restart=always
RestartSec=2s
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF

systemctl enable cocoon-agent.service
