#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  echo "usage: check-reproducible-candidate.sh --root DIR --version X.Y.Z[-prerelease] --commit SHA --source-date-epoch EPOCH" >&2
  exit 2
}

root=
version=
commit=
source_date_epoch=
while [[ $# -gt 0 ]]; do
  case "$1" in
    --root) root=${2-}; shift 2 ;;
    --version) version=${2-}; shift 2 ;;
    --commit) commit=${2-}; shift 2 ;;
    --source-date-epoch) source_date_epoch=${2-}; shift 2 ;;
    *) usage ;;
  esac
done
[[ -n "$root" && -n "$version" && -n "$commit" && -n "$source_date_epoch" ]] || usage
root=$(cd "$root" && pwd)
[[ "$commit" =~ ^[0-9a-f]{40}$ ]] || usage
[[ "$source_date_epoch" =~ ^[1-9][0-9]*$ ]] || usage
[[ "$(git -C "$root" rev-parse HEAD)" == "$commit" ]] || { echo "requested commit does not match HEAD" >&2; exit 1; }
[[ -z "$(git -C "$root" status --porcelain=v1 --untracked-files=all)" ]] || { echo "working tree must be clean" >&2; exit 1; }

reproducibility_tmp_root=${XISNOVE_RELEASE_TMPDIR:-$(dirname "$root")}
work_root=$(mktemp -d "$reproducibility_tmp_root/xisnove-reproducibility.XXXXXXXX")
work_root=$(cd "$work_root" && pwd -P)
work_a="$work_root/work-a"
work_b="$work_root/work-b"
out_a="$work_root/candidate-a"
out_b="$work_root/candidate-b"
cleanup() {
  status=$?
  trap - EXIT INT TERM
  git -C "$root" worktree remove --force "$work_a" >/dev/null 2>&1 || true
  git -C "$root" worktree remove --force "$work_b" >/dev/null 2>&1 || true
  rm -rf "$work_root"
  exit "$status"
}
trap cleanup EXIT INT TERM

git -C "$root" worktree add --quiet --detach "$work_a" "$commit"
git -C "$root" worktree add --quiet --detach "$work_b" "$commit"
for item in "$work_a:$out_a" "$work_b:$out_b"; do
  worktree=${item%%:*}
  output=${item#*:}
  bash "$worktree/scripts/release/build-candidate.sh" \
    --root "$worktree" \
    --output-dir "$output" \
    --version "$version" \
    --commit "$commit" \
    --source-date-epoch "$source_date_epoch"
done

if ! cmp --silent "$out_a/candidate-manifest.json" "$out_b/candidate-manifest.json"; then
  echo "candidate manifests differ" >&2
  exit 1
fi

inventory() {
  python3 - "$1" "$2" <<'PY'
import hashlib, json, os, pathlib, stat, sys

root = pathlib.Path(sys.argv[1])
records = []
for path in sorted(root.rglob("*")):
    relative = path.relative_to(root).as_posix()
    metadata = path.lstat()
    if stat.S_ISLNK(metadata.st_mode):
        raise SystemExit(f"candidate contains symlink: {relative}")
    if not stat.S_ISREG(metadata.st_mode):
        continue
    contents = path.read_bytes()
    records.append({
        "path": relative,
        "mode": stat.S_IMODE(metadata.st_mode),
        "size": len(contents),
        "sha256": hashlib.sha256(contents).hexdigest(),
    })
pathlib.Path(sys.argv[2]).write_text(json.dumps(records, separators=(",", ":")) + "\n")
PY
}
inventory "$out_a" "$work_root/inventory-a.json"
inventory "$out_b" "$work_root/inventory-b.json"
if ! cmp --silent "$work_root/inventory-a.json" "$work_root/inventory-b.json"; then
  echo "candidate trees differ" >&2
  exit 1
fi
printf 'reproducible candidate: %s\n' "$commit"
