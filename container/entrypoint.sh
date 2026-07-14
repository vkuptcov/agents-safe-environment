#!/usr/bin/env bash
set -Eeuo pipefail

if (( $# == 0 )); then
    echo "codex-safe-entrypoint: probe command is required" >&2
    exit 2
fi

readonly host_uid="${CODEX_SAFE_HOST_UID:-}"
readonly host_gid="${CODEX_SAFE_HOST_GID:-}"
readonly host_user="${CODEX_SAFE_HOST_USER:-}"
readonly host_group="${CODEX_SAFE_HOST_GROUP:-}"
readonly host_home="${CODEX_SAFE_HOST_HOME:-}"
readonly account_name_pattern='^[a-z_][a-z0-9_-]*[$]?$'

if [[ ! "${host_uid}" =~ ^[0-9]+$ ]] || [[ ! "${host_gid}" =~ ^[0-9]+$ ]]; then
    echo "codex-safe-entrypoint: CODEX_SAFE_HOST_UID and CODEX_SAFE_HOST_GID must be numeric" >&2
    exit 2
fi
if [[ ! "${host_user}" =~ ${account_name_pattern} ]]; then
    echo "codex-safe-entrypoint: CODEX_SAFE_HOST_USER is not a supported account name" >&2
    exit 2
fi
if [[ ! "${host_group}" =~ ${account_name_pattern} ]]; then
    echo "codex-safe-entrypoint: CODEX_SAFE_HOST_GROUP is not a supported group name" >&2
    exit 2
fi
if [[ "${host_home}" != /* ]] ||
    [[ "${host_home}" == "/" ]] ||
    [[ "${host_home}" == *,* ]] ||
    [[ "${host_home}" == *$'\n'* ]] ||
    [[ "${host_home}" == *$'\r'* ]] ||
    [[ "${host_home}" == */ ]] ||
    [[ "${host_home}" == *//* ]] ||
    [[ "${host_home}" == */./* ]] ||
    [[ "${host_home}" == */../* ]] ||
    [[ "${host_home}" == */. ]] ||
    [[ "${host_home}" == */.. ]]; then
    echo "codex-safe-entrypoint: CODEX_SAFE_HOST_HOME must be a canonical absolute directory" >&2
    exit 2
fi

configure_host_group() {
    local id_entry
    local id_name
    local name_entry
    local name_gid

    id_entry="$(getent group "${host_gid}" || true)"
    name_entry="$(getent group "${host_group}" || true)"
    if [[ -n "${name_entry}" ]]; then
        IFS=: read -r _ _ name_gid _ <<<"${name_entry}"
        if [[ "${name_gid}" != "${host_gid}" ]]; then
            echo "codex-safe-entrypoint: group ${host_group@Q} already uses GID ${name_gid}" >&2
            return 1
        fi
    fi

    if [[ -z "${id_entry}" ]]; then
        groupadd --gid "${host_gid}" "${host_group}"
        return
    fi

    id_name="${id_entry%%:*}"
    if [[ "${id_name}" == "${host_group}" ]]; then
        return
    fi
    if [[ -n "${name_entry}" ]]; then
        echo "codex-safe-entrypoint: GID ${host_gid} has conflicting group names" >&2
        return 1
    fi
    groupmod --new-name "${host_group}" "${id_name}"
}

configure_host_user() {
    local id_entry
    local id_name
    local name_entry
    local name_uid

    id_entry="$(getent passwd "${host_uid}" || true)"
    name_entry="$(getent passwd "${host_user}" || true)"
    if [[ -n "${name_entry}" ]]; then
        IFS=: read -r _ _ name_uid _ <<<"${name_entry}"
        if [[ "${name_uid}" != "${host_uid}" ]]; then
            echo "codex-safe-entrypoint: user ${host_user@Q} already uses UID ${name_uid}" >&2
            return 1
        fi
    fi

    if [[ -z "${id_entry}" ]]; then
        useradd \
            --uid "${host_uid}" \
            --gid "${host_gid}" \
            --home-dir "${host_home}" \
            --no-create-home \
            --shell /bin/bash \
            "${host_user}"
        return
    fi

    id_name="${id_entry%%:*}"
    if [[ "${id_name}" != "${host_user}" ]]; then
        if [[ -n "${name_entry}" ]]; then
            echo "codex-safe-entrypoint: UID ${host_uid} has conflicting user names" >&2
            return 1
        fi
        usermod --login "${host_user}" "${id_name}"
    fi
    usermod --gid "${host_gid}" --home "${host_home}" "${host_user}"
}

configure_host_group
configure_host_user

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

mkdir -p "${host_home}"
chown "${host_uid}:${host_gid}" "${host_home}" /var/run/docker.sock
chmod 0700 "${host_home}"
chmod 0600 /var/run/docker.sock
install \
    --owner="${host_uid}" \
    --group="${host_gid}" \
    --mode=0644 \
    /etc/codex-safe/bashrc \
    "${host_home}/.bashrc"

exec setpriv \
    --reuid="${host_uid}" \
    --regid="${host_gid}" \
    --clear-groups \
    -- \
    env HOME="${host_home}" \
    "$@"
