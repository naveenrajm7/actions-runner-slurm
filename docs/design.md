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

JIT is returned with `Cache-Control: no-store`. A response cached for retry is encrypted with AES-256-GCM and bound to the lease ID. The bootstrap writes JIT only to a mode-0600 temporary file, then passes it through the runner-supported `ACTIONS_RUNNER_INPUT_JITCONFIG` environment input rather than a process argument. The disposable runner directory is removed after the allocation step; diagnostics are copied to the configured log directory.

## Trust boundary

Pyxis/Enroot provides disposable filesystems, not hostile multi-tenant isolation. Workflow code runs as the Slurm Unix identity and can access explicitly configured mounts and reachable cluster services. Initial use is restricted to trusted private repositories, runner-group policy, a dedicated service identity, no home mount, and minimal read-only extra mounts.

Docker container actions, job containers, and service containers are outside the initial boundary.
