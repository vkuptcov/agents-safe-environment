#!/usr/bin/env bash
set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
run_id="$(date +%s)-$$-${RANDOM}"
temp_root="$(mktemp -d "${TMPDIR:-/tmp}/codex-safe-smoke.XXXXXX")"
primary_repo="${temp_root}/primary repo"
linked_worktree="${temp_root}/feature worktree"
nested_directory="${linked_worktree}/nested directory"
probe_script="${linked_worktree}/smoke-probe.sh"
ready_marker="${linked_worktree}/.codex-safe-ready-${run_id}"
continue_marker="${linked_worktree}/.codex-safe-continue-${run_id}"
phase7_marker="${linked_worktree}/.codex-safe-phase7-${run_id}"
phase8_marker="${linked_worktree}/.codex-safe-phase8-${run_id}"
staged_relative="phase7-staged.txt"
staged_file="${linked_worktree}/${staged_relative}"
nested_marker="${linked_worktree}/phase8-nested-marker.txt"
nested_daemon_id_file="${linked_worktree}/.codex-safe-nested-daemon-${run_id}"
nested_name="codex-safe-nested-${run_id}"
nested_image="alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc"
binary="${temp_root}/codex-safe"
outer_log="${temp_root}/outer.log"
sentinel_name="codex-safe-host-sentinel-${run_id}"
launcher_pid=""
outer_container=""

cleanup() {
    local status=$?
    local cleanup_failed=false
    trap - EXIT INT TERM
    set +e

    touch "${continue_marker}" 2>/dev/null || true
    if [[ -n "${launcher_pid}" ]] && kill -0 "${launcher_pid}" 2>/dev/null; then
        kill -TERM "${launcher_pid}" 2>/dev/null || true
        wait "${launcher_pid}" 2>/dev/null || true
    fi
    if [[ -n "${outer_container}" ]]; then
        docker rm --force "${outer_container}" >/dev/null 2>&1 || true
    fi
    docker rm --force "${sentinel_name}" >/dev/null 2>&1 || true
    rm -rf "${temp_root}"

    if [[ -e "${temp_root}" ]]; then
        echo "smoke: cleanup could not remove ${temp_root}" >&2
        cleanup_failed=true
    fi
    if [[ "${cleanup_failed}" == true ]] && (( status == 0 )); then
        status=1
    fi

    exit "${status}"
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

wait_for_file() {
    local path=$1
    local timeout=$2

    for (( attempt = 1; attempt <= timeout; attempt++ )); do
        if [[ -e "${path}" ]]; then
            return 0
        fi
        if [[ -n "${launcher_pid}" ]] && ! kill -0 "${launcher_pid}" 2>/dev/null; then
            echo "smoke: launcher exited before creating ${path}" >&2
            cat "${outer_log}" >&2
            return 1
        fi
        sleep 1
    done

    echo "smoke: timed out waiting for ${path}" >&2
    cat "${outer_log}" >&2
    return 1
}

find_outer_container() {
    local candidate
    local sources

    while IFS= read -r candidate; do
        [[ -n "${candidate}" ]] || continue
        sources="$(docker inspect --format '{{range .Mounts}}{{println .Source}}{{end}}' "${candidate}")"
        if grep --fixed-strings --line-regexp --quiet "${linked_worktree}" <<<"${sources}"; then
            printf '%s\n' "${candidate}"
            return 0
        fi
    done < <(docker ps --filter 'label=codex-safe.session' --format '{{.ID}}')

    return 1
}

assert_equal() {
    local actual=$1
    local expected=$2
    local description=$3

    if [[ "${actual}" != "${expected}" ]]; then
        echo "smoke: ${description}: got ${actual@Q}, expected ${expected@Q}" >&2
        return 1
    fi
}

assert_report_line() {
    local expected=$1
    local report=$2
    local description=$3

    if ! grep --fixed-strings --line-regexp --quiet "${expected}" <<<"${report}"; then
        echo "smoke: missing ${description}: ${expected}" >&2
        echo "smoke: actual report:" >&2
        printf '%s\n' "${report}" >&2
        return 1
    fi
}

mkdir -p "${primary_repo}"
git init -b main "${primary_repo}" >/dev/null
git -C "${primary_repo}" config user.name "Codex Safe Smoke"
git -C "${primary_repo}" config user.email "codex-safe@example.invalid"
printf 'primary baseline\n' >"${primary_repo}/baseline.txt"
git -C "${primary_repo}" add baseline.txt
git -C "${primary_repo}" commit -m baseline >/dev/null
git -C "${primary_repo}" worktree add -b smoke/feature "${linked_worktree}" >/dev/null
mkdir -p "${nested_directory}"

cat >"${probe_script}" <<'PROBE'
#!/usr/bin/env bash
set -Eeuo pipefail

ready_marker=$1
continue_marker=$2
linked_worktree=$3
primary_repo=$4
phase7_marker=$5
run_id=$6
sentinel_name=$7
nested_image=$8
nested_name=$9
nested_marker=${10}
nested_daemon_id_file=${11}
phase8_marker=${12}

git -c safe.directory="${linked_worktree}" -C "${linked_worktree}" status --short >/dev/null
printf 'staged by Sysbox probe\n' >"${linked_worktree}/phase7-staged.txt"
git -c safe.directory="${linked_worktree}" -C "${linked_worktree}" add phase7-staged.txt

common_git_dir="$(
    git -c safe.directory="${linked_worktree}" \
        -C "${linked_worktree}" \
        rev-parse --path-format=absolute --git-common-dir
)"
common_marker="${common_git_dir}/codex-safe-write-${run_id}"
printf 'common Git directory is writable\n' >"${common_marker}"
rm -f "${common_marker}"

