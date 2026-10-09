# Design and security notes

## Ownership and lifecycle

Each provisioning attempt is recorded before submission. A random non-secret lease ID is written to Slurm's `comment` field as `slurm-gha/<lease-id>`. REST submission ambiguity is represented explicitly; reconciliation adopts exactly one allocation with the matching comment and refuses duplicate matches.

Active capacity includes intents, submissions, ambiguous submissions, pending allocations, starting runners, idle runners, busy runners, and completing runners. `maxPending` separately bounds unregistered capacity. Scale-down only cancels a Slurm job when both the stored job ID and correlation comment match and Slurm still reports it pending.

GitHub lifecycle and Slurm state are stored independently. A Slurm `COMPLETED` result does not assert that the Actions job passed.

## Worker claim

The provisioning service creates a random 256-bit claim credential and stores only its SHA-256 digest. Its expiry covers the configured queue and startup windows. The plaintext is delivered in the Slurm job environment, not the batch script; administrators and users who can inspect job environments may therefore see it. It authorizes only one lease and becomes useless after registration, terminal state, or expiry.

The worker POSTs its Slurm job ID to the HTTPS claim endpoint. Before generating JIT, the service checks:

1. the bearer credential digest and expiry;
2. the lease's recorded Slurm job ID;
3. the cluster-qualified job when available;
4. the Slurm correlation comment;
5. that the allocation is starting/running and has not restarted.

JIT is returned with `Cache-Control: no-store`. A response cached for retry is encrypted with AES-256-GCM and bound to the lease ID. The bootstrap writes JIT only to a mode-0600 temporary file, then passes it through the runner-supported `ACTIONS_RUNNER_INPUT_JITCONFIG` environment input rather than a process argument. The disposable runner directory is removed after the allocation step. Pyxis diagnostics are copied to the configured log directory. In the initial VMoCS backend, Slurm output is retained but guest `_diag` files remain on the disposable VM.

## Trust boundary

Pyxis/Enroot and VMoCS provide disposable environments, not a complete hostile multi-tenant boundary. Workflow code ultimately runs under the site policy attached to the Slurm identity, container mounts or VM template, assigned devices, and reachable cluster services. Initial use is restricted to trusted private repositories, runner-group policy, and a dedicated service identity.

The bootstrap callback always uses HTTPS. When `service.tls.caFile` is configured, the Pyxis launcher mounts that CA certificate read-only into the runner container. The VMoCS launcher instead points curl at `/etc/slurm-gha/callback-ca.crt`, which must be installed in the guest image. TLS verification is never disabled.

Docker container actions, job containers, and service containers are outside the initial boundary.
