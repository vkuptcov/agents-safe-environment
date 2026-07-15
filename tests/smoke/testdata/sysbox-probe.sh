set -Eeuo pipefail

report=$1
ready=$2
release=$3
linked=$4
primary=$5
nested_marker=$6
expected_user=$7
expected_group=$8
git_marker=$9
expected_home=${10}
sentinel=${11}
nested_name=${12}
compose_file=${13}
compose_project=${14}
compose_name=${15}
staged=${16}
cyrillic=${17}
nested_image=${18}

[[ "$(whoami)" == "$expected_user" ]]
[[ "$(id -gn)" == "$expected_group" ]]
[[ "$HOME" == "$expected_home" ]]

passwd_home="$(getent passwd "$expected_user" | cut -d: -f6)"
[[ "$passwd_home" == "$expected_home" ]]
sudo_uid="$(sudo --non-interactive id -u)"
[[ "$sudo_uid" == 0 ]]

sudoers_mode="$(stat -c '%a' /etc/sudoers.d/codex-safe-host)"
[[ "$sudoers_mode" == 440 ]]
sudoers_writable=false
[[ -w /etc/sudoers.d/codex-safe-host ]] && sudoers_writable=true

[[ "$(git config --global --get codex-safe-smoke.marker)" == "$git_marker" ]]
git_writable=false
(printf '\n[test]\n' >>"$HOME/.gitconfig") 2>/dev/null && git_writable=true

locale_name="$(locale charmap)"
[[ "$locale_name" == UTF-8 ]]
colors="$(tput colors)"
[[ "$colors" =~ ^[0-9]+$ ]] && (( colors >= 256 ))
color_prompt=false
bash -ic '[[ ${PS1} == *"01;32m"* && ${PS1} == *"01;34m"* ]]' >/dev/null 2>&1 && color_prompt=true
color_ls=false
bash -ic 'alias ls' 2>/dev/null | grep -Fq 'ls --color=auto' && color_ls=true
tools=false
command -v less >/dev/null && command -v make >/dev/null && command -v rg >/dev/null && docker compose version >/dev/null && bash -ic '_completion_loader make; complete -p make' >/dev/null 2>&1 && tools=true
[[ "$tools" == true ]]

printf 'Привет из codex-safe\n' > "$cyrillic"
[[ "$(cat "$cyrillic")" == 'Привет из codex-safe' ]]
git -c safe.directory="$linked" -C "$linked" status --short >/dev/null
printf 'staged by Sysbox probe\n' > "$staged"
git -c safe.directory="$linked" -C "$linked" add "$(basename "$staged")"
common_git="$(git -c safe.directory="$linked" -C "$linked" rev-parse --path-format=absolute --git-common-dir)"
common_marker="$common_git/probe-write"
printf ok > "$common_marker"
rm "$common_marker"
if (printf forbidden >>"$primary/baseline.txt") 2>/dev/null; then
    exit 1
fi

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

printf 'services:\n  smoke:\n    image: %s\n    container_name: %s\n    command: ["/bin/sleep", "300"]\n' "$nested_image" "$compose_name" > "$compose_file"
docker compose --project-name "$compose_project" --file "$compose_file" up --detach >/dev/null
compose_running="$(docker inspect --format '{{.State.Running}}' "$compose_name")"
[[ "$compose_running" == true ]]

printf 'user=%s\ngroup=%s\nhome=%s\npasswd_home=%s\nsudo_uid=%s\nsudoers_mode=%s\nsudoers_writable=%s\ngit_marker=%s\ngit_writable=%s\nlocale=%s\ncolors=%s\ncolor_prompt=%s\ncolor_ls=%s\ntools=%s\nnested_daemon=%s\nnested_runtime=%s\nsentinel_visible=%s\nnested_running=%s\ncompose_running=%s\n' \
    "$(whoami)" "$(id -gn)" "$HOME" "$passwd_home" "$sudo_uid" "$sudoers_mode" "$sudoers_writable" "$git_marker" "$git_writable" "$locale_name" "$colors" "$color_prompt" "$color_ls" "$tools" "$nested_daemon" "$nested_runtime" "$sentinel_visible" "$nested_running" "$compose_running" > "$report"

printf ready > "$ready"
while [[ ! -e "$release" ]]; do
    sleep 1
done
docker compose --project-name "$compose_project" --file "$compose_file" down --remove-orphans >/dev/null
docker rm --force "$nested_name" >/dev/null
rm -f "$compose_file"