if printf 'forbidden write\n' >>"${primary_repo}/baseline.txt" 2>/tmp/primary-write-error; then
    echo "smoke probe: primary checkout unexpectedly accepted a write" >&2
    exit 1
fi

printf 'phase7 complete\n' >"${phase7_marker}"

nested_daemon_id="$(docker info --format '{{.ID}}')"
if [[ -z "${nested_daemon_id}" ]]; then
    echo "smoke probe: nested Docker daemon ID is empty" >&2
    exit 1
fi
if [[ "$(docker info --format '{{.DefaultRuntime}}')" != crun ]]; then
    echo "smoke probe: nested Docker does not use crun by default" >&2
    exit 1
fi
printf '%s\n' "${nested_daemon_id}" >"${nested_daemon_id_file}"

if docker ps -a --format '{{.Names}}' | grep --fixed-strings --line-regexp --quiet "${sentinel_name}"; then
    echo "smoke probe: nested Docker can see the host sentinel" >&2
    exit 1
fi

docker run \
    --detach \
    --name "${nested_name}" \
    --user "${CODEX_SAFE_HOST_UID}:${CODEX_SAFE_HOST_GID}" \
    --mount "type=bind,source=${linked_worktree},target=${linked_worktree}" \
    "${nested_image}" \
    /bin/sh -c 'printf "nested marker\n" >"$1"; while [ ! -e "$2" ]; do sleep 1; done' \
    sh "${nested_marker}" "${continue_marker}" \
    >/dev/null

for (( attempt = 1; attempt <= 60; attempt++ )); do
    if [[ -e "${nested_marker}" ]]; then
        break
    fi
    if ! docker inspect "${nested_name}" >/dev/null 2>&1; then
        echo "smoke probe: nested container exited before creating its marker" >&2
        exit 1
    fi
    sleep 1
done
if [[ ! -e "${nested_marker}" ]]; then
    echo "smoke probe: timed out waiting for nested marker" >&2
    exit 1
fi

printf 'phase8 complete\n' >"${phase8_marker}"

printf 'ready\n' >"${ready_marker}"
for (( attempt = 1; attempt <= 120; attempt++ )); do
    if [[ -e "${continue_marker}" ]]; then
        docker rm --force "${nested_name}" >/dev/null
        exit 0
    fi
    sleep 1
done

echo "smoke probe: timed out waiting for continue marker" >&2
exit 1
PROBE
chmod 0755 "${probe_script}"

(
    cd "${repo_root}"
    GOCACHE="${temp_root}/go-cache" \
    GOMODCACHE="${temp_root}/go-mod-cache" \
    go build -o "${binary}" ./cmd/codex-safe
)

docker build -t codex-safe-mvp:local -f "${repo_root}/container/Dockerfile" "${repo_root}" >/dev/null
docker run \
    --detach \
    --name "${sentinel_name}" \
    --label "codex-safe.smoke=${run_id}" \
    --entrypoint /bin/sleep \
    codex-safe-mvp:local \
    300 \
    >/dev/null
host_daemon_id="$(docker info --format '{{.ID}}')"

"${binary}" \
    --project "${nested_directory}" \
    --image codex-safe-mvp:local \
    -- \
    "${probe_script}" \
    "${ready_marker}" \
    "${continue_marker}" \
    "${linked_worktree}" \
    "${primary_repo}" \
    "${phase7_marker}" \
    "${run_id}" \
    "${sentinel_name}" \
    "${nested_image}" \
    "${nested_name}" \
    "${nested_marker}" \
    "${nested_daemon_id_file}" \
    "${phase8_marker}" \
    >"${outer_log}" 2>&1 &
launcher_pid=$!

wait_for_file "${ready_marker}" 120

