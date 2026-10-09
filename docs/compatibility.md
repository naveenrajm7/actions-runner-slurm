# Compatibility record

## Verified development cluster

Observed and tested October 4, 2026.

| Component | Observed contract |
| --- | --- |
| Slurm | 24.11.5 |
| slurmrestd data parser | `data_parser/v0.0.42` |
| REST authentication | `X-SLURM-USER-NAME` plus renewable JWT in `X-SLURM-USER-TOKEN` |
| Pyxis | 0.21.0 |
| Enroot | 4.0.1 |
| VMoCS | 0.1.3; repeatable selected-variable forwarding passes |
| GitHub scale-set client | `github.com/actions/scaleset` v0.4.0 |
| Actions runner | v2.337.0, Linux x64 SHA-256 `70920811a4f8ad4328818682bca5c6469c1c942fab52448868071d0063816613` |
| Ubuntu runner base | 24.04 index digest `sha256:534baea6a22c03a63003dbc8dbe78fe34bc0d7e595d9a9dc9834884ff530eb55` |
| Go | 1.27.1 |

The OpenAPI document advertised v0.0.42 submission, list, state, cancellation, and allocation paths. Typed submissions use explicit `job` fields; `memory_per_node` and `time_limit` use the v0.0.42 `{set,infinite,number}` wrappers.

The cluster's singular `/job/{id}` endpoint returned `Invalid JobID` for federation-encoded IDs above the 26-bit local `MAX_JOB_ID`, even while those jobs were present in `/slurm/v0.0.42/jobs/`. The adapter uses plural `GET /jobs/` for full-record reconciliation and ownership verification. Cancellation uses plural `DELETE /jobs/` with an explicit one-element `jobs` list and `SIGKILL`, then checks both envelope and per-job errors. These are measured v0.0.42 deployment behaviors, not assumed compatibility for other Slurm versions.

## Pyxis feasibility result

REST job 68034290 requested one node, one task, one CPU, 512 MiB, and five minutes in `defq`. Its host batch script invoked `srun` with:

- an existing squashfs image under `/cluster/images`;
- `--no-container-mount-home`;
- `--no-container-entrypoint`;
- one task.

Inside the container it printed the compute hostname and allocation ID, resolved `github.com`, and opened TCP port 443. Slurm accounting recorded `COMPLETED`, exit code `0:0`, elapsed time 16 seconds. No named smoke allocation remained in `squeue` afterward.

This verifies REST submission, Pyxis startup, compute DNS, and GitHub network egress.

## VMoCS environment-forwarding result

Observed October 8, 2026. A random 256-bit value was placed in `VMOCS_FORWARD_TEST` on the host, and the allocation ran:

```bash
srun -p vm -A vm -N1 -n1 \
  --vm-image=base-ubuntu \
  --vm-forward-env=VMOCS_FORWARD_TEST \
  /bin/sh -lc 'printf %s "$VMOCS_FORWARD_TEST" | sha256sum'
```

The host and guest SHA-256 values matched. This verifies selected-variable delivery into the attached guest command without placing the value in the command arguments. VMoCS printed a QMP broken-pipe warning during teardown after the guest command had completed; that warning remains worth monitoring but did not invalidate the forwarding result.

The Actions Runner Slurm VMoCS backend uses the same mechanism for several lease callback variables, so repeatability is part of the required contract. An initial 0.1.2 REST smoke exposed a failure where the guest received the last selected variable but not the preceding nonce. VMoCS 0.1.3 and its corrected SPANK plugin were then installed on the controller and all schedulable VM nodes.

A direct job forwarded two random values plus `SLURM_JOB_ID`; all three arrived in the guest and both host/guest SHA-256 pairs matched. A REST-submitted `slurm-gha slurm-smoke` then booted the VM without a node constraint, received the repeated environment values, saw the requested 2 vCPUs, validated runner v2.337.0 and the executable bootstrap, and reached GitHub. This closes the repeatable-forwarding blocker.

## VMoCS runner image result

Observed October 8, 2026 with template `actions-runner-ubuntu-24.04`; the qcow2 checksum matched its build manifest.

A direct VMoCS job used an isolated 300-second SSH timeout and completed successfully. Inside the VM, it verified the guest identity, one selected environment value, writable `/runner` and `/runner-logs`, the matching executable bootstrap, Actions runner `2.337.0`, GitHub DNS, and outbound TCP/443. This validates the prepared guest payload independently of the multi-variable integration.

The rebuilt image manifest records `callback_ca_included: true`. The certificate at `/etc/slurm-gha/callback-ca.crt` matched the callback service certificate used for the live claim.

