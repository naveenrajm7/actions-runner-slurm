# actions-runner-slurm

`slurm-gha` provisions ephemeral GitHub Actions runner scale-set workers as Slurm allocations. GitHub matches jobs and the official `actions/runner` executes them; Slurm provides scheduling, resource enforcement, and accounting. A runner class can launch in a Pyxis/Enroot container or a VMoCS virtual machine. Native host execution is reserved but not implemented.

The user documentation is published at [naveenrajm7.github.io/actions-runner-slurm](https://naveenrajm7.github.io/actions-runner-slurm/). Start with the [prerequisites](https://naveenrajm7.github.io/actions-runner-slurm/getting-started/prerequisites/) and the guided installation.

The implementation currently includes:

- strict `slurm-gha/v1alpha1` configuration and resource validation;
- the official `github.com/actions/scaleset` v0.4.0 listener and JIT client;
- a typed slurmrestd v0.0.42 adapter with rotating token-file reads and custom CA support;
- durable BoltDB leases, lifecycle transitions, ambiguous-submission adoption, and a single-process lock;
- one-use, hashed bootstrap credentials and AES-GCM-encrypted retryable JIT responses;
- explicit execution backends for Pyxis containers and VMoCS VMs, with native mode visibly reserved;
- HTTPS claim, health, and readiness endpoints;
- `serve`, `validate-config`, `doctor`, `status`, `generate-key`, and `slurm-smoke` commands.

The first cluster contract was verified on October 4, 2026: a REST-submitted Slurm 24.11.5 allocation launched a squashfs image with Pyxis 0.21.0 / Enroot 4.0.1, reached `github.com:443`, exited zero, and released the allocation. On October 8, VMoCS 0.1.3 launched the prepared Ubuntu VM, forwarded the complete callback environment, claimed a real GitHub JIT configuration over verified HTTPS, and executed a private-repository workflow successfully with runner v2.337.0. See [the compatibility record](docs/compatibility.md).

## Build and test

Go 1.25 or newer is required by `actions/scaleset`; this repository is developed with Go 1.27.1.

```bash
go test ./...
go build -trimpath -o bin/slurm-gha ./cmd/slurm-gha
```

Pull requests and pushes to `main` run formatting, module consistency, vet,
race-enabled tests, and a clean build in GitHub Actions.

## Release

Create and publish a GitHub Release with a semantic version tag such as
`v0.1.0`. The release workflow reruns the test workflow, builds static Linux
archives for amd64 and arm64, embeds the version and commit in the binary, and
attaches each archive and its SHA-256 checksum to the GitHub Release.

The release workflow accepts prerelease tags such as `v0.2.0-rc.1`. GitHub
automatically supplies the source archives in addition to the binary assets.

Validate a configuration before starting the service:

```bash
bin/slurm-gha validate-config --config /etc/slurm-gha/config.yaml
bin/slurm-gha doctor --config /etc/slurm-gha/config.yaml
```

Generate the 32-byte key used to protect cached JIT responses. The command refuses to overwrite an existing file:

```bash
bin/slurm-gha generate-key --output /run/secrets/slurm-gha-jit-key
```

Start the service:

```bash
bin/slurm-gha serve --config /etc/slurm-gha/config.yaml
```

The service reconciles existing owned allocations before opening GitHub listener sessions. Ordinary shutdown stops new work and preserves scale sets, state, and running allocations.

## Execution modes and runner images

Each scale set selects exactly one execution mode:

| Mode | Environment | Status |
| --- | --- | --- |
| `pyxis` | Pyxis/Enroot container from `execution.image` | Implemented and end-to-end verified |
| `vmocs` | VMoCS VM template from `execution.image` | Implemented and end-to-end workflow verified |
| `native` | Compute-node host | Reserved, not implemented |

See [execution modes](docs/execution-modes.md) for configuration and launch behavior, and [VMoCS guest preparation](images/runner-vm/README.md) for an actionable image checklist.

The image definition in [images/runner/Dockerfile](images/runner/Dockerfile) packages official Actions runner v2.337.0 and [the bootstrap](images/runner/bootstrap.sh) for Pyxis. A VMoCS guest must provide the same files at `/opt/actions-runner` and `/opt/slurm-gha/bootstrap.sh`, plus writable `/runner` and `/runner-logs` directories. The bootstrap copies the distribution into a fresh writable directory, claims JIT over HTTPS, supplies it through `ACTIONS_RUNNER_INPUT_JITCONFIG`, then removes the claim material before starting the runner.

A Pyxis image must be imported to a site-readable squashfs path. A VMoCS template must be available on eligible compute nodes and use VMoCS 0.1.3 or newer for repeatable selected-environment forwarding. Docker-dependent Actions features are not supplied by either mode.

## Configuration

Start with the [Pyxis example](examples/config.yaml) or [VMoCS example](examples/config-vmocs.yaml). Each class has a fixed GitHub scale-set name and Slurm resource template. Unknown workflow labels do not create classes.

```yaml
jobs:
  smoke:
    runs-on: slurm-cpu-small
    steps:
      - uses: actions/checkout@v4
      - run: hostname
```

Security and lifecycle details are in [docs/design.md](docs/design.md). The initial trust boundary is private, trusted repositories running under a dedicated site Unix identity.

## Current boundary

Pyxis and VMoCS are both end-to-end workflow verified. Native execution, metrics, drain administration, accounting fallback for fast-finished jobs, and the full failure/concurrency campaign remain subsequent milestones. Federated job IDs exceed Slurm's local `MAX_JOB_ID`, so the adapter uses plural v0.0.42 endpoints for observation and cancellation instead of the singular `/job/{id}` handler that rejects those IDs.