for (( attempt = 1; attempt <= 30; attempt++ )); do
    if outer_container="$(find_outer_container)"; then
        break
    fi
    sleep 1
done
if [[ -z "${outer_container}" ]]; then
    echo "smoke: could not identify the live outer container" >&2
    exit 1
fi

echo "smoke: outer container ${outer_container} reached the inspection barrier"

runtime="$(docker inspect --format '{{.HostConfig.Runtime}}' "${outer_container}")"
privileged="$(docker inspect --format '{{.HostConfig.Privileged}}' "${outer_container}")"
working_dir="$(docker inspect --format '{{.Config.WorkingDir}}' "${outer_container}")"
mount_report="$(
    docker inspect \
        --format '{{range .Mounts}}{{printf "%s|%s|%t|%s\n" .Source .Destination .RW .Propagation}}{{end}}' \
        "${outer_container}"
)"

assert_equal "${runtime}" "sysbox-runc" "outer runtime"
assert_equal "${privileged}" "false" "outer privileged mode"
assert_equal "${working_dir}" "${nested_directory}" "outer working directory"
assert_report_line \
    "${primary_repo}|${primary_repo}|false|rprivate" \
    "${mount_report}" \
    "read-only primary checkout mount"
assert_report_line \
    "${primary_repo}/.git|${primary_repo}/.git|true|rprivate" \
    "${mount_report}" \
    "read-write common Git mount"
assert_report_line \
    "${linked_worktree}|${linked_worktree}|true|rprivate" \
    "${mount_report}" \
    "read-write linked worktree mount"
if grep --fixed-strings --quiet '/var/run/docker.sock' <<<"${mount_report}"; then
    echo "smoke: outer container unexpectedly mounts the host Docker socket" >&2
    exit 1
fi
if [[ ! -f "${phase7_marker}" ]]; then
    echo "smoke: probe did not complete Phase 7 assertions" >&2
    exit 1
fi
if [[ ! -f "${phase8_marker}" ]]; then
    echo "smoke: probe did not complete Phase 8 assertions" >&2
    exit 1
fi

nested_daemon_id="$(cat "${nested_daemon_id_file}")"
if [[ "${nested_daemon_id}" == "${host_daemon_id}" ]]; then
    echo "smoke: nested and host Docker daemon IDs are identical" >&2
    exit 1
fi
if [[ -z "${nested_daemon_id}" ]]; then
    echo "smoke: nested Docker daemon ID is empty" >&2
    exit 1
fi

host_nested_match="$(
    docker ps -a \
        --filter "name=^${nested_name}$" \
        --format '{{.Names}}'
)"
assert_equal "${host_nested_match}" "" "nested container visibility in host Docker"
assert_equal \
    "$(docker inspect --format '{{.State.Running}}' "${sentinel_name}")" \
    "true" \
    "host sentinel state"
assert_equal "$(cat "${nested_marker}")" "nested marker" "nested marker contents"
assert_equal \
    "$(stat -c '%u:%g' "${nested_marker}")" \
    "$(id -u):$(id -g)" \
    "nested marker ownership"
printf 'host edit\n' >>"${nested_marker}"
assert_equal \
    "$(tail -n 1 "${nested_marker}")" \
    "host edit" \
    "host edit of nested marker"

touch "${continue_marker}"

set +e
wait "${launcher_pid}"
launcher_status=$?
set -e
launcher_pid=""
if (( launcher_status != 0 )); then
    echo "smoke: launcher exited with status ${launcher_status}" >&2
    cat "${outer_log}" >&2
    exit "${launcher_status}"
fi

staged_paths="$(git -C "${linked_worktree}" diff --cached --name-only)"
assert_report_line "${staged_relative}" "${staged_paths}" "staged linked-worktree file"
assert_equal "$(cat "${staged_file}")" "staged by Sysbox probe" "staged file contents"
assert_equal "$(cat "${primary_repo}/baseline.txt")" "primary baseline" "primary checkout baseline"
assert_equal \
    "$(docker inspect --format '{{.State.Running}}' "${sentinel_name}")" \
    "true" \
    "host sentinel state after nested cleanup"
host_nested_match="$(
    docker ps -a \
        --filter "name=^${nested_name}$" \
        --format '{{.Names}}'
)"
assert_equal "${host_nested_match}" "" "nested object after outer shutdown"
bad_owner="$(
    find "${linked_worktree}" "${primary_repo}/.git" \
        ! -uid "$(id -u)" \
        -print \
        -quit
)"
if [[ -n "${bad_owner}" ]]; then
    echo "smoke: Sysbox write left a path not owned by the invoking user: ${bad_owner}" >&2
    exit 1
fi

echo "smoke: harness completed"
