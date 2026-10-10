#!/usr/bin/env bash
# A/B benchmark driver for the proxy hot path.
#
# Runs the committed Go benchmarks against two revisions, alternating base and
# candidate one round at a time (strict A/B/A/B, -count=1 per run) so noise
# spreads evenly across both sides, then compares the runs with benchstat.
#
# Usage:
#   ./bench/run-benchmarks.sh [--base REF] [--cand REF] [--count N]
#       [--benchtime D] [--bench REGEX] [--packages PKGS] [--profiles DIR]
#
# Defaults: base=origin/main, cand=HEAD, count=10, benchtime=1s, bench=".",
#           packages="./internal/protoutil ./internal/router".
#
# The output header records the commit SHAs, CPU model, nproc, GOMAXPROCS, and
# Go version, so a captured run is self-attesting. For a quick single-revision
# sanity run, `mise run test:bench` does that without this script.

set -euo pipefail

BASE=origin/main
CAND=HEAD
COUNT=10
BENCHTIME=1s
BENCH=.
PKGS="./internal/protoutil ./internal/router"
PROFILES=""

while [[ $# -gt 0 ]]; do
	case "$1" in
	--base) BASE="$2"; shift 2 ;;
	--cand) CAND="$2"; shift 2 ;;
	--count) COUNT="$2"; shift 2 ;;
	--benchtime) BENCHTIME="$2"; shift 2 ;;
	--bench) BENCH="$2"; shift 2 ;;
	--packages) PKGS="$2"; shift 2 ;;
	--profiles) PROFILES="$2"; shift 2 ;;
	*) echo "unknown flag: $1" >&2; exit 2 ;;
	esac
done

command -v go >/dev/null 2>&1 || { echo "go is required (mise install go works too)" >&2; exit 1; }
git diff-index --quiet HEAD -- || { echo "working tree is dirty; commit or stash first" >&2; exit 1; }

baseSHA="$(git rev-parse --short "${BASE}^{commit}")"
candSHA="$(git rev-parse --short "${CAND}^{commit}")"

# GOMAXPROCS is fixed for the whole run: ns/op comparisons mean nothing if the
# parallelism changes between rounds.
export GOMAXPROCS="$(nproc)"

{
	echo "# base:       $BASE ($baseSHA)"
	echo "# cand:       $CAND ($candSHA)"
	echo "# date:       $(date -u +%Y-%m-%dT%H:%M:%SZ)"
	echo "# kernel:     $(uname -r)"
	echo "# cpu:        $(grep -m1 'model name' /proc/cpuinfo | cut -d: -f2 | sed 's/^ *//')"
	echo "# nproc:      $(nproc)"
	echo "# GOMAXPROCS: $GOMAXPROCS"
	echo "# go:         $(go version)"
	echo "# bench:      $BENCH  benchtime: $BENCHTIME  rounds: $COUNT"
	echo "# packages:   $PKGS"
} | tee bench-header.txt

WORK="$(mktemp -d)"
trap 'git worktree remove --force "$WORK/base" >/dev/null 2>&1 || true; git worktree remove --force "$WORK/cand" >/dev/null 2>&1 || true; rm -rf "$WORK"' EXIT

git worktree add --detach "$WORK/base" "$BASE" >/dev/null
git worktree add --detach "$WORK/cand" "$CAND" >/dev/null

# Warm both build caches before the first round, so round 1 does not pay the
# compile.
(cd "$WORK/base" && go build ./internal/...) >/dev/null
(cd "$WORK/cand" && go build ./internal/...) >/dev/null

: > "$WORK/base.txt"
: > "$WORK/cand.txt"
for i in $(seq 1 "$COUNT"); do
	echo "=== round $i/$COUNT: base ===" >&2
	# PKGS is intentionally unquoted: it is a space-separated package list.
	(cd "$WORK/base" && go test -run '^$' -bench "$BENCH" -benchmem -count=1 -benchtime "$BENCHTIME" $PKGS) | tee -a "$WORK/base.txt"

	echo "=== round $i/$COUNT: cand ===" >&2
	(cd "$WORK/cand" && go test -run '^$' -bench "$BENCH" -benchmem -count=1 -benchtime "$BENCHTIME" $PKGS) | tee -a "$WORK/cand.txt"
done

cp "$WORK/base.txt" "bench-$baseSHA.txt"
cp "$WORK/cand.txt" "bench-$candSHA.txt"

echo
echo "=== benchstat ($baseSHA vs $candSHA) ==="
go run golang.org/x/perf/cmd/benchstat@latest "$WORK/base.txt" "$WORK/cand.txt" | tee "benchstat-$baseSHA-vs-$candSHA.txt"

if [[ -n "$PROFILES" ]]; then
	mkdir -p "$PROFILES"
	for side in base cand; do
		if [[ $side == base ]]; then ref="$baseSHA"; else ref="$candSHA"; fi
		dir="$PROFILES/$ref"
		mkdir -p "$dir"
		echo "=== profiles for $side ($ref) ===" >&2
		# -o keeps the test binary next to the profiles, which is what pprof
		# needs for symbolization.
		(cd "$WORK/$side" && go test -run '^$' -bench "$BENCH" -count=1 -benchtime "$BENCHTIME" $PKGS \
			-o "$dir/test.test" \
			-cpuprofile "$dir/cpu.out" \
			-memprofile "$dir/heap.out" \
			-blockprofile "$dir/block.out" \
			-mutexprofile "$dir/mutex.out") >/dev/null

		go tool pprof -top -nodecount=25 "$dir/test.test" "$dir/cpu.out" | tee "$dir/cpu-top.txt"
		go tool pprof -top -nodecount=25 -sample_index=alloc_space "$dir/test.test" "$dir/heap.out" | tee "$dir/heap-top.txt"
		go tool pprof -top -nodecount=15 "$dir/test.test" "$dir/block.out" | tee "$dir/block-top.txt"
		go tool pprof -top -nodecount=15 "$dir/test.test" "$dir/mutex.out" | tee "$dir/mutex-top.txt"
		# Block and mutex profiling are sampling-disabled until enabled; the
		# test binary enables them under -blockprofile/-mutexprofile, but
		# rates stay small. Zero tops are normal for short runs.
	done
	echo "profiles written under $PROFILES"
fi

echo
echo "artifacts: bench-header.txt bench-$baseSHA.txt bench-$candSHA.txt benchstat-$baseSHA-vs-$candSHA.txt ${PROFILES:+$PROFILES/}"
