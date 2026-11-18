#!/usr/bin/env bash
# Publish site/ (and optionally a console build) with bounded retention:
# only the newest N site snapshots, site releases and console releases are kept.
set -euo pipefail

if [[ $# -lt 1 || $# -gt 3 || $(id -u) != 0 ]]; then
    printf 'Usage: sudo bash publish-site.sh <repo-dir> [console.tgz] [keep]\n' >&2
    exit 2
fi
repo=$1
console_tgz=${2:-}
keep=${3:-3}
if [[ ! $keep =~ ^[1-9][0-9]*$ ]]; then
    printf 'keep must be a positive integer\n' >&2
    exit 2
fi
if [[ ! -f $repo/site/index.html || ! -f $repo/site/favicon.svg ]]; then
    printf 'repo-dir must contain site/index.html and site/favicon.svg\n' >&2
    exit 2
fi

webroot=/var/www/agentrib
site_releases=/var/www/agentrib-site-releases
console_releases=/var/www/agentrib-console-releases
backups=/var/backups/agentrib
ts=$(date +%Y%m%d-%H%M%S)

install -d -m 0755 "$site_releases" "$console_releases"
install -d -m 0700 "$backups"

members=()
for name in index.html favicon.svg engines projects; do
    if [[ -e $webroot/$name ]]; then
        members+=("$name")
    fi
done
if [[ ${#members[@]} -gt 0 ]]; then
    tar -czhf "$backups/site-$ts.tar.gz" -C "$webroot" "${members[@]}"
fi
active=''
if [[ -L $webroot/console ]]; then
    active=$(readlink "$webroot/console")
    printf '%s\n' "$active" > "$backups/console-$ts.target"
fi

release=$site_releases/site-$ts
install -d -m 0755 "$release"
for name in engines projects; do
    if [[ ! -d $repo/site/$name ]]; then
        continue
    fi
    cp -a "$repo/site/$name" "$release/$name"
    chmod -R a+rX "$release/$name"
    if [[ -e $webroot/$name && ! -L $webroot/$name ]]; then
        rm -rf -- "$webroot/$name"
    fi
    ln -s "$release/$name" "$webroot/.next-$name"
    mv -Tf "$webroot/.next-$name" "$webroot/$name"
done

# Normalize CRLF checkouts so publishes are identical from any platform.
tr -d '\r' < "$repo/site/index.html" > "$webroot/.next-index.html"
tr -d '\r' < "$repo/site/favicon.svg" > "$webroot/.next-icon.svg"
chmod 0644 "$webroot/.next-index.html" "$webroot/.next-icon.svg"
mv -Tf "$webroot/.next-index.html" "$webroot/index.html"
mv -Tf "$webroot/.next-icon.svg" "$webroot/favicon.svg"

if [[ -n $console_tgz ]]; then
    if [[ ! -f $console_tgz ]]; then
        printf 'console archive not found: %s\n' "$console_tgz" >&2
        exit 2
    fi
    console=$console_releases/console-$ts
    install -d -m 0755 "$console"
    tar -xzf "$console_tgz" -C "$console" --no-same-owner --no-same-permissions
    if [[ ! -s $console/index.html || ! -d $console/_next/static ]]; then
        printf 'console archive lacks index.html or _next/static\n' >&2
        exit 2
    fi
    chmod -R a+rX "$console"
    ln -s "$console" "$webroot/.next-console"
    mv -Tf "$webroot/.next-console" "$webroot/console"
    active=$console
fi

prune() {
    local pattern=$1 protected=$2 old
    ls -1dt $pattern 2>/dev/null | tail -n +"$((keep + 1))" | while read -r old; do
        if [[ $old == "$protected" ]]; then
            continue
        fi
        rm -rf -- "$old"
    done
}
prune "$site_releases/site-*" "$release"
prune "$console_releases/*" "$active"
ls -1t "$backups"/site-*.tar.gz 2>/dev/null | tail -n +"$((keep + 1))" | xargs -r rm -f --
ls -1t "$backups"/console-*.target 2>/dev/null | tail -n +"$((keep + 1))" | xargs -r rm -f --

for path in /healthz /; do
    code=$(curl -sS -o /dev/null -w '%{http_code}' --resolve www.agentrib.com:443:127.0.0.1 "https://www.agentrib.com$path")
    if [[ $code != 200 ]]; then
        printf 'verification failed: %s returned %s\n' "$path" "$code" >&2
        exit 1
    fi
done
if [[ -n $active ]]; then
    code=$(curl -sS -o /dev/null -w '%{http_code}' --resolve www.agentrib.com:443:127.0.0.1 "https://www.agentrib.com/console/")
    if [[ $code != 200 ]]; then
        printf 'verification failed: /console/ returned %s\n' "$code" >&2
        exit 1
    fi
fi
printf 'Published site-%s; console=%s; retention=%s\n' "$ts" "$(basename "$active")" "$keep"