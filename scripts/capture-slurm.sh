#!/usr/bin/env bash
# Read-only allocation capture. Requires Bash, Slurm clients, date, paste, mktemp.
# A complete capture is written to stdout only after every query succeeds.
set -euo pipefail
export LC_ALL=C
capture_path=$(mktemp)
jobs_path=$(mktemp)
trap 'rm -f "$capture_path" "$jobs_path"' EXIT
printf '# started_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$capture_path"
# --local intentionally scopes this to one Slurm cluster, not its federation.
# --array expands array tasks. Cluster permissions can still hide allocations.
squeue --local --all --array --noheader --states=RUNNING,SUSPENDED,COMPLETING --format='%i|%u|%T|%N' > "$jobs_path"
while IFS='|' read -r job_id owner state hostlist; do
  [[ -z "$job_id" ]] && continue
  if [[ -z "$owner" || -z "$state" || -z "$hostlist" || "$hostlist" == *'|'* ]]; then
    printf 'invalid squeue row; refusing partial capture\n' >&2
    exit 1
  fi
  expanded=$(scontrol show hostnames "$hostlist")
  [[ -n "$expanded" ]] || { printf 'empty node expansion\n' >&2; exit 1; }
  nodes=$(printf '%s\n' "$expanded" | paste -sd, -)
  printf '%s|%s|%s|%s\n' "$job_id" "$owner" "$state" "$nodes" >> "$capture_path"
done < "$jobs_path"
printf '# finished_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$capture_path"
cat "$capture_path"
