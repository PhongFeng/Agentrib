#!/usr/bin/env bash
# Remove disposable scratch directories older than N days from a workspace.
# Only directories are touched; protected names and paths referenced by the
# Runner config (runner.json) are always kept.
set -euo pipefail

if [[ $# -lt 1 ]]; then
    printf 'Usage: clean-scratch.sh <dir> [days] [protected-name...]\n' >&2
    exit 2
fi
dir=$1
days=${2:-7}
protected=${*:3}
if [[ ! $days =~ ^[0-9]+$ ]]; then
    printf 'days must be a non-negative integer\n' >&2
    exit 2
fi
if [[ ! -d $dir ]]; then
    exit 0
fi

find "$dir" -mindepth 1 -maxdepth 1 -type d -mtime +"$days" -print0 |
while IFS= read -r -d '' path; do
    name=${path##*/}
    if [[ " $protected " == *" $name "* ]]; then
        continue
    fi
    if [[ -f $dir/runner.json ]] && grep -qF "$name" "$dir/runner.json"; then
        continue
    fi
    rm -rf -- "$path"
    printf 'removed %s\n' "$path"
done