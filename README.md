# actions-runner-slurm

`slurm-gha` provisions ephemeral GitHub Actions runner scale-set workers as Slurm allocations. GitHub matches jobs and the official `actions/runner` executes them; Slurm provides scheduling, resource enforcement, accounting, and Pyxis/Enroot container startup.

The implementation currently includes:

- strict `slurm-gha/v1alpha1` configuration and resource validation;
- the official `github.com/actions/scaleset` v0.4.0 listener and JIT client;
- a typed slurmrestd v0.0.42 adapter with rotating token-file reads and custom CA support;
- durable BoltDB leases, lifecycle transitions, ambiguous-submission adoption, and a single-process lock;
- one-use, hashed bootstrap credentials and AES-GCM-encrypted retryable JIT responses;
- a Pyxis launcher that disables home/entrypoint behavior and creates isolated runner directories;
- HTTPS claim, health, and readiness endpoints;
- `serve`, `validate-config`, `doctor`, `status`, `generate-key`, and `slurm-smoke` commands.

The first cluster contract was verified on October 4, 2026: a REST-submitted Slurm 24.11.5 allocation launched a squashfs image with Pyxis 0.21.0 / Enroot 4.0.1, reached `github.com:443`, exited zero, and released the allocation. See [the compatibility record](docs/compatibility.md).

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

## Runner image

The image definition in [images/runner/Dockerfile](images/runner/Dockerfile) packages official Actions runner v2.337.0 and [the bootstrap](images/runner/bootstrap.sh). The bootstrap copies that read-only distribution into a fresh writable directory, claims JIT over HTTPS, supplies it through `ACTIONS_RUNNER_INPUT_JITCONFIG`, then removes the claim material before starting the runner.

An image must be built and imported to a site-readable squashfs path before a real class is enabled. Docker-dependent Actions features are not supported by this first execution mode.

## Configuration

Start with [examples/config.yaml](examples/config.yaml). Each class has a fixed GitHub scale-set name and Slurm resource template. Unknown workflow labels do not create classes.

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

Pyxis execution is implemented. Native execution, metrics, drain administration, accounting fallback for fast-finished jobs, and the full failure/concurrency campaign remain subsequent milestones. The Slurm adapter deliberately uses the working batched `/jobs/` endpoint because this deployment's v0.0.42 single-job endpoint returned `Invalid JobID` for a live job.
