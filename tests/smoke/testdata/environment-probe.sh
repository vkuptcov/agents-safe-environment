set -Eeuo pipefail

report=$1
ready=$2
release=$3
expected_user=$4
expected_group=$5
git_marker=$6
expected_home=$7

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
command -v less >/dev/null \
    && command -v make >/dev/null \
    && command -v rg >/dev/null \
    && docker compose version >/dev/null \
    && bash -ic '_completion_loader make; complete -p make' >/dev/null 2>&1 \
    && tools=true
[[ "$tools" == true ]]

printf 'user=%s\ngroup=%s\nhome=%s\npasswd_home=%s\nsudo_uid=%s\nsudoers_mode=%s\nsudoers_writable=%s\ngit_marker=%s\ngit_writable=%s\nlocale=%s\ncolors=%s\ncolor_prompt=%s\ncolor_ls=%s\ntools=%s\n' \
    "$(whoami)" "$(id -gn)" "$HOME" "$passwd_home" "$sudo_uid" "$sudoers_mode" \
    "$sudoers_writable" "$git_marker" "$git_writable" "$locale_name" "$colors" \
    "$color_prompt" "$color_ls" "$tools" > "$report"

printf ready > "$ready"
while [[ ! -e "$release" ]]; do
    sleep 1
done
