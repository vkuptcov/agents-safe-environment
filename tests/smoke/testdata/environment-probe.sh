set -Eeuo pipefail

report=$1
ready=$2
release=$3

actual_user="$(whoami)"
actual_group="$(id -gn)"
actual_home="$HOME"
passwd_home="$(getent passwd "$actual_user" | cut -d: -f6)"
sudo_uid="$(sudo --non-interactive id -u)"
sudoers_mode="$(stat -c '%a' /etc/sudoers.d/agents-safe-host)"
sudoers_writable=false
[[ -w /etc/sudoers.d/agents-safe-host ]] && sudoers_writable=true

actual_git_marker="$(git config --global --get codex-safe-smoke.marker)"
git_writable=false
(printf '\n[test]\n' >>"$HOME/.gitconfig") 2>/dev/null && git_writable=true

locale_name="$(locale charmap)"
colors="$(tput colors)"
color_prompt=false
bash -ic '[[ ${PS1} == *"01;32m"* && ${PS1} == *"01;34m"* ]]' >/dev/null 2>&1 && color_prompt=true
color_ls=false
bash -ic 'alias ls' 2>/dev/null | grep -Fq 'ls --color=auto' && color_ls=true

tool_less=false
command -v less >/dev/null && tool_less=true
tool_make=false
command -v make >/dev/null && tool_make=true
tool_rg=false
command -v rg >/dev/null && tool_rg=true
docker_compose=false
docker compose version >/dev/null && docker_compose=true
make_completion=false
bash -ic '_completion_loader make; complete -p make' >/dev/null 2>&1 && make_completion=true

printf 'user=%s\ngroup=%s\nhome=%s\npasswd_home=%s\nsudo_uid=%s\nsudoers_mode=%s\nsudoers_writable=%s\ngit_marker=%s\ngit_writable=%s\nlocale=%s\ncolors=%s\ncolor_prompt=%s\ncolor_ls=%s\ntool_less=%s\ntool_make=%s\ntool_rg=%s\ndocker_compose=%s\nmake_completion=%s\n' \
    "$actual_user" "$actual_group" "$actual_home" "$passwd_home" "$sudo_uid" "$sudoers_mode" \
    "$sudoers_writable" "$actual_git_marker" "$git_writable" "$locale_name" "$colors" \
    "$color_prompt" "$color_ls" "$tool_less" "$tool_make" "$tool_rg" "$docker_compose" \
    "$make_completion" > "$report"

printf ready > "$ready"
while [[ ! -e "$release" ]]; do
    sleep 1
done
