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
| GitHub scale-set client | `github.com/actions/scaleset` v0.4.0 |
| Actions runner | v2.337.0, Linux x64 SHA-256 `70920811a4f8ad4328818682bca5c6469c1c942fab52448868071d0063816613` |
| Ubuntu runner base | 24.04 index digest `sha256:534baea6a22c03a63003dbc8dbe78fe34bc0d7e595d9a9dc9834884ff530eb55` |
| Go | 1.27.1 |

The OpenAPI document advertised v0.0.42 submission, list, state, cancellation, and allocation paths. Typed submissions use explicit `job` fields; `memory_per_node` and `time_limit` use the v0.0.42 `{set,infinite,number}` wrappers.

The cluster's single-job endpoint returned `Invalid JobID` for a job that was simultaneously present in `/slurm/v0.0.42/jobs/`. The adapter therefore uses the working list endpoint for reconciliation and ownership verification. This is a measured deployment behavior, not assumed compatibility for other Slurm versions.

## Pyxis feasibility result

REST job 68034290 requested one node, one task, one CPU, 512 MiB, and five minutes in `defq`. Its host batch script invoked `srun` with:

- an existing squashfs image under `/cluster/images`;
- `--no-container-mount-home`;
- `--no-container-entrypoint`;
- one task.

Inside the container it printed the compute hostname and allocation ID, resolved `github.com`, and opened TCP port 443. Slurm accounting recorded `COMPLETED`, exit code `0:0`, elapsed time 16 seconds. No named smoke allocation remained in `squeue` afterward.

This verifies REST submission, Pyxis startup, compute DNS, and GitHub network egress.

## Runner image result

The pinned Dockerfile built successfully and was imported by Enroot as a 255 MiB squashfs with SHA-256 `a23416760d54231595cb34eea554ec314d536575fe3a4aa48b14f5eef2f0bd74`.

REST job 68034373 mounted a fresh writable `/runner`, copied the read-only distribution there, and executed `Runner.Listener --version`. It reported `2.337.0`, reached GitHub TCP/443, and completed with exit code `0:0` in 55 seconds. This also confirmed why the copy is required: an earlier diagnostic invocation directly from squashfs could not create `/opt/actions-runner/_diag`; the production bootstrap has always used the writable-copy path.

## GitHub control plane and callback result

GitHub App installation authentication to the `AMD-Alola` organization succeeded with the organization self-hosted-runners write permission. The `adc-slurm-runner` group did not exist, so it was created with selected-repository visibility and public repositories disabled. The service then created `slurm-cpu-small` as scale set ID 7 and established its message session.

The runner image was rebuilt with explicit private-CA callback support and imported as a 255 MiB squashfs with SHA-256 `dab1ced83ba73f2835afa8c8ca20758c591f59328254442071e0f91827a043b4`.

Slurm job 68034525 mounted the callback certificate read-only into that image, resolved the login-node service address from a compute node, verified HTTPS, and received a healthy response from `/healthz`. Accounting recorded `COMPLETED`, exit code `0:0`, elapsed time 20 seconds.

`AMD-Alola/adc-netbox-agent` was added as the group's sole selected repository. Push-triggered Actions run [37262346072](https://github.com/AMD-Alola/adc-netbox-agent/actions/runs/37262346072) requested `runs-on: [slurm-cpu-small]`. The listener submitted Slurm job 68034594, the compute allocation claimed its JIT configuration over the verified HTTPS callback, and ephemeral runner `slurm-c6151df4405b` executed repository commit `3413e1083447b69c7f7485478e9f0f57eac7dfd0`.

The job checked out the private repository and verified `SLURM_JOB_ID`, the GitHub Actions environment, and the writable runner distribution. GitHub reported success, the runner removed its credentials and registration, and Slurm accounting recorded `COMPLETED`, exit code `0:0`, with no allocation left in the queue. This completes the first real end-to-end workflow verification.
