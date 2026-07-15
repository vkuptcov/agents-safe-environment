set -Eeuo pipefail

report=$1
ready=$2
release=$3
linked=$4
nested_marker=$5
sentinel=$6
nested_name=$7
compose_file=$8
compose_project=$9
compose_name=${10}
nested_image=${11}

nested_daemon="$(docker info --format '{{.ID}}')"
nested_runtime="$(docker info --format '{{.DefaultRuntime}}')"
[[ -n "$nested_daemon" && "$nested_runtime" == crun ]]

sentinel_visible=false
docker ps -a --format '{{.Names}}' | grep -Fxq "$sentinel" && sentinel_visible=true
[[ "$sentinel_visible" == false ]]

docker run --detach --name "$nested_name" \
    --user "$CODEX_SAFE_HOST_UID:$CODEX_SAFE_HOST_GID" \
    --mount "type=bind,source=$linked,target=$linked" \
    "$nested_image" \
    /bin/sh -c 'printf "nested marker\n" > "$1"; while [ ! -e "$2" ]; do sleep 1; done' \
    sh "$nested_marker" "$release" >/dev/null

for _ in $(seq 1 60); do
    [[ -e "$nested_marker" ]] && break
    docker inspect "$nested_name" >/dev/null
    sleep 1
done
[[ -e "$nested_marker" ]]
nested_running="$(docker inspect --format '{{.State.Running}}' "$nested_name")"
[[ "$nested_running" == true ]]

printf 'services:\n  smoke:\n    image: %s\n    container_name: %s\n    command: ["/bin/sleep", "300"]\n' \
    "$nested_image" "$compose_name" > "$compose_file"
docker compose --project-name "$compose_project" --file "$compose_file" up --detach >/dev/null
compose_running="$(docker inspect --format '{{.State.Running}}' "$compose_name")"
[[ "$compose_running" == true ]]

printf 'nested_daemon=%s\nnested_runtime=%s\nsentinel_visible=%s\nnested_running=%s\ncompose_running=%s\n' \
    "$nested_daemon" "$nested_runtime" "$sentinel_visible" "$nested_running" "$compose_running" > "$report"

printf ready > "$ready"
while [[ ! -e "$release" ]]; do
    sleep 1
done
docker compose --project-name "$compose_project" --file "$compose_file" down --remove-orphans >/dev/null
docker rm --force "$nested_name" >/dev/null
rm -f "$compose_file"
