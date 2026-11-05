---
title: A private repository builds, releases and deploys from a workstation
status: draft
depends_on:
  - 001-gate-principles.md
  - 033-what-a-release-reads-is-checked-on-push.md
affects:
  - internal/ship/
  - internal/config/
  - internal/contract/
  - cmd/lateregate/
  - docs/commands.md
  - docs/configuration.md
  - docs/releasing.md
  - CHANGELOG.md
effort: large
trigger: a repository whose push CI and tag releases do not run on GitHub Actions has no way to build, publish and deploy its release, and `release` waits for a CI that never reports
created: 2026-11-06
updated: 2026-11-06
author: changkun
dispatched_task_id: null
---

# A private repository builds, releases and deploys from a workstation

## The problem

A release here is a pipeline that runs on GitHub: a push workflow runs the
bar, a tag workflow builds the image, rolls it out and proves it live, and
`lateregate release` reads that CI before it tags. A repository whose runs
cannot happen on GitHub has none of it. It still needs the bar on the tree
it ships, an image built for the cluster's platform, a deploy that pins what
was built, and a live check, and it needs `release` to stop waiting for a
CI that will not report.

## The decision

**A `ship` block is the declaration.** `.lateregate.yaml` names the image
(registry path, Dockerfile, context, platform, build arguments, the argument
that receives the version, and `prepare` commands for a Dockerfile that
copies a prebuilt binary) and the deploy (the rollout identity's kubeconfig
as a 1Password reference, namespace, deployment, container, rollout timeout,
the live-check URL and script, and `preflight` and `after_roll` commands).
A repository without the block is unchanged.

**Four commands, each the whole of one step.** `build` runs the same bar a
release runs over the whole tree, then builds the image tagged
`dev-<short sha>` locally. `release` is the existing command. `push` builds
from a clean tree that sits exactly on a `vX.Y.Z` tag and publishes
`<tag>`, `sha-<short sha>` and, for a stable tag, `latest`. `deploy` takes
the newest stable tag on origin, or the one named.

**What deploys is the tag, not the checkout.** `deploy` unpacks the tag with
`git archive` into a temporary directory and runs `deploy/prod`, the live
check and the hooks from there. It resolves `<name>:<tag>` to a digest in
the registry, refuses when the image is absent, and sets the deployment to
`<name>@sha256:...`. Docker reads the digest with `buildx imagetools
inspect` and podman pulls the image and reads the digest it recorded, so
neither needs another tool.

**The rollout identity is a file that exists only for the deploy.** `op read
--out-file` writes the kubeconfig into a directory only its owner can enter,
the content never passes through this process, and the directory is removed
on every exit path including an interrupt. Every `kubectl` call passes
`--kubeconfig` and every step's environment sets `KUBECONFIG`, so the
operator's own context is never used. `deploy-credential` makes the
Document once: it builds the kubeconfig from the repository's
`rollout-identity.yaml` and the cluster, proves the identity with a
server-side dry run of `deploy/prod`, stores it, and reads it back to compare.

**The steps follow the shared pipeline.** Preflight, apply (`-k` when
`deploy/prod` holds a `kustomization.yaml`, `-f` otherwise), set image,
rollout status, `after_roll`, then the live check with `BASE_URL`, `TAG`,
`COMMIT` and `OUTPUT_MD` and no token. A missing live-check script is an
error. Any failure stops the deploy naming the step.

**The GitHub release is created from the changelog.** `release` creates it
after the tag is pushed and `deploy` creates it when it is missing, through
one idempotent helper. Failing to create it never undoes a tag or a deploy.

**The wiring no longer expects push CI.** `release` skips the CI read, and
`contract` and `init` stop asking for the shared workflow caller. Hooks, the
changelog, the identity block and the rest of the wiring are still checked.

Image signing and an SBOM are left out: the cluster enforces no signatures,
and signing a private image would publish a record of it to a public log.

## What was considered

- **A shell script per repository.** Rejected: the repositories would drift
  apart, and the deploy semantics are the shared pipeline's, which this
  keeps in one place.
- **Deploying from the working tree.** Rejected: a dirty or moved checkout
  would change what reaches the cluster. The tag is the only input.
- **Deploying by tag instead of digest.** Rejected: a tag can move between
  the check and the roll. The digest cannot.
- **Reading the kubeconfig from stdout.** Rejected: the secret would pass
  through this process and its buffers. The password manager writes the file.
- **Running the bar in `push`.** Rejected: the release that cut the tag ran
  it on this tree, and a second run costs the same minutes for no new fact.

## Acceptance criteria

- A `ship` block parses with defaults, restated defaults are reported, and a
  missing name, namespace, deployment, URL or kubeconfig, a kubeconfig that
  is not an `op://` reference, and a platform that is not Linux are refused.
- `release` of a ship repository reads no CI and creates the GitHub release;
  `contract` accepts it with no caller and `init` writes none.
- `build` runs the bar and then builds for the configured platform without
  pushing; `push` refuses off a tag and on a dirty tree, tags and pushes
  three names, and explains a refused login.
- `deploy` picks tags in version order, refuses a missing image, a missing
  live check and a missing `op`, passes `--kubeconfig` on every `kubectl`
  call, runs the steps in order, and leaves no kubeconfig behind after a
  success, a failed step or a signal.
- `deploy-credential` refuses an empty context, finds the account and its
  token Secret in the repository's file, passes `--context` on every call
  made with the administrator's kubeconfig, never prints or passes the
  token on a command line, and compares what it stored with what it reads.
