#!/usr/bin/env bash
set -Eeuo pipefail

if (( $# == 0 )); then
    echo "codex-safe-entrypoint: probe command is required" >&2
    exit 2
fi

readonly dockerd_log=/tmp/codex-safe-dockerd.log
dockerd_pid=""

cleanup() {
    local status=$?
    trap - EXIT INT TERM

    if [[ -n "${dockerd_pid}" ]] && kill -0 "${dockerd_pid}" 2>/dev/null; then
        kill -TERM "${dockerd_pid}" 2>/dev/null || true
        wait "${dockerd_pid}" 2>/dev/null || true
    fi

    exit "${status}"
}

exit_on_signal() {
    exit "$1"
}

trap cleanup EXIT
trap 'exit_on_signal 130' INT
trap 'exit_on_signal 143' TERM

mkdir -p /run/docker /var/lib/docker
# The nested runtime must preserve Sysbox bind mounts at their exact paths,
# including targets with spaces. The image pins crun for that compatibility.
dockerd \
    --add-runtime=crun=/usr/local/bin/crun \
    --data-root=/var/lib/docker \
    --default-runtime=crun \
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

readonly host_uid="${CODEX_SAFE_HOST_UID:-}"
readonly host_gid="${CODEX_SAFE_HOST_GID:-}"
if [[ ! "${host_uid}" =~ ^[0-9]+$ ]] || [[ ! "${host_gid}" =~ ^[0-9]+$ ]]; then
    echo "codex-safe-entrypoint: CODEX_SAFE_HOST_UID and CODEX_SAFE_HOST_GID must be numeric" >&2
    exit 2
fi

readonly probe_home=/tmp/codex-safe-home
mkdir -p "${probe_home}"
chown "${host_uid}:${host_gid}" "${probe_home}" /var/run/docker.sock
chmod 0700 "${probe_home}"
chmod 0600 /var/run/docker.sock

exec setpriv \
    --reuid="${host_uid}" \
    --regid="${host_gid}" \
    --clear-groups \
    -- \
    env HOME="${probe_home}" \
    "$@"
