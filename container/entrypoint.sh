#!/usr/bin/env bash
set -Eeuo pipefail

if (( $# == 0 )); then
    echo "codex-safe-entrypoint: probe command is required" >&2
    exit 2
fi

readonly dockerd_log=/tmp/codex-safe-dockerd.log
dockerd_pid=""
probe_pid=""

cleanup() {
    local status=$?
    trap - EXIT INT TERM

    if [[ -n "${probe_pid}" ]] && kill -0 "${probe_pid}" 2>/dev/null; then
        kill -TERM "${probe_pid}" 2>/dev/null || true
        wait "${probe_pid}" 2>/dev/null || true
    fi
    if [[ -n "${dockerd_pid}" ]] && kill -0 "${dockerd_pid}" 2>/dev/null; then
        kill -TERM "${dockerd_pid}" 2>/dev/null || true
        wait "${dockerd_pid}" 2>/dev/null || true
    fi

    exit "${status}"
}

forward_signal() {
    local signal=$1
    local status=$2

    if [[ -n "${probe_pid}" ]] && kill -0 "${probe_pid}" 2>/dev/null; then
        kill -"${signal}" "${probe_pid}" 2>/dev/null || true
    fi
    exit "${status}"
}

trap cleanup EXIT
trap 'forward_signal INT 130' INT
trap 'forward_signal TERM 143' TERM

mkdir -p /run/docker /var/lib/docker
dockerd \
    --data-root=/var/lib/docker \
    --host=unix:///var/run/docker.sock \
    >"${dockerd_log}" 2>&1 &
dockerd_pid=$!

readonly ready_timeout="${CODEX_SAFE_DOCKER_READY_TIMEOUT:-60}"
if [[ ! "${ready_timeout}" =~ ^[1-9][0-9]*$ ]]; then
    echo "codex-safe-entrypoint: CODEX_SAFE_DOCKER_READY_TIMEOUT must be a positive integer" >&2
    exit 2
fi

ready=false
for (( attempt = 1; attempt <= ready_timeout; attempt++ )); do
    if docker info >/dev/null 2>&1; then
        ready=true
        break
    fi
    if ! kill -0 "${dockerd_pid}" 2>/dev/null; then
        echo "codex-safe-entrypoint: dockerd exited before becoming ready" >&2
        cat "${dockerd_log}" >&2
        exit 1
    fi
    sleep 1
done

if [[ "${ready}" != true ]]; then
    echo "codex-safe-entrypoint: dockerd did not become ready within ${ready_timeout}s" >&2
    cat "${dockerd_log}" >&2
    exit 1
fi

"$@" &
probe_pid=$!

set +e
wait "${probe_pid}"
probe_status=$?
set -e
probe_pid=""
exit "${probe_status}"
