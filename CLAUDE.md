# CLAUDE.md - csi-wekafs

## Project Overview

Kubernetes CSI (Container Storage Interface) driver for WekaFS, a high-performance distributed filesystem. Supports native Weka protocol and NFS transport, snapshots, encryption, dynamic/static provisioning, and observability via Prometheus metrics and OpenTelemetry tracing.

**Current version**: 2.8.3
**Language**: Go 1.24
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
├── Dockerfile                   # Production multi-stage build (golang:1.24-alpine -> ubi9-minimal)
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
- **Controller Deployment** with sidecars: provisioner, attacher, resizer, snapshotter
- **Node DaemonSet** with liveness probe sidecar
- RBAC roles, CSIDriver resource, optional SELinux policy

## Coding Conventions

- Structured logging with Zerolog (use `log.Ctx(ctx)` for request-scoped loggers)
- Error types: transient vs non-transient in `apiclient/errors.go`
- Volume IDs encode filesystem/snapshot/path info - see `utilities.go`
- Mount operations have separate implementations: native Weka (`wekafsmount.go`) and NFS (`nfsmount.go`)
- Tests colocated with source files (`*_test.go`)

## Workflow Rules

- **Run `/simplify` after every code change** to check for reuse, quality, and efficiency issues
- **Keep CLAUDE.md up to date** when repo structure, conventions, or key patterns change
- **Keep README.md up to date** when user-facing behavior, configuration, or deployment instructions change

## Agentic Flow

The main agent is an orchestrator. It should delegate work via Task tool and minimize direct tool use. Direct tool use is acceptable only for 1-2 quick checks to orient. Model should always be set explicitly on tasks/subagents.

### Delegation Model (in order)

1. **Haiku task** — all codebase exploration, investigation, searching, and reading files. Even for complex debugging — haiku can read and trace code paths. It's 10-20x cheaper than opus.
2. **Sonnet task** — code edits, test runs, build verification, deploy flows.
3. **Opus task** — only for complex plan generation that requires deep understanding of the codebase. Use it only while having better initial context from a haiku task, or when sonnet is struggling with execution.
