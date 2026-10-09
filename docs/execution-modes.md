# Execution modes

Every scale set selects one launch backend with `scaleSets[].execution.mode`. The controller owns leases and Slurm lifecycle; a small backend interface renders only the mode-specific `srun` step. Adding another Slurm launch plugin therefore requires a renderer, a registry entry, validation, and documentation rather than new conditionals throughout reconciliation.

| Mode | Slurm launch plugin | `execution.image` means | Status |
| --- | --- | --- | --- |
| `pyxis` | Pyxis/Enroot | OCI reference or shared SquashFS path | Implemented and end-to-end verified |
| `vmocs` | VMoCS | VMoCS template name passed to `--vm-image` | Implemented and end-to-end workflow verified |
| `native` | None | Not used | Reserved and rejected at service startup |

## Pyxis container mode

```yaml
execution:
  mode: "pyxis"
  image: "/cluster/images/actions-runner-2.337.0.sqsh"
  scratchRoot: "/shared/slurm-gha/scratch"
  mountHome: false
  extraMounts: []
```

The launcher mounts a private lease directory at `/runner`, a persistent diagnostic directory at `/runner-logs`, and the callback CA when configured. It disables the container entrypoint and home mount by default, then executes `/opt/slurm-gha/bootstrap.sh`.

## VMoCS VM mode

```yaml
execution:
  mode: "vmocs"
  image: "base-ubuntu"
  scratchRoot: "/shared/slurm-gha/scratch"
```

The launcher invokes a one-task step equivalent to:

```bash
srun --nodes=1 --ntasks=1 --cpus-per-task=2 --kill-on-bad-exit=1 --export=ALL \
  --vm-image=base-ubuntu \
  --vm-forward-env=SLURM_GHA_SERVICE_URL \
  --vm-forward-env=SLURM_GHA_LEASE_ID \
  --vm-forward-env=SLURM_GHA_CLASS \
  --vm-forward-env=SLURM_GHA_CLAIM_TOKEN \
  --vm-forward-env=SLURM_JOB_ID \
  /opt/slurm-gha/bootstrap.sh
```

If `service.tls.caFile` is configured, it also forwards `SLURM_GHA_CA_FILE=/etc/slurm-gha/callback-ca.crt`. Unlike Pyxis, VMoCS does not receive the host CA file through a dynamic bind mount; the guest image must contain that certificate at the fixed path.

The step receives `scaleSets[].slurm.cpusPerTask` explicitly so the VM gets the requested vCPU count rather than the nested `srun` default. The claim token remains in the Slurm job environment and out of the batch script and SSH command arguments. VMoCS 0.1.3 or newer is required because the launcher depends on repeatable `--vm-forward-env=NAME` support across the allocator and remote SPANK contexts. VMoCS limits a step to 16 selected variables; this integration currently uses five, or six with a private callback CA.

`mountHome` and `extraMounts` are Pyxis-only and configuration validation rejects them in `vmocs` mode. VMoCS template-level storage and device policy remain site-admin concerns.

### VM guest image contract

Prepare the VMoCS template with all of the following:

- A Linux guest whose architecture matches the installed Actions runner. The current repository image pins Linux x64 runner v2.337.0.
- The official runner distribution extracted at `/opt/actions-runner`, including an executable `bin/Runner.Listener` and `run.sh`.
- The matching repository [bootstrap script](../images/runner/bootstrap.sh) installed executable at `/opt/slurm-gha/bootstrap.sh`.
- `/bin/bash`, `/bin/sh`, `curl`, `git`, `ca-certificates`, `coreutils`, `getent`, `tar`, and the libraries installed by the runner's `bin/installdependencies.sh`.
- Directories `/runner` and `/runner-logs`, mode `0700`, owned by the VMoCS template's SSH user. The default `ubuntu` user cannot create these top-level directories on an ordinary image.
- Read and execute access to `/opt/actions-runner` and `/opt/slurm-gha/bootstrap.sh` for that SSH user.
- If the callback uses a private CA, the CA certificate at `/etc/slurm-gha/callback-ca.crt`, readable by the SSH user. Do not copy the service private key into the VM.
- Writable ephemeral disk space for a full runner copy and workflow workspace.
- DNS and outbound TCP/443 access to GitHub, plus HTTPS access to `service.publicUrl`.
- The SSH/cloud-init setup required by the VMoCS template so its attached command executes as the intended guest user.
- A system or service-user VMoCS template definition available to the Slurm identity on every eligible compute node; another user's private template is not sufficient.

Do not bake the GitHub App key, Slurm JWT, JIT encryption key, claim token, or JIT configuration into the image. The one-use claim token is delivered only for the allocation and unset before the runner starts.

The initial VMoCS backend retains `slurm.out` and `slurm.err` under `service.logRoot`. Runner `_diag` files copied to `/runner-logs` remain inside the disposable VM and are not retained after teardown. A future diagnostics export must use a per-allocation mechanism; a shared writable guest mount would risk cross-runner collisions.

### Test the prepared template

```bash
sudo -u github-ci /usr/local/bin/slurm-gha slurm-smoke \
  --mode vmocs \
  --base-url https://slurm-api.example.org \
  --user github-ci \
  --token-file /run/secrets/slurm-jwt \
  --partition vm \
  --account vm \
  --vm-image base-ubuntu \
  --cpus 2 \
  --memory-mib 2048 \
  --work-root /shared/slurm-gha-smoke \
  --require-runner
```

This verifies VM launch, repeatable selected-variable forwarding (a smoke nonce and `SLURM_JOB_ID`), guest DNS, GitHub TCP/443 egress, the runner distribution, and the bootstrap path. It does not claim a real GitHub runner. After it passes, configure a disposable scale set and run an end-to-end workflow.

## Native mode

The schema reserves native execution so its eventual configuration can remain stable:

```yaml
execution:
  mode: "native"
  runnerPath: "/opt/actions-runner"
  scratchRoot: "/shared/slurm-gha/scratch"
```

`validate-config` accepts this shape, but `doctor` and `serve` return `native execution mode is reserved but not implemented`. No workflow can currently execute directly on a compute-node host.
