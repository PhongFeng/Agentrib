#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 1 || $(id -u) != 0 ]]; then
    printf 'Usage: sudo bash install-linux.sh /path/to/agentrib-server\n' >&2
    exit 2
fi

binary=$1
deploy_dir=$(cd -- "$(dirname -- "$0")" && pwd)
install -d -m 0755 /opt/agentrib/bin
install -d -m 0700 /etc/agentrib

if ! id agentrib >/dev/null 2>&1; then
    adduser --system --group --no-create-home agentrib
fi
if ! runuser -u postgres -- psql -Atqc "SELECT 1 FROM pg_roles WHERE rolname='agentrib'" | grep -qx 1; then
    runuser -u postgres -- createuser agentrib
fi
if ! runuser -u postgres -- psql -Atqc "SELECT 1 FROM pg_database WHERE datname='agentrib'" | grep -qx 1; then
    runuser -u postgres -- createdb -O agentrib agentrib
fi

if [[ ! -f /etc/agentrib/server.env ]]; then
    umask 077
    {
        printf '%s\n' 'AGENTRIB_DATABASE_URL=postgresql:///agentrib?host=/var/run/postgresql&user=agentrib'
        printf 'AGENTRIB_OPERATOR_TOKEN=%s\n' "$(openssl rand -hex 32)"
    } > /etc/agentrib/server.env
fi

install -m 0755 "$binary" /opt/agentrib/bin/agentrib-server.new
mv -f /opt/agentrib/bin/agentrib-server.new /opt/agentrib/bin/agentrib-server
install -m 0644 "$deploy_dir/agentrib.service" /etc/systemd/system/agentrib.service
install -D -m 0644 "$deploy_dir/nginx.conf" /etc/nginx/snippets/agentrib.conf
install -D -m 0644 "$deploy_dir/nginx-play.conf" /etc/nginx/snippets/agentrib-play.conf
systemctl daemon-reload
systemctl enable agentrib.service
systemctl restart agentrib.service