The previous image exhausted the template's 120-second SSH timeout because `systemd-networkd-wait-online.service` waited for a stale build-time interface. The rebuilt image removed that conflict and reached VM readiness in about 23 seconds.

The smoke work root must be writable and shared between the service/login node and compute nodes. The tested site used a per-user directory on its shared parallel filesystem because its regional home and cluster-image mounts did not satisfy both requirements.

## VMoCS callback and runner-registration result

A one-run integration harness used the production controller, renderer, Slurm REST adapter, claim handler, encrypted JIT cache, and GitHub scale-set client. It reused an existing scale set and provisioned one disposable runner allocation.

The VM booted on an eligible compute node, verified the baked callback CA, authenticated with its one-use claim credential, and received a real JIT configuration. Runner v2.337.0 then printed `Connected to GitHub` and `Listening for Jobs`. This controlled registration test preceded the complete repository workflow below.

The original singular v0.0.42 cancellation path returned `Invalid JobID` while the test allocation was visibly running. Native `scancel` removed that allocation during diagnosis.

The federation-safe plural cancellation path was then ported from [`naveenrajm7/slurm-plugin` PR #32](https://github.com/naveenrajm7/slurm-plugin/pull/32). A rebuilt `slurm-gha` deliberately timed out a VMoCS smoke job, invoking its deferred REST cleanup. `DELETE /slurm/v0.0.42/jobs/` canceled the federated ID without native fallback, and `squeue` was empty. No test allocation or temporary Slurm token remained afterward.

## VMoCS end-to-end workflow result

A push-triggered workflow in a selected private repository requested `runs-on: [slurm-cpu-small]`. The VMoCS-backed listener reused the existing scale set and submitted one ephemeral runner allocation.

The prepared Ubuntu VM booted on an eligible compute node, claimed its JIT configuration, connected to GitHub, and executed the VMoCS validation job. GitHub reported success; the runner removed its credentials and registration and exited zero. The durable service state recorded `actionsResult: succeeded` and `state: terminal`. Slurm accounting recorded `COMPLETED`, exit code `0:0`, and no allocation remained in `squeue`. GitHub's organization runner list no longer contained the ephemeral runner after completion.

VMoCS emitted `QMP send failed: [Errno 32] Broken pipe` during teardown after the successful runner exit. It did not change the workflow or Slurm result but remains a teardown warning to monitor.

## Runner image result

The pinned Dockerfile built successfully and was imported by Enroot as a 255 MiB squashfs with SHA-256 `a23416760d54231595cb34eea554ec314d536575fe3a4aa48b14f5eef2f0bd74`.

REST job 68034373 mounted a fresh writable `/runner`, copied the read-only distribution there, and executed `Runner.Listener --version`. It reported `2.337.0`, reached GitHub TCP/443, and completed with exit code `0:0` in 55 seconds. This also confirmed why the copy is required: an earlier diagnostic invocation directly from squashfs could not create `/opt/actions-runner/_diag`; the production bootstrap has always used the writable-copy path.

## GitHub control plane and callback result

GitHub App installation authentication to the `AMD-Alola` organization succeeded with the organization self-hosted-runners write permission. The `adc-slurm-runner` group did not exist, so it was created with selected-repository visibility and public repositories disabled. The service then created `slurm-cpu-small` as scale set ID 7 and established its message session.

The runner image was rebuilt with explicit private-CA callback support and imported as a 255 MiB squashfs with SHA-256 `dab1ced83ba73f2835afa8c8ca20758c591f59328254442071e0f91827a043b4`.

Slurm job 68034525 mounted the callback certificate read-only into that image, resolved the login-node service address from a compute node, verified HTTPS, and received a healthy response from `/healthz`. Accounting recorded `COMPLETED`, exit code `0:0`, elapsed time 20 seconds.

`AMD-Alola/adc-netbox-agent` was added as the group's sole selected repository. Push-triggered Actions run [37262346072](https://github.com/AMD-Alola/adc-netbox-agent/actions/runs/37262346072) requested `runs-on: [slurm-cpu-small]`. The listener submitted Slurm job 68034594, the compute allocation claimed its JIT configuration over the verified HTTPS callback, and ephemeral runner `slurm-c6151df4405b` executed repository commit `3413e1083447b69c7f7485478e9f0f57eac7dfd0`.

The job checked out the private repository and verified `SLURM_JOB_ID`, the GitHub Actions environment, and the writable runner distribution. GitHub reported success, the runner removed its credentials and registration, and Slurm accounting recorded `COMPLETED`, exit code `0:0`, with no allocation left in the queue. This completes the first real end-to-end workflow verification.
