# CLAUDE.md - csi-wekafs

## Project Overview

Kubernetes CSI (Container Storage Interface) driver for WekaFS, a high-performance distributed filesystem. Supports native Weka protocol and NFS transport, snapshots, encryption, dynamic/static provisioning, and observability via Prometheus metrics and OpenTelemetry tracing.

**Current version**: 2.9.4
**Language**: Go 1.26
**Registry**: `quay.io/weka.io/csi-wekafs`
**GitHub**: `github.com/weka/csi-wekafs`

## Repository Structure

```
csi-wekafs/
├── cmd/
│   ├── wekafsplugin/            # Main CSI driver binary entry point
│   └── wait-for-leader/         # Leader election gate utility
├── pkg/wekafs/                  # Core driver package
│   ├── apiclient/               # Weka REST API client (auth, filesystem, snapshot, NFS, quota, KMS)
│   ├── controllerserver.go      # CSI Controller (Create/Delete Volume, Snapshots, Expand)
│   ├── nodeserver.go            # CSI Node (Publish/Unpublish, Stage/Unstage)
│   ├── identityserver.go        # CSI Identity (plugin info, capabilities)
│   ├── wekafs.go                # Driver init, gRPC setup, health probes
│   ├── volume.go                # Volume abstraction, capacity, xattr metadata
│   ├── snapshot.go              # Snapshot operations & state
│   ├── volumehealth.go          # ControllerGetVolume: volume condition & capacity via REST API
│   ├── wekafsmount.go           # Native Weka mount operations
│   ├── nfsmount.go              # NFS fallback mount operations
│   ├── driverconfig.go          # Configuration management
│   ├── gc.go                    # Garbage collection for orphaned data
│   └── utilities.go             # Helpers (volume IDs, validation)
├── charts/csi-wekafsplugin/     # Helm chart for K8s deployment
│   ├── Chart.yaml
│   ├── values.yaml              # 100+ configurable options
│   ├── values.schema.json
│   └── templates/               # Deployment, DaemonSet, RBAC, CSIDriver
├── examples/                    # Usage examples (dynamic, static, snapshots, encryption)
├── tests/csi-sanity/            # CSI sanity test suite (docker-compose based)
├── .github/workflows/           # CI/CD (sanity tests, release, dev builds, PR lint)
├── docs/                        # Additional documentation
├── selinux/                     # SELinux policy & config
├── Dockerfile                   # Production multi-stage build (golang:1.26-alpine -> ubi9-minimal)
├── debug.Dockerfile             # Debug build with Delve
├── Makefile                     # Build targets (build, push, build-debug, deploy-debug)
├── go.mod / go.sum
├── README.md                    # Deployment guide, platform support, values reference
└── RELEASE.md                   # Version history & release notes
```

## Key Dependencies

- CSI spec v1.11.0, gRPC, Kubernetes client-go v0.34.1, controller-runtime v0.22.4
- Prometheus (metrics), OpenTelemetry (tracing), Zerolog (logging)
- k8s.io/mount-utils for mount operations

## Build & Test

```bash
make build                    # Docker image via buildx (multi-platform)
make push                     # Push to registry
make build-debug              # Debug image with Delve
make deploy-debug             # Build + push + deploy debug to cluster
go test ./pkg/wekafs/...      # Unit tests
go test ./pkg/wekafs/apiclient/... # API client tests
# CSI sanity tests run via docker-compose in tests/csi-sanity/
```

## Helm Chart

Deploy: `helm install csi-wekafsplugin charts/csi-wekafsplugin/`

Key components deployed:
- **Controller Deployment** with sidecars: provisioner, attacher, resizer, snapshotter, external-health-monitor
- **Node DaemonSet** with liveness probe sidecar
- RBAC roles, CSIDriver resource, optional SELinux policy

## Defaults must stay in sync

Some defaults are restated in more than one place, and nothing validates the copies
against each other, so they drift silently.

- `charts/csi-wekafsplugin/values.yaml` ↔ the values table in `README.md`. Currently
  paired: `dynamicProvisionPath`, `csiDriverVersion`, `controller.concurrency.*`,
  `controller.healthPort`, `controller.maxConcurrentRequests`.
- `charts/csi-wekafsplugin/values.yaml` ↔ the flag defaults in
  `cmd/wekafsplugin/main.go`. Currently paired: `endpoint`, `drivername`,
  `dynamic-path`, `metricsport`, `grpcrequesttimeoutseconds`.

Change one, change the other in the same commit.

`values.schema.json` carries no defaults by design — it types and validates only.
Do not add defaults to it.

## Coding Conventions

- Structured logging with Zerolog (use `log.Ctx(ctx)` for request-scoped loggers)
- Error types: transient vs non-transient in `apiclient/errors.go`
- Volume IDs encode filesystem/snapshot/path info - see `utilities.go`
- Mount operations have separate implementations: native Weka (`wekafsmount.go`) and NFS (`nfsmount.go`)
- Controller-side Kubernetes access goes through the controller-runtime manager: `manager.GetClient()` for cached PV reads (indexed by `spec.csi.volumeHandle`), `manager.GetAPIReader()` for Secrets, so no Secret informer is started
- Tests colocated with source files (`*_test.go`)

