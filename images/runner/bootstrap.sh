#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

: "${SLURM_GHA_SERVICE_URL:?missing service URL}"
: "${SLURM_GHA_LEASE_ID:?missing lease ID}"
: "${SLURM_GHA_CLAIM_TOKEN:?missing claim token}"
: "${SLURM_JOB_ID:?missing Slurm job ID}"

case "$SLURM_JOB_ID" in
  *[!0-9]*) printf 'invalid Slurm job ID\n' >&2; exit 64 ;;
esac

runner_root=/runner/runner
diagnostics_dir=/runner-logs
claim_config=/runner/.claim-curl.conf
claim_payload=/runner/.claim-request.json
jit_file=/runner/.jit-config

# Invoked indirectly by the EXIT trap.
# shellcheck disable=SC2317
cleanup() {
  status=$?
  rm -f -- "$claim_config" "$claim_payload" "$jit_file"
  if [ -d "$runner_root/_diag" ]; then
    cp -a -- "$runner_root/_diag/." "$diagnostics_dir/" 2>/dev/null || true
  fi
  exit "$status"
}
trap cleanup EXIT

mkdir -p -- "$runner_root" "$diagnostics_dir"
chmod 0700 -- "$runner_root" "$diagnostics_dir"
cp -a /opt/actions-runner/. "$runner_root/"

printf 'header = "Authorization: Bearer %s"\n' "$SLURM_GHA_CLAIM_TOKEN" >"$claim_config"
chmod 0600 "$claim_config"
printf '{"jobId":%s}\n' "$SLURM_JOB_ID" >"$claim_payload"
chmod 0600 "$claim_payload"
unset SLURM_GHA_CLAIM_TOKEN

claim_url="${SLURM_GHA_SERVICE_URL%/}/api/v1/leases/${SLURM_GHA_LEASE_ID}/claim"
attempt=0
until curl --config "$claim_config" --fail --silent --show-error \
  --connect-timeout 10 --max-time 60 --request POST \
  --header 'Content-Type: application/json' --data-binary "@$claim_payload" \
  --output "$jit_file" "$claim_url"; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 12 ]; then
    printf 'runner claim failed after %s attempts\n' "$attempt" >&2
    exit 69
  fi
  sleep $((attempt < 5 ? attempt : 5))
done
chmod 0600 "$jit_file"
rm -f -- "$claim_config" "$claim_payload"

ACTIONS_RUNNER_INPUT_JITCONFIG="$(<"$jit_file")"
export ACTIONS_RUNNER_INPUT_JITCONFIG
rm -f -- "$jit_file"
unset SLURM_GHA_SERVICE_URL SLURM_GHA_LEASE_ID SLURM_GHA_CLASS

cd "$runner_root"
if [ "$(id -u)" -eq 0 ]; then
  export RUNNER_ALLOW_RUNASROOT=1
fi
./run.sh &
runner_pid=$!
unset ACTIONS_RUNNER_INPUT_JITCONFIG
set +e
wait "$runner_pid"
runner_status=$?
set -e
exit "$runner_status"
