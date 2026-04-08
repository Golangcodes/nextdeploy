#!/usr/bin/env bash
#
# Benchmark CLI startup time for nextdeploy and any installed competitors.
#
# We measure the cold-cache cost of `<cli> --version`, which is the cheapest
# possible invocation: parse argv, look up one variable, print, exit. The gap
# between tools at this point is the gap in interpreter boot, dependency
# loading, and framework initialization. It's the *floor* on what every other
# command will pay.
#
# Why this is a fair benchmark:
#   - Same OS, same kernel, same disk cache, run back-to-back
#   - Each tool only has to print its version → no real work, no network
#   - 10 runs each with cold/median/mean → smooths out scheduler noise
#   - We don't claim performance on real deploys here, only startup floor
#
# What it does NOT measure: actual deploy speed, build time, network latency.
# Those are dominated by AWS API round-trips and Next.js compilation.

set -euo pipefail

RUNS=10
NEXTDEPLOY_BIN="${NEXTDEPLOY_BIN:-./bin/nextdeploy}"

if [ ! -x "$NEXTDEPLOY_BIN" ]; then
  echo "nextdeploy binary not found at $NEXTDEPLOY_BIN — run 'make build-cli' first" >&2
  exit 1
fi

# time_ms <command> [args...]  → prints elapsed wall time in milliseconds
time_ms() {
  local start_ns end_ns
  start_ns=$(date +%s%N)
  "$@" >/dev/null 2>&1
  end_ns=$(date +%s%N)
  echo $(( (end_ns - start_ns) / 1000000 ))
}

# bench_tool <name> <command> [args...] → runs $RUNS times, prints summary line
bench_tool() {
  local name="$1"; shift
  local samples=()
  local i sum=0 min=99999999 max=0

  # one warm-up to load disk cache, not counted
  "$@" >/dev/null 2>&1 || true

  for i in $(seq 1 "$RUNS"); do
    local t
    t=$(time_ms "$@")
    samples+=("$t")
    sum=$(( sum + t ))
    [ "$t" -lt "$min" ] && min=$t
    [ "$t" -gt "$max" ] && max=$t
  done

  # median: sort samples, pick middle
  local sorted median
  sorted=$(printf '%s\n' "${samples[@]}" | sort -n)
  median=$(echo "$sorted" | awk -v n="$RUNS" 'NR==int((n+1)/2)')

  local mean=$(( sum / RUNS ))
  printf "  %-30s  min %4d ms   median %4d ms   mean %4d ms   max %4d ms\n" \
    "$name" "$min" "$median" "$mean" "$max"
}

echo
echo "═══════════════════════════════════════════════════════════════════════════"
echo "  CLI startup benchmark — \`<tool> --version\`"
echo "  Runs: $RUNS per tool (1 warm-up, not counted)"
echo "  Measures the floor cost of invoking each CLI."
echo "═══════════════════════════════════════════════════════════════════════════"
echo

bench_tool "nextdeploy (this repo)" "$NEXTDEPLOY_BIN" --version

if command -v vercel >/dev/null 2>&1; then
  bench_tool "vercel" vercel --version
fi

if command -v sst >/dev/null 2>&1; then
  bench_tool "sst" sst --version
fi

if command -v netlify >/dev/null 2>&1; then
  bench_tool "netlify" netlify --version
fi

if command -v wrangler >/dev/null 2>&1; then
  bench_tool "wrangler (cloudflare)" wrangler --version
fi

if command -v amplify >/dev/null 2>&1; then
  bench_tool "amplify" amplify --version
fi

echo
echo "  Binary size:"
printf "    nextdeploy:  %s\n" "$(ls -lh "$NEXTDEPLOY_BIN" | awk '{print $5}')"
if command -v vercel >/dev/null 2>&1; then
  vercel_path=$(command -v vercel)
  # Resolve through wrappers for an accurate node_modules size
  if [ -L "$vercel_path" ]; then
    vercel_path=$(readlink -f "$vercel_path")
  fi
  printf "    vercel CLI:  ~%s installed (node_modules walk skipped)\n" "$(du -h "$(dirname "$vercel_path")" 2>/dev/null | tail -1 | awk '{print $1}')"
fi

echo
echo "  Note: nextdeploy is a single statically-linked Go binary with zero runtime"
echo "  dependencies. Other tools listed are Node CLIs that boot a JS runtime"
echo "  before doing anything. The gap is structural, not optimization."
echo
