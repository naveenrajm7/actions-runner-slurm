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

This verifies REST submission, Pyxis startup, compute DNS, and GitHub network egress. It does not yet verify the runner image, compute-to-service TLS trust, GitHub App authentication, scale-set registration, or a real Actions workflow.

## Runner image result

The pinned Dockerfile built successfully and was imported by Enroot as a 255 MiB squashfs with SHA-256 `a23416760d54231595cb34eea554ec314d536575fe3a4aa48b14f5eef2f0bd74`.

REST job 68034373 mounted a fresh writable `/runner`, copied the read-only distribution there, and executed `Runner.Listener --version`. It reported `2.337.0`, reached GitHub TCP/443, and completed with exit code `0:0` in 55 seconds. This also confirmed why the copy is required: an earlier diagnostic invocation directly from squashfs could not create `/opt/actions-runner/_diag`; the production bootstrap has always used the writable-copy path.

Compute-to-service TLS trust, GitHub App authentication, scale-set registration, JIT delivery, and a real Actions workflow remain to be verified with deployment credentials and a stable callback URL.
