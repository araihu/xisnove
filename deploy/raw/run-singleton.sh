#!/bin/sh
set -eu

[ "$#" -ge 2 ] || { printf 'usage: run-singleton.sh LOCK COMMAND [ARG...]\n' >&2; exit 2; }
lock=$1
shift
directory=$lock.d

if command -v flock >/dev/null 2>&1; then
	exec flock --exclusive --nonblock --conflict-exit-code 75 "$lock" "$@"
fi

acquire() {
	if mkdir "$directory" 2>/dev/null; then
		printf '%s\n' "$$" >"$directory/owner"
		return 0
	fi
	owner=$(cat "$directory/owner" 2>/dev/null || true)
	if [ -n "$owner" ] && kill -0 "$owner" 2>/dev/null; then
		printf 'another Xisnove server owns %s\n' "$lock" >&2
		return 75
	fi
	rm -f "$directory/owner"
	rmdir "$directory" 2>/dev/null || {
		printf 'singleton lock directory is not recoverable: %s\n' "$directory" >&2
		return 75
	}
	mkdir "$directory" 2>/dev/null || {
		printf 'another Xisnove server owns %s\n' "$lock" >&2
		return 75
	}
	printf '%s\n' "$$" >"$directory/owner"
}

release() {
	owner=$(cat "$directory/owner" 2>/dev/null || true)
	if [ "$owner" = "$$" ]; then
		rm -f "$directory/owner"
		rmdir "$directory" 2>/dev/null || true
	fi
}

acquire
trap release EXIT HUP INT TERM
"$@" &
child=$!
trap 'kill -TERM "$child" 2>/dev/null || true' HUP INT TERM
wait "$child"
