FROM ubuntu:24.04@sha256:534baea6a22c03a63003dbc8dbe78fe34bc0d7e595d9a9dc9834884ff530eb55

ARG RUNNER_VERSION=2.337.0
ARG RUNNER_SHA256=70920811a4f8ad4328818682bca5c6469c1c942fab52448868071d0063816613

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates curl git \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /opt/actions-runner /opt/slurm-gha \
    && curl --fail --location --silent --show-error \
       --output /tmp/actions-runner.tar.gz \
       "https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-x64-${RUNNER_VERSION}.tar.gz" \
    && printf '%s  %s\n' "$RUNNER_SHA256" /tmp/actions-runner.tar.gz | sha256sum --check \
    && tar -xzf /tmp/actions-runner.tar.gz -C /opt/actions-runner \
    && rm -f /tmp/actions-runner.tar.gz \
    && /opt/actions-runner/bin/installdependencies.sh \
    && rm -rf /var/lib/apt/lists/*

COPY bootstrap.sh /opt/slurm-gha/bootstrap.sh
RUN chmod 0755 /opt/slurm-gha/bootstrap.sh

WORKDIR /runner
ENTRYPOINT ["/opt/slurm-gha/bootstrap.sh"]
