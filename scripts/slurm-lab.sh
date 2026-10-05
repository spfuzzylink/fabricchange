#!/usr/bin/env bash
# Destructive only to its disposable lab: never run on a cluster/client host.
set -euo pipefail
export LC_ALL=C

fail() { printf 'Slurm lab: %s\n' "$*" >&2; exit 1; }
[[ "${1:-}" == --disposable-github-runner && $# == 1 ]] || fail 'requires --disposable-github-runner'
[[ "${CI:-}" == true && "${GITHUB_ACTIONS:-}" == true && "${RUNNER_ENVIRONMENT:-}" == github-hosted && "${RUNNER_OS:-}" == Linux ]] || fail 'requires a disposable GitHub-hosted Linux runner'
[[ $(id -u) != 0 ]] || fail 'invoke as the normal runner user, not root'
# shellcheck source=/dev/null
source /etc/os-release
[[ "$ID" == ubuntu && "$VERSION_ID" == 24.04 ]] || fail 'requires Ubuntu 24.04'
for tool in sudo systemctl systemd-run timeout findmnt squeue scontrol sbatch scancel slurmd slurmctld munge munged go; do
  command -v "$tool" >/dev/null || fail "missing $tool"
done
sudo -n true
for daemon in slurmctld slurmd slurmstepd munged; do
  if pgrep -x "$daemon" >/dev/null; then fail "preexisting $daemon; refusing to touch this host"; fi
done
if sudo systemctl list-units --all --plain --no-legend slurmstepd.scope | grep -q slurmstepd.scope; then
  fail 'preexisting slurmstepd.scope; refusing to touch this host'
fi

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"
# Prevent caller-specific scheduler options from redirecting the isolated lab.
for variable in $(compgen -v | grep -E '^(SLURM_|SBATCH_|SQUEUE_|SCONTROL_|SCANCEL_)' || true); do unset "$variable"; done
lab_dir=$(sudo mktemp -d /var/lib/fabricchange-lab.XXXXXX)
lab_name=${lab_dir##*/}
munge_unit="$lab_name-munge.service"
controller_unit="$lab_name-controller.service"
worker_unit="$lab_name-worker.service"
job_id=
job_pid=
worker_started=false
finished=false

cleanup() {
  local code=$?
  local unit state
  trap - EXIT INT TERM
  set +e
  if [[ -n "$job_id" ]]; then timeout 10s scancel "$job_id"; fi
  # Kill only the units created below. Stop the Slurm-created scope only after
  # proving it did not predate this lab and attempting the owned worker start.
  local units=("$worker_unit" "$controller_unit")
  if [[ "$worker_started" == true ]]; then units+=(slurmstepd.scope); fi
  units+=("$munge_unit")
  for unit in "${units[@]}"; do
    if [[ $(sudo systemctl show --property=LoadState --value "$unit") == not-found ]]; then continue; fi
    if ! timeout 15s sudo systemctl stop "$unit"; then
      printf 'Slurm lab: failed to stop owned unit %s\n' "$unit" >&2
      code=1
    fi
    state=$(sudo systemctl show --property=ActiveState --value "$unit")
    if [[ "$state" != inactive && "$state" != failed ]]; then
      printf 'Slurm lab: owned unit %s remains in state %s\n' "$unit" "$state" >&2
      code=1
    fi
  done
  if [[ -n "$job_pid" ]] && kill -0 "$job_pid" 2>/dev/null; then
    printf 'Slurm lab: payload process survived daemon cleanup\n' >&2
    code=1
  fi
  if [[ "$finished" != true || "$code" != 0 ]]; then
    for unit in "$munge_unit" "$controller_unit" "$worker_unit"; do
      sudo journalctl --no-pager -n 80 -u "$unit" 2>/dev/null
    done
    for file in "$lab_dir/controller/slurmctld.log" "$lab_dir/worker/slurmd.log" "$lab_dir/results/job.out"; do
      if sudo test -f "$file"; then sudo tail -n 80 "$file"; fi
    done
  fi
  sudo systemctl reset-failed "$worker_unit" "$controller_unit" "$munge_unit" 2>/dev/null
  if ! sudo rm -rf -- "$lab_dir"; then code=1; fi
  if [[ "$finished" == true && "$code" == 0 ]]; then
    printf '\nPASS: CPU-only Slurm capture/import, cancellation, and owned service cleanup verified.\n'
    if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
      printf '### FabricChange live Slurm lab\n\nPassed on a disposable CPU-only Ubuntu 24.04 runner: real array task execution, running job ID/owner/node, pending exclusion, incomplete inventory, cancellation, empty capture, payload process exit, and owned service cleanup. This does not validate GPU hardware, multi-node expansion, or production operation.\n' >> "$GITHUB_STEP_SUMMARY"
    fi
  fi
  exit "$code"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

mount_options=$(findmnt --noheadings --output OPTIONS --target "$lab_dir") || fail 'cannot inspect lab filesystem mount options'
case ",$mount_options," in
  *,noexec,*) fail 'lab filesystem must allow the test binary and batch script to execute' ;;
esac

runner_user=$(id -un)
runner_group=$(id -gn)
node_name=$(hostname -s)
[[ "$node_name" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || fail 'unexpected hostname'
sudo chmod 755 "$lab_dir"
sudo install -d -m 755 -o munge -g munge "$lab_dir/munge"
sudo install -d -m 755 -o slurm -g slurm "$lab_dir/controller"
sudo install -d -m 755 -o root -g root "$lab_dir/worker"
sudo install -d -m 755 -o "$runner_user" -g "$runner_group" "$lab_dir/results"
sudo dd if=/dev/urandom of="$lab_dir/munge/key" bs=1024 count=1 status=none
sudo chown munge:munge "$lab_dir/munge/key"
sudo chmod 600 "$lab_dir/munge/key"

# Use the actual host's CPU topology; do not emulate multiple nodes. Reserve a
# modest memory amount so kernel/runner overhead cannot invalidate registration.
node_config=$(slurmd -C | sed -n '1p' | sed -E 's/ UpTime=[^ ]+//g; s/ RealMemory=[0-9]+/ RealMemory=512/')
[[ "$node_config" == "NodeName=$node_name "* ]] || fail 'unexpected slurmd -C node identity'
sudo tee "$lab_dir/slurm.conf" >/dev/null <<EOF
ClusterName=fabricchange-lab
SlurmctldHost=$node_name(127.0.0.1)
SlurmctldPort=17317
SlurmdPort=17318
SlurmUser=slurm
SlurmdUser=root
AuthType=auth/munge
AuthInfo=socket=$lab_dir/munge/socket
StateSaveLocation=$lab_dir/controller
SlurmdSpoolDir=$lab_dir/worker
SlurmctldPidFile=$lab_dir/controller/slurmctld.pid
SlurmdPidFile=$lab_dir/worker/slurmd.pid
SlurmctldLogFile=$lab_dir/controller/slurmctld.log
SlurmdLogFile=$lab_dir/worker/slurmd.log
SlurmctldDebug=info
SlurmdDebug=info
ProctrackType=proctrack/linuxproc
TaskPlugin=task/none
SelectType=select/cons_tres
SelectTypeParameters=CR_CPU
SchedulerType=sched/backfill
ReturnToService=2
SlurmctldTimeout=30
SlurmdTimeout=30
KillWait=5
InactiveLimit=0
$node_config NodeAddr=127.0.0.1 State=UNKNOWN
PartitionName=lab Nodes=$node_name Default=YES MaxTime=00:05:00 State=UP
EOF
sudo chmod 644 "$lab_dir/slurm.conf"
export SLURM_CONF="$lab_dir/slurm.conf"
go build -o "$lab_dir/results/fabricchange" ./cmd/fabricchange
"$lab_dir/results/fabricchange" version

printf 'Lab commit: %s\nRun: %s/%s/actions/runs/%s\n' "${GITHUB_SHA:-unknown}" "${GITHUB_SERVER_URL:-https://github.com}" "${GITHUB_REPOSITORY:-unknown}" "${GITHUB_RUN_ID:-unknown}"
dpkg-query -W -f='${Package} ${Version}\n' munge slurmctld slurmd slurm-client
sudo systemd-run --quiet --unit="$munge_unit" --service-type=simple --property=User=munge --property=Group=munge --property=TimeoutStopSec=10s \
  /usr/sbin/munged --foreground --socket="$lab_dir/munge/socket" --key-file="$lab_dir/munge/key" \
  --pid-file="$lab_dir/munge/munged.pid" --seed-file="$lab_dir/munge/seed"
for attempt in {1..30}; do
  if munge --socket="$lab_dir/munge/socket" -n >/dev/null 2>&1; then break; fi
  sleep 1
done
munge --socket="$lab_dir/munge/socket" -n >/dev/null || fail 'MUNGE did not become ready'
sudo systemd-run --quiet --unit="$controller_unit" --service-type=simple --property=TimeoutStopSec=10s \
  /usr/sbin/slurmctld -D -f "$SLURM_CONF"
worker_started=true
sudo systemd-run --quiet --unit="$worker_unit" --service-type=notify \
  --property=Delegate=yes --property=KillMode=process --property=TimeoutStartSec=45s --property=TimeoutStopSec=10s \
  /usr/sbin/slurmd --systemd -f "$SLURM_CONF"
for attempt in {1..60}; do
  if timeout 5s scontrol show node "$node_name" -o > "$lab_dir/results/node.txt" 2>/dev/null && \
    grep -q 'State=IDLE' "$lab_dir/results/node.txt"; then break; fi
  sleep 1
done
grep -q 'State=IDLE' "$lab_dir/results/node.txt" || fail 'worker did not become IDLE'
# No imported external jobs or controller state should exist in this fresh lab.
timeout 10s squeue --local --all --noheader > "$lab_dir/results/initial-queue.txt"
[[ ! -s "$lab_dir/results/initial-queue.txt" ]] || fail 'unexpected initial allocation'

cat > "$lab_dir/results/job.sh" <<EOF
#!/usr/bin/env bash
set -eu
printf '%s\n' "\$\$" > "$lab_dir/results/started.pid"
exec sleep 120
EOF
job_id=$(timeout 10s sbatch --parsable --array=0-1%1 --nodes=1 --ntasks=1 --time=00:03:00 \
  --output="$lab_dir/results/job.out" "$lab_dir/results/job.sh")
[[ "$job_id" =~ ^[0-9]+$ ]] || fail 'unexpected sbatch job id'
running_id=
pending_id=
for attempt in {1..60}; do
  timeout 5s squeue --local --all --array --noheader --jobs="$job_id" --format='%i|%T' > "$lab_dir/results/queue.txt"
  running_id=$(awk -F '|' '$2 == "RUNNING" {print $1}' "$lab_dir/results/queue.txt")
  pending_id=$(awk -F '|' '$2 == "PENDING" {print $1}' "$lab_dir/results/queue.txt")
  if [[ "$running_id" =~ ^${job_id}_[01]$ && "$pending_id" =~ ^${job_id}_[01]$ && -s "$lab_dir/results/started.pid" ]]; then break; fi
  sleep 1
done
[[ "$running_id" =~ ^${job_id}_[01]$ && "$pending_id" =~ ^${job_id}_[01]$ && "$running_id" != "$pending_id" ]] || fail 'expected one running and one pending array task'
job_pid=$(cat "$lab_dir/results/started.pid")
[[ "$job_pid" =~ ^[0-9]+$ ]] && kill -0 "$job_pid" || fail 'batch payload did not start'
printf '%s\n' "$running_id" "$pending_id" "$runner_user" "$node_name" > "$lab_dir/results/expected.txt"
timeout 20s bash scripts/capture-slurm.sh > "$lab_dir/results/running.txt"
timeout 10s scancel "$job_id"
for attempt in {1..30}; do
  timeout 5s squeue --local --all --array --noheader > "$lab_dir/results/remaining.txt"
  if [[ ! -s "$lab_dir/results/remaining.txt" ]] && ! kill -0 "$job_pid" 2>/dev/null; then break; fi
  sleep 1
done
[[ ! -s "$lab_dir/results/remaining.txt" ]] || fail 'allocations survived cancellation'
if kill -0 "$job_pid" 2>/dev/null; then fail 'batch payload survived cancellation'; fi
job_id=
job_pid=
timeout 20s bash scripts/capture-slurm.sh > "$lab_dir/results/empty.txt"
FABRICCHANGE_LIVE_SLURM_DIR="$lab_dir/results" go test -count=1 -v -run '^TestLiveSlurmLab$' .
for file in node.txt queue.txt expected.txt running.txt running.json empty.txt empty.json; do
  printf '\n%s\n' "$file"
  cat "$lab_dir/results/$file"
done
finished=true
