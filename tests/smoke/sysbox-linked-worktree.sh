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
binary="${temp_root}/codex-safe"
outer_log="${temp_root}/outer.log"
sentinel_name="codex-safe-host-sentinel-${run_id}"
launcher_pid=""
outer_container=""

cleanup() {
    local status=$?
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

printf 'ready\n' >"${ready_marker}"
for (( attempt = 1; attempt <= 120; attempt++ )); do
    if [[ -e "${continue_marker}" ]]; then
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

"${binary}" \
    --project "${nested_directory}" \
    --image codex-safe-mvp:local \
    -- \
    "${probe_script}" "${ready_marker}" "${continue_marker}" \
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

echo "smoke: harness completed"
