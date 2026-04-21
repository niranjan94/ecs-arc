# ecs-arc-runner

Custom self-hosted GitHub Actions runner image, derived from
`ghcr.io/actions/actions-runner`, with a curated set of CLI utilities baked in
so that workflows launched as ECS tasks by
[ecs-arc](https://github.com/niranjan94/ecs-arc) do not have to reinstall
common tools on every job.

Published at: `ghcr.io/niranjan94/ecs-arc-runner`.

## Why this image exists

The upstream `ghcr.io/actions/actions-runner` image is intentionally minimal:
it contains only the .NET runtime and the runner binaries. This is a deliberate
design choice by GitHub (see
[`actions/runner#3080`](https://github.com/actions/runner/issues/3080), closed
as "working as intended"), predicated on the assumption that operators will
build a downstream image tailored to their workloads.

In practice that minimalism breaks common GitHub-published actions out of the
box:

- `actions/checkout` invokes `git` for anything beyond trivial clones
  (submodules, LFS, sparse checkout); the base image has no `git`.
- `actions/cache` prefers `zstd` and silently misses cache entries when the
  runner and the cache archive disagree on compression; the base image has no
  `zstd`.
- Numerous community actions shell out to `curl`, `wget`, `jq`, `unzip`,
  `gnupg`, `openssh`, etc. — all absent.

This image closes that gap.

## What is in the image

On top of `ghcr.io/actions/actions-runner:<version>`:

- **Must-haves:** `zstd`, `wget`, `curl`, `jq`.
- **Source control + transport:** `git`, `git-lfs`, `ca-certificates`, `gnupg`,
  `openssh-client`.
- **Archive handling:** `unzip`, `zip`, `xz-utils`, `bzip2`, `gzip`, `tar`.
- **Build tools:** `build-essential`.
- **Python:** `python3`, `python3-pip`, `python3-venv`.
- **General shell + networking:** `rsync`, `sudo`, `locales`, `tzdata`,
  `net-tools`, `dnsutils`, `iputils-ping`, `netcat-openbsd`, `file`, `less`,
  `vim-tiny`.
- **GitHub CLI:** `gh` from the official apt repository.
- **yq (mikefarah):** pinned binary from GitHub releases.
- Locale `en_US.UTF-8` generated, `LANG=C.UTF-8`.

What is intentionally **not** included: language toolchains beyond Python 3
(Node, Go, Ruby, JDK, …) and cloud CLIs. Use the corresponding `setup-*`
actions for those.

## How to pull

```bash
docker pull ghcr.io/niranjan94/ecs-arc-runner:latest
# or pin to an upstream runner version:
docker pull ghcr.io/niranjan94/ecs-arc-runner:2.330.0
```

Tags:

| Tag | Meaning |
|---|---|
| `:latest` | Most recent successful build (cron, main-push, or manual dispatch). |
| `:<runner_version>` | Specific upstream `actions/runner` version, e.g. `:2.330.0`. |
| `:tip` | In-progress Dockerfile on `main` — do not use in production. |

## Manual smoke test

```bash
docker run --rm --entrypoint bash ghcr.io/niranjan94/ecs-arc-runner:latest \
  -c "gh --version && yq --version && zstd --version && jq --version && git --version"
```

## Bumping the image

- **Utility list:** edit `runner/Dockerfile`, push to `main`. The workflow path
  filter triggers a rebuild; the `resolve` job fetches the latest upstream
  `actions/runner` release so you always get "current latest upstream +
  edited utilities".
- **`yq` version:** edit the `YQ_VERSION` `ARG` default in the Dockerfile and
  push. Same path trigger rebuilds.
- **Pinning to a specific upstream version on demand:** `workflow_dispatch`
  with `runner_version=2.329.0`. Publishes `:2.329.0` and moves `:latest` to
  it.

## Versioning

There is no independent semver for this image. Its version is the upstream
`actions/runner` version it was built from, as published on
[`actions/runner` releases](https://github.com/actions/runner/releases).

## Using your own image

This image is ecs-arc's default, but it is not mandatory. Any image compatible
with the upstream `ghcr.io/actions/actions-runner` entrypoint will work.

Point ecs-arc at a different image by setting `runner_image` on a `[[runner]]`
or `[[template]]` block (or `[defaults]`) in your TOML:

```toml
[defaults]
runner_image = "123456789012.dkr.ecr.us-east-1.amazonaws.com/my-runner:2025.04.21"

[[runner]]
family = "runner-gpu"
# …
# Or override per-runner:
# runner_image = "ghcr.io/my-org/my-runner:custom"
```

If you want to build a downstream of your own, the simplest path is to start
`FROM ghcr.io/niranjan94/ecs-arc-runner:<version>` (or
`ghcr.io/actions/actions-runner:<version>` if you want the strict minimum) and
layer your own tools on top. Preserve the upstream `ENTRYPOINT` / `CMD` and
end as `USER runner`, otherwise the Actions runner service will not launch
correctly.
