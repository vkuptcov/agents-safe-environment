set -Eeuo pipefail

linked=$1
primary=$2
staged=$3
cyrillic=$4

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
