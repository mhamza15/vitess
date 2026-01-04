#!/bin/bash

# Minimal MySQL install script for vitesst test images.
# Skips mysql-shell and percona-xtrabackup to reduce image size.

set -euo pipefail

FLAVOR="$1"

export DEBIAN_FRONTEND=noninteractive

KEYSERVERS=(
    keyserver.ubuntu.com
    hkp://keyserver.ubuntu.com:80
)

add_apt_key() {
    for i in {1..3}; do
        for keyserver in "${KEYSERVERS[@]}"; do
            if apt-key adv --no-tty --keyserver "${keyserver}" --recv-keys "$1"; then return; fi
        done
    done
}

MAX_RETRY=20

do_fetch() {
    wget \
        --tries=$MAX_RETRY \
        --read-timeout=30 \
        --timeout=30 \
        --retry-connrefused \
        --waitretry=1 \
        --no-dns-cache \
        $1 -O $2
}

# Install base packages
BASE_PACKAGES=(
    ca-certificates
    dirmngr
    gnupg
    libaio1
    libatomic1
    procps
    wget
)

apt-get update
apt-get install -y --no-install-recommends "${BASE_PACKAGES[@]}"

# Download and install MySQL packages
case "${FLAVOR}" in
mysql80)
    VERSION=8.0.43
    do_fetch https://repo.mysql.com/apt/debian/pool/mysql-8.0/m/mysql-community/mysql-common_${VERSION}-1debian12_amd64.deb /tmp/mysql-common.deb
    do_fetch https://repo.mysql.com/apt/debian/pool/mysql-8.0/m/mysql-community/mysql-community-client-plugins_${VERSION}-1debian12_amd64.deb /tmp/mysql-community-client-plugins.deb
    do_fetch https://repo.mysql.com/apt/debian/pool/mysql-8.0/m/mysql-community/mysql-community-client-core_${VERSION}-1debian12_amd64.deb /tmp/mysql-community-client-core.deb
    do_fetch https://repo.mysql.com/apt/debian/pool/mysql-8.0/m/mysql-community/mysql-community-client_${VERSION}-1debian12_amd64.deb /tmp/mysql-community-client.deb
    do_fetch https://repo.mysql.com/apt/debian/pool/mysql-8.0/m/mysql-community/mysql-client_${VERSION}-1debian12_amd64.deb /tmp/mysql-client.deb
    do_fetch https://repo.mysql.com/apt/debian/pool/mysql-8.0/m/mysql-community/mysql-community-server-core_${VERSION}-1debian12_amd64.deb /tmp/mysql-community-server-core.deb
    do_fetch https://repo.mysql.com/apt/debian/pool/mysql-8.0/m/mysql-community/mysql-community-server_${VERSION}-1debian12_amd64.deb /tmp/mysql-community-server.deb
    do_fetch https://repo.mysql.com/apt/debian/pool/mysql-8.0/m/mysql-community/mysql-server_${VERSION}-1debian12_amd64.deb /tmp/mysql-server.deb
    do_fetch https://repo.mysql.com/apt/debian/pool/mysql-8.0/m/mysql-community/libmysqlclient21_${VERSION}-1debian12_amd64.deb /tmp/libmysqlclient21.deb
    ;;
mysql84)
    VERSION=8.4.6
    do_fetch https://repo.mysql.com/apt/debian/pool/mysql-8.4-lts/m/mysql-community/mysql-common_${VERSION}-1debian12_amd64.deb /tmp/mysql-common.deb
    do_fetch https://repo.mysql.com/apt/debian/pool/mysql-8.4-lts/m/mysql-community/mysql-community-client-plugins_${VERSION}-1debian12_amd64.deb /tmp/mysql-community-client-plugins.deb
    do_fetch https://repo.mysql.com/apt/debian/pool/mysql-8.4-lts/m/mysql-community/mysql-community-client-core_${VERSION}-1debian12_amd64.deb /tmp/mysql-community-client-core.deb
    do_fetch https://repo.mysql.com/apt/debian/pool/mysql-8.4-lts/m/mysql-community/mysql-community-client_${VERSION}-1debian12_amd64.deb /tmp/mysql-community-client.deb
    do_fetch https://repo.mysql.com/apt/debian/pool/mysql-8.4-lts/m/mysql-community/mysql-client_${VERSION}-1debian12_amd64.deb /tmp/mysql-client.deb
    do_fetch https://repo.mysql.com/apt/debian/pool/mysql-8.4-lts/m/mysql-community/mysql-community-server-core_${VERSION}-1debian12_amd64.deb /tmp/mysql-community-server-core.deb
    do_fetch https://repo.mysql.com/apt/debian/pool/mysql-8.4-lts/m/mysql-community/mysql-community-server_${VERSION}-1debian12_amd64.deb /tmp/mysql-community-server.deb
    do_fetch https://repo.mysql.com/apt/debian/pool/mysql-8.4-lts/m/mysql-community/mysql-server_${VERSION}-1debian12_amd64.deb /tmp/mysql-server.deb
    do_fetch https://repo.mysql.com/apt/debian/pool/mysql-8.4-lts/m/mysql-community/libmysqlclient24_${VERSION}-1debian12_amd64.deb /tmp/libmysqlclient.deb
    ;;
*)
    echo "Unknown flavor: ${FLAVOR}"
    exit 1
    ;;
esac

# Install downloaded packages
dpkg -i /tmp/*.deb || apt-get install -f -y

# Clean up
rm -rf /var/lib/apt/lists/* /tmp/*.deb /var/lib/mysql/
