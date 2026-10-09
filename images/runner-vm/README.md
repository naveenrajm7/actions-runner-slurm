# VMoCS guest preparation

This directory documents the guest-side contract for `execution.mode: vmocs`. VMoCS owns the qcow2 image and template definition; Actions Runner Slurm expects these fixed paths inside the selected guest.

## Required layout

| Guest path | Requirement |
| --- | --- |
| `/opt/actions-runner` | Official runner distribution; `bin/Runner.Listener` and `run.sh` executable |
| `/opt/slurm-gha/bootstrap.sh` | The bootstrap from the matching Actions Runner Slurm revision, mode `0755` |
| `/runner` | Empty writable directory, mode `0700`, owned by the template SSH user |
| `/runner-logs` | Empty writable directory, mode `0700`, owned by the template SSH user |
| `/etc/slurm-gha/callback-ca.crt` | Public callback CA when `service.tls.caFile` is configured; omit for a publicly trusted certificate |

The current repository pins Actions runner v2.337.0 for Linux x64 with SHA-256 `70920811a4f8ad4328818682bca5c6469c1c942fab52448868071d0063816613`. Keep the VM and container versions aligned so both backends exercise the same runner behavior.

## Example Ubuntu provisioning

Run the equivalent of these commands while building the base image. Replace `ubuntu:ubuntu` with the VMoCS template's `ssh-user` identity.

```bash
sudo apt-get update
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y \
  bash ca-certificates coreutils curl git gzip libc-bin tar

curl --fail --location --silent --show-error \
  --output /tmp/actions-runner.tar.gz \
  https://github.com/actions/runner/releases/download/v2.337.0/actions-runner-linux-x64-2.337.0.tar.gz
printf '%s  %s\n' \
  70920811a4f8ad4328818682bca5c6469c1c942fab52448868071d0063816613 \
  /tmp/actions-runner.tar.gz | sha256sum --check

sudo install -d -m 0755 /opt/actions-runner /opt/slurm-gha
sudo tar -xzf /tmp/actions-runner.tar.gz -C /opt/actions-runner
sudo /opt/actions-runner/bin/installdependencies.sh
sudo install -m 0755 ./images/runner/bootstrap.sh /opt/slurm-gha/bootstrap.sh
sudo install -d -o ubuntu -g ubuntu -m 0700 /runner /runner-logs
rm -f /tmp/actions-runner.tar.gz
```

When the service callback uses a private CA, also install its public certificate:

```bash
sudo install -d -m 0755 /etc/slurm-gha
sudo install -m 0644 ./callback-ca.crt /etc/slurm-gha/callback-ca.crt
```

Never place the GitHub App private key, Slurm JWT, JIT encryption key, claim token, or JIT configuration in the guest image.

Publish the resulting template where the Slurm service identity can resolve it on every eligible compute node. A template that exists only in another user's `~/.vmocs/templates.yaml` is not sufficient.

For initial cold-boot validation, set the VMoCS template's `ssh-timeout` to at least 300 seconds. Reduce it only after measuring the published image on the target nodes. A production runner image should not spend time waiting for build-only interfaces. Remove stale Packer netplan files, retain one DHCP configuration that matches the runtime virtio NIC and marks it optional, run `netplan generate`, and mask `systemd-networkd-wait-online.service`. VMoCS already waits for SSH before it executes the runner command, so the wait-online boot barrier is redundant for this image.

## Preflight

From an attached VMoCS guest command running as the template user, all of these checks must pass:

```bash
test -x /opt/actions-runner/bin/Runner.Listener
test -x /opt/actions-runner/run.sh
test -x /opt/slurm-gha/bootstrap.sh
test -w /runner
test -w /runner-logs
getent hosts github.com
timeout 15 bash -c 'exec 3<>/dev/tcp/github.com/443'
```

Then run the repository's `slurm-gha slurm-smoke --mode vmocs --require-runner` command. It additionally proves that the installed SPANK plugin preserves repeated forwarding options by delivering both a nonce and `SLURM_JOB_ID` into the guest.