## Comments

Keep them minimal. Write a comment only when the code cannot be made obvious on its
own: a Weka API quirk, a CSI spec requirement, a mount or namespace constraint, a
deliberate omission, a workaround whose reason isn't visible in the diff. Do not
comment what the code already says.

The existing comments that carry information the reader cannot recover from the code
are the bar — `nodeserver.go` explaining why releasing the parent wekafs mount does
not break data access through the propagated bind mount, `constants.go` explaining
why `garbageCollectionTimeout` exists at all, `apistore_test.go` explaining that a
concurrent map read/write is a fatal Go runtime error rather than a benign race.

The test is what happens when the code is wrong. Comment what fails *silently*, at
runtime, on one platform only, or only under concurrency — those cost a cycle to
rediscover. Say nothing about syntax, types, or signature shape: the compiler,
`go vet` and the CSI sanity suite reject those instantly and loudly, so the comment
buys nothing even when it is accurate.

## Scope of a change

Fix what was asked, and only that. Unrelated problems you notice along the way stay
untouched — even obvious ones, even one-line ones. Mention them and offer to open a
ticket instead.

A change is in scope only if the requested fix does not work without it.

## Workflow Rules

- **Run `/simplify` once the change is complete and working** — after the change as a
  whole, not after each edit. It checks for reuse, quality, and efficiency issues.
- **Keep CLAUDE.md up to date** when repo structure, conventions, or key patterns change
- **Keep README.md up to date** when user-facing behavior, configuration, or deployment instructions change

## Commit messages

One subject line that stands on its own: conventional-commit type, then the
user-visible outcome in plain words. `.github/workflows/lint_pr.yaml` gates the PR
title on the same set — types `ci`, `chore`, `refactor`, `feat`, `fix`, `docs`,
`style`, `breaking`, `test`, with optional scopes `deps`, `ci`, `CSI-<n>`,
`WEKAPP-<n>`.

Existing history is the bar:

- `fix: install nfs-utils in the driver image so NFS transport can mount`
- `fix: keep a readonly attachment readonly when an override removes "ro"`
- `docs: explain sync_on_close, and correct the order mount options are applied in`

A body is allowed, and only for the non-obvious *why* — the failure mode, the
constraint, what was ruled out and why. That reasoning has nowhere else to live: PR
descriptions in this repo are short and written for release notes, not for reviewers.
No bullet summaries of the diff, no restating the subject in longer form.

## PR Descriptions

PR descriptions are **copied into the release notes as they are**, so write them for
whoever reads those — an operator or a customer, not the person who reviewed the diff.
See PR #409 for the house example.

Use exactly these headers, at this level and in this order:

```markdown
### TL;DR
### What changed?
### How to test?
### Why make this change?
```

- **Short.** #409's whole body is about 660 characters. That is the target.
- `### TL;DR` — one line: the user-visible outcome.
- `### What changed?` — a short bullet list.
- `### How to test?` — numbered steps someone can actually follow: deploy, create a PVC,
  check a value. If only automated tests can show it, say so plainly.
- `### Why make this change?` — one short paragraph on the problem it solves.
- Plain language, for a reader who does not know the codebase. Describe behaviour and
  impact, not implementation.
- That audience rule follows the change. A PR with no user-visible effect - a refactor,
  a CI or test-infrastructure change - has developers as its only readers, so write it
  for them and be as technical as it needs to be. Say plainly that nothing changes for
  someone running the driver, rather than inventing user impact to fill the section.
- Naming concrete user-facing identifiers is fine and encouraged — config keys, volume
  context parameters, metric names, CLI flags, role names, values. What stays out: file
  paths, line numbers, commit hashes, internal type names, and lock, goroutine or
  race-condition analysis.

**Anything long or technical belongs in a PR comment instead** — review responses,
finding-by-finding breakdowns, design trade-offs, verification evidence, notes on what
was deliberately left unfixed. Comments are not copied into release notes, so there is
no length limit there. When shortening an existing description, move the old text into a
comment rather than deleting it.

## Agentic Flow

The main agent is an orchestrator. It should delegate work via Task tool and minimize direct tool use. Direct tool use is acceptable only for 1-2 quick checks to orient. Model should always be set explicitly on tasks/subagents.

### Delegation Model (in order)

1. **Haiku task** — all codebase exploration, investigation, searching, and reading files. Even for complex debugging — haiku can read and trace code paths. It's 10-20x cheaper than opus.
2. **Sonnet task** — code edits, test runs, build verification, deploy flows.
3. **Opus task** — only for complex plan generation that requires deep understanding of the codebase. Use it only while having better initial context from a haiku task, or when sonnet is struggling with execution.
