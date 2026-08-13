# CLAUDE.md - csi-wekafs

## Project Overview

Kubernetes CSI (Container Storage Interface) driver for WekaFS, a high-performance distributed filesystem. Supports native Weka protocol and NFS transport, snapshots, encryption, dynamic/static provisioning, and observability via Prometheus metrics and OpenTelemetry tracing.

**Current version**: 2.9.0
**Language**: Go 1.26
**Registry**: `quay.io/weka.io/csi-wekafs`
**GitHub**: `github.com/weka/csi-wekafs`

## Repository Structure

```
csi-wekafs/
├── cmd/
│   ├── wekafsplugin/            # Main CSI driver binary entry point
│   ├── metricsserver/           # Standalone metrics server binary (same code as --csimode=metricsserver)
│   └── wait-for-leader/         # Leader election gate utility
├── pkg/bootstrap/               # Process startup shared by the cmd/ binaries (logging, /metrics, tracing)
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
├── charts/csi-metricsserver/    # Helm chart for the standalone metrics server Deployment
├── charts/csi-wekafsplugin/     # Helm chart for K8s deployment
│   ├── Chart.yaml
│   ├── values.yaml              # 100+ configurable options
│   ├── values.schema.json
│   └── templates/               # Deployment, DaemonSet, RBAC, CSIDriver
├── dashboards/                  # Grafana dashboards (export format) + PrometheusRule alerts
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

## Coding Conventions

- Structured logging with Zerolog (use `log.Ctx(ctx)` for request-scoped loggers)
- Error types: transient vs non-transient in `apiclient/errors.go`
- Volume IDs encode filesystem/snapshot/path info - see `utilities.go`
- Mount operations have separate implementations: native Weka (`wekafsmount.go`) and NFS (`nfsmount.go`)
- Controller-side Kubernetes access goes through the controller-runtime manager: `manager.GetClient()` for cached PV reads (indexed by `spec.csi.volumeHandle`), `manager.GetAPIReader()` for Secrets, so no Secret informer is started
- Volume capacity metrics are published with `SetWithTimestamp` carrying the *measurement* time, not
  the scrape time, and quotas are cached for `quotaCacheValiditySeconds`. Prometheus (with
  `honorTimestamps`) drops repeats of the same timestamp, so a volume emits ~1 sample per cache
  period. Any dashboard or alert window over these metrics must exceed that cache validity
- Tests colocated with source files (`*_test.go`)

## Workflow Rules

- **Run `/simplify` after every code change** to check for reuse, quality, and efficiency issues
- **Keep CLAUDE.md up to date** when repo structure, conventions, or key patterns change
- **Keep README.md up to date** when user-facing behavior, configuration, or deployment instructions change

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
