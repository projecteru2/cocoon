#!/bin/sh
# Filename: network.sh
# Target path: /etc/initramfs-tools/scripts/init-bottom/cocoon-network
#
# Runs in init-bottom phase — AFTER configure_networking has parsed kernel ip=
# parameters into /run/net-*.conf, and AFTER mountroot has assembled the overlay.
# Converts initramfs network config into systemd-networkd .network files so
# the IP configuration persists after switch_root, and writes /etc/resolv.conf
# for immediate DNS availability regardless of init system.

PREREQ=""
prereqs() { echo "$PREREQ"; }
case "$1" in prereqs) prereqs; exit 0 ;; esac

. /scripts/functions

# $rootmnt is set by initramfs — points to the mounted root filesystem.
[ -z "$rootmnt" ] && exit 0

_dns_servers=""

for conf_file in /run/net-*.conf; do
    [ -f "$conf_file" ] || continue

    unset DEVICE IPV4ADDR IPV4NETMASK IPV4GATEWAY IPV4DNS0 IPV4DNS1 HOSTNAME
    . "$conf_file"
    [ -z "$DEVICE" ] || [ -z "$IPV4ADDR" ] && continue

    # Convert dotted netmask to prefix length.
    prefix=0
    IFS=. read -r a b c d <<EOF
${IPV4NETMASK}
EOF
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
    {
        printf "[Match]\nName=%s\n\n[Network]\nAddress=%s/%d\n" "$DEVICE" "$IPV4ADDR" "$prefix"
        [ -n "$IPV4GATEWAY" ] && [ "$IPV4GATEWAY" != "0.0.0.0" ] && printf "Gateway=%s\n" "$IPV4GATEWAY"
        [ -n "$IPV4DNS0" ] && [ "$IPV4DNS0" != "0.0.0.0" ] && printf "DNS=%s\n" "$IPV4DNS0"
        [ -n "$IPV4DNS1" ] && [ "$IPV4DNS1" != "0.0.0.0" ] && printf "DNS=%s\n" "$IPV4DNS1"
        # Fallback DNS if none provided.
        [ -z "$IPV4DNS0" ] || [ "$IPV4DNS0" = "0.0.0.0" ] && printf "DNS=8.8.8.8\nDNS=8.8.4.4\n"
    } > "${rootmnt}/etc/systemd/network/10-${DEVICE}.network"

    # Collect DNS servers for resolv.conf.
    [ -n "$IPV4DNS0" ] && [ "$IPV4DNS0" != "0.0.0.0" ] && _dns_servers="${_dns_servers} ${IPV4DNS0}"
    [ -n "$IPV4DNS1" ] && [ "$IPV4DNS1" != "0.0.0.0" ] && _dns_servers="${_dns_servers} ${IPV4DNS1}"

    # Set hostname from the first interface that has one.
    if [ -n "$HOSTNAME" ] && [ ! -f "${rootmnt}/etc/cocoon-hostname-set" ]; then
        echo "$HOSTNAME" > "${rootmnt}/etc/hostname"
        : > "${rootmnt}/etc/cocoon-hostname-set"
    fi
done

# Write /etc/resolv.conf from DNS servers collected above.
[ -z "$_dns_servers" ] && _dns_servers="8.8.8.8 8.8.4.4"
: > "${rootmnt}/etc/resolv.conf"
for _ns in $_dns_servers; do
    printf "nameserver %s\n" "$_ns" >> "${rootmnt}/etc/resolv.conf"
done
