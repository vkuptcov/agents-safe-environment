set -Eeuo pipefail

: "${REPORT:?REPORT is required}"
: "${READY:?READY is required}"
: "${RELEASE:?RELEASE is required}"
: "${LINKED_WORKTREE:?LINKED_WORKTREE is required}"
: "${NESTED_MARKER:?NESTED_MARKER is required}"
: "${HOST_SENTINEL_NAME:?HOST_SENTINEL_NAME is required}"
: "${NESTED_CONTAINER_NAME:?NESTED_CONTAINER_NAME is required}"
: "${COMPOSE_FILE:?COMPOSE_FILE is required}"
: "${COMPOSE_PROJECT:?COMPOSE_PROJECT is required}"
: "${COMPOSE_CONTAINER_NAME:?COMPOSE_CONTAINER_NAME is required}"
: "${NESTED_IMAGE:?NESTED_IMAGE is required}"

nested_daemon="$(docker info --format '{{.ID}}')"
nested_runtime="$(docker info --format '{{.DefaultRuntime}}')"

sentinel_visible=false
docker ps -a --format '{{.Names}}' | grep -Fxq "$HOST_SENTINEL_NAME" && sentinel_visible=true

docker run --detach --name "$NESTED_CONTAINER_NAME" \
    --user "$CODEX_SAFE_HOST_UID:$CODEX_SAFE_HOST_GID" \
    --mount "type=bind,source=$LINKED_WORKTREE,target=$LINKED_WORKTREE" \
    "$NESTED_IMAGE" \
    /bin/sh -c 'printf "nested marker\n" > "$1"; while [ ! -e "$2" ]; do sleep 1; done' \
    sh "$NESTED_MARKER" "$RELEASE" >/dev/null

for _ in $(seq 1 60); do
    [[ -e "$NESTED_MARKER" ]] && break
    docker inspect "$NESTED_CONTAINER_NAME" >/dev/null
    sleep 1
done
nested_running="$(docker inspect --format '{{.State.Running}}' "$NESTED_CONTAINER_NAME")"

printf 'services:\n  smoke:\n    image: %s\n    container_name: %s\n    command: ["/bin/sleep", "300"]\n' \
    "$NESTED_IMAGE" "$COMPOSE_CONTAINER_NAME" > "$COMPOSE_FILE"
docker compose --project-name "$COMPOSE_PROJECT" --file "$COMPOSE_FILE" up --detach >/dev/null
compose_running="$(docker inspect --format '{{.State.Running}}' "$COMPOSE_CONTAINER_NAME")"

printf 'nested_daemon=%s\nnested_runtime=%s\nsentinel_visible=%s\nnested_running=%s\ncompose_running=%s\n' \
    "$nested_daemon" "$nested_runtime" "$sentinel_visible" "$nested_running" "$compose_running" > "$REPORT"

printf ready > "$READY"
while [[ ! -e "$RELEASE" ]]; do
    sleep 1
done
docker compose --project-name "$COMPOSE_PROJECT" --file "$COMPOSE_FILE" down --remove-orphans >/dev/null
docker rm --force "$NESTED_CONTAINER_NAME" >/dev/null
rm -f "$COMPOSE_FILE"
