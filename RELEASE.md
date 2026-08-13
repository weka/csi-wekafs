# Release v2.9.2
## What's Changed

### Improvements
* feat: allow a separate priorityClassName for controller and node components by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/778 [(more details)](#pr-778)

### Bug Fixes
* fix(chart): use the configured logLevel for the csi-snapshotter sidecar by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/779 [(more details)](#pr-779)
* fix: give every container port a name unique within its pod by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/792 [(more details)](#pr-792)
* fix: stop the driver panicking when it cannot determine volume encryption by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/793 [(more details)](#pr-793)
* docs: show a Source Code link on the chart page by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/794 [(more details)](#pr-794)

---
<details>
<summary><b>PR Details</b></summary>

### <a name="pr-778"></a>PR #778 - feat: allow a separate priorityClassName for controller and node components
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/778

> ### TL;DR
> The controller and the node pods can now be given different priority classes.
> 
> ### What changed?
> - New chart values `controller.priorityClassName` and `node.priorityClassName`.
> - Either one overrides the existing global `priorityClassName` for its own pods. Left unset, both inherit it, so nothing changes for an existing installation.
> 
> ### How to test?
> 1. Install with `--set controller.priorityClassName=system-cluster-critical --set node.priorityClassName=system-node-critical`.
> 2. Confirm `kubectl get deploy,ds -n csi-wekafs -o custom-columns=NAME:.metadata.name,PC:.spec.template.spec.priorityClassName` shows the two different classes.
> 3. Install with only the global `priorityClassName` set and confirm both components still use it.
> 
> ### Why make this change?
> Requested in issue #691. A controller Deployment and a node DaemonSet have different scheduling requirements — the usual pairing is `system-cluster-critical` for the controller and `system-node-critical` for the node — and a single global value could only ever express one of them. Setting a per-component value replaces the global one rather than being combined with it, since a priority class is a single name.

### <a name="pr-779"></a>PR #779 - fix(chart): use the configured logLevel for the csi-snapshotter sidecar
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/779

> ### TL;DR
> `logLevel` now applies to the snapshotter sidecar too, instead of it always logging at level 5.
> 
> ### What changed?
> - The `csi-snapshotter` container's verbosity came from a value written into the template rather than from `logLevel`. It now follows `logLevel` like every other container.
> - No change at the default: the hardcoded value and the default are both 5.
> 
> ### How to test?
> 1. Install with `--set logLevel=2`.
> 2. Run `kubectl get deploy <release>-controller -o yaml` and confirm the `csi-snapshotter` container is started with `--v=2`.
> 3. Confirm its logs are correspondingly quieter.
> 
> ### Why make this change?
> Reported in issue #687. Turning `logLevel` down quieted every container except the snapshotter, which kept logging at 5 — and on a busy controller that was most of the remaining log volume. While fixing it we checked every other container in both charts; this was the only one whose log level was not configurable.

### <a name="pr-792"></a>PR #792 - fix: give every container port a name unique within its pod
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/792

> ### TL;DR
> 
> Fixes duplicate container port names in the node and controller pods, which could send metrics scrapes to the wrong sidecar.
> 
> ### What changed?
> 
> - In the node DaemonSet, the two ports both named `healthz` are now `ns-healthz` (9899, node driver) and `reg-healthz` (9809, registrar).
> - In the controller Deployment, the attacher metrics port is renamed from `pr-metrics` to `at-metrics`. The provisioner keeps `pr-metrics`.
> - The liveness probes that referenced these ports by name were updated to match.
> 
> No ports, values or defaults change — only names.
> 
> ### How to test?
> 
> 1. Install or upgrade the chart with `metrics.enabled=true`.
> 2. Run `kubectl get pod <node-pod> -o jsonpath='{.spec.containers[*].ports[*].name}'` and confirm `ns-healthz` and `reg-healthz` appear instead of `healthz` twice.
> 3. Do the same for a controller pod and confirm `at-metrics` and `pr-metrics` are distinct.
> 4. Confirm both pods stay healthy — the liveness probes resolve the renamed ports.
> 
> ### Why make this change?
> 
> A port name must be unique across a whole pod, not just within one container. Two pods reused a name, so anything resolving a port by name — a Service `targetPort`, a `PodMonitor` port selector — would get whichever one the cluster happened to keep. Metrics could be scraped from the wrong sidecar, or missed. Nothing in the chart selects by these names today, which is why it went unnoticed, but it makes the ports unsafe to reference.
> 
> Original fix by @assafgi in #682, reworked with the shorter names already used in the 3.0 branch so a second set of names does not follow behind it.

### <a name="pr-793"></a>PR #793 - fix: stop the driver panicking when it cannot determine volume encryption
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/793

> ### TL;DR
> 
> Fixes a crash: the driver could panic when checking whether a volume is encrypted, taking the process down.
> 
> ### What changed?
> 
> - Checking encryption on a filesystem-backed volume with no API credentials bound now returns a clear error instead of crashing.
> - The error says the encryption state could not be determined, and includes the underlying reason when there is one.
> - Volume creation that needs this answer fails with an `Internal` error rather than continuing.
> 
> ### How to test?
> 
> 1. Create a volume backed by a whole filesystem whose storage class has no API secret attached.
> 2. Perform an operation that touches encryption on it — for example creating a volume from it.
> 3. Confirm the driver reports an error and keeps running, rather than the controller pod restarting.
> 
> Automated coverage is included: the new test reproduces the crash against the unfixed code.
> 
> ### Why make this change?
> 
> The check ended by reading a value that was not always set. For a filesystem-backed volume with no API client, the driver has no way to ask the cluster whether the volume is encrypted, so nothing filled that value in and reading it crashed the process.
> 
> Reporting an error is the safe answer rather than assuming "not encrypted". Callers use this to decide whether to apply encryption, so a volume whose state could not be read would otherwise be treated as one that had been read and found unencrypted.
> 
> Found by @kristina-solovyova in #692.

### <a name="pr-794"></a>PR #794 - docs: show a Source Code link on the chart page
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/794

> ### TL;DR
> 
> The chart page now shows a **Source Code** link, pointing at the tag the chart was built from.
> 
> ### What changed?
> 
> - The chart README and the ArtifactHub listing render a `## Source Code` section, taken from the `sources` entry already in `Chart.yaml`.
> - Fixed the source URL recorded by PR-built charts, which contained the literal text `$CHART_VERSION` instead of the version number. Released charts were not affected.
> 
> ### How to test?
> 
> 1. Open `charts/csi-wekafsplugin/README.md` and confirm a **Source Code** section appears under **Maintainers**, with a link ending in the chart version.
> 2. On a PR build, run `helm show chart <chart>` and confirm the `sources` URL contains a version number rather than `$CHART_VERSION`.
> 
> ### Why make this change?
> 
> `Chart.yaml` has always recorded where the chart comes from, but nothing displayed it, so the published chart page gave no link back to the source at that version. Rendering it from `Chart.yaml` means it follows the value the release process already maintains, rather than becoming another thing to update by hand.
> 
> Both sections were originally raised by @ari in #582.

</details>
# Release v2.9.1
## What's Changed

### Bug Fixes
* fix: reject malformed API endpoints and secrets instead of failing later by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/758 [(more details)](#pr-758)

### Documentation
* fix(ci): point helm-docs at the root README template by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/754 [(more details)](#pr-754)

### Miscellaneous
* ci: draft release notes on main only by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/757 [(more details)](#pr-757)
* chore(deps): update to Go 1.26 and current external libraries by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/759 [(more details)](#pr-759)
* chore(deps): update the UBI base image by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/760 [(more details)](#pr-760)
* chore(deps): update the CSI sidecars to their current releases by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/761 [(more details)](#pr-761)
* ci: add make update-sidecars and repair the Renovate config by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/762 [(more details)](#pr-762)
* ci: run the Go unit tests on every pull request by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/763 [(more details)](#pr-763)

---
## PR Details

### <a name="pr-754"></a>PR #754 - fix(ci): point helm-docs at the root README template
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/754

> ### TL;DR
> Restores the project README, which the v2.9.0 release overwrote with generated boilerplate.
> 
> ### What changed?
> - Restored the README sections the release removed: pre-requisites, deployment, usage, volume health monitoring, additional documentation and build instructions
> - Corrected the documentation-generator template path in the release, pull-request and push-dev workflows, so this cannot happen again
> 
> ### How to test?
> 1. Open the README on this branch and confirm every section is present, with the 2.9.0 version badges and value table.
> 2. On the next release, confirm the README changes only where the version and values changed.
> 
> ### Why make this change?
> The documentation generator was pointed at a template path that does not exist in this repository. Rather than failing, it fell back to its own default template and wrote that over the README, deleting 54 lines of hand-written documentation. The v2.9.0 release was the first to run this workflow to completion, so this is the first time it happened. Nothing was permanently lost — the source template still held every section. Nothing changes for anyone running the driver.

### <a name="pr-757"></a>PR #757 - ci: draft release notes on main only
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/757

> ### TL;DR
> Release note drafts are now produced for the release branch only, instead of once per merged change.
> 
> ### What changed?
> - A draft release is generated when something lands on `main`, and no longer when something lands on `dev`
> - Drafting any branch on demand still works, by running the workflow manually and naming the branch
> 
> ### How to test?
> 1. Merge a pull request into `dev` and confirm no draft release appears.
> 2. Merge `dev` into `main` and confirm a draft release is created.
> 3. Run the draft workflow manually against any branch and confirm it still produces a draft.
> 
> ### Why make this change?
> Development lands on `dev` continuously and is merged into `main` only when there is enough for a release. Drafting on every push to `dev` would create a draft release per merged pull request, burying the one that matters. Nothing changes for anyone running the driver.

### <a name="pr-758"></a>PR #758 - fix: reject malformed API endpoints and secrets instead of failing later
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/758

> ### TL;DR
> A malformed or incomplete API secret is now rejected with a message naming the problem, instead of failing later with a generic error.
> 
> ### What changed?
> - An endpoint that includes a URL scheme, or that is not a valid address, is rejected rather than quietly skipped
> - If no endpoint in a secret is usable, creating the API client fails immediately instead of producing a client with nothing to talk to
> - `username`, `password` and `organization` are now required in the secret, and a missing one is named in the error
> - Refreshing the endpoint list from the cluster will no longer replace a working set with an empty one
> 
> ### How to test?
> 1. Create an API secret whose `endpoints` value includes a scheme, for example `https://1.2.3.4:14000`.
> 2. Create a PVC using it, and confirm provisioning fails with an error naming the endpoint rather than a generic "no endpoints" message.
> 3. Repeat with `username` omitted from the secret and confirm the error names the missing key.
> 
> ### Why make this change?
> Endpoints that failed validation were skipped one by one, so a secret where every entry was malformed still produced an API client — just one with an empty endpoint list, which then failed at the first request, far from the secret that caused it. Missing credentials behaved the same way, defaulting to empty and surfacing later as an authentication failure. The error now arrives when the volume is created, and says which part of the secret is wrong.
> 
> 
> This is a rework of the fixes originally made in #615 and #617, reimplemented against the endpoint
> handling as it stands now, which was rewritten in the meantime. Neither of those pull requests
> recorded a ticket.

### <a name="pr-759"></a>PR #759 - chore(deps): update to Go 1.26 and current external libraries
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/759

> ### TL;DR
> Updates the driver to Go 1.26 and refreshes every third-party library it depends on.
> 
> ### What changed?
> - Built with Go 1.26, in both the driver image and the test harness image
> - Kubernetes client libraries, controller-runtime, gRPC, Prometheus and OpenTelemetry all moved to their current releases
> - One Kubernetes library was a version behind the others and now matches them
> 
> ### How to test?
> 1. Deploy the chart and confirm the driver starts and reports ready.
> 2. Create a PVC, confirm it binds, then delete it.
> 3. Take a snapshot and restore from it.
> 
> ### Why make this change?
> Routine currency: newer releases carry security and bug fixes, and staying close to upstream keeps each future update small. The CSI specification itself is deliberately not updated here — the newest version removes an interface the volume health reporting depends on, which is a decision of its own rather than a dependency bump.

### <a name="pr-760"></a>PR #760 - chore(deps): update the UBI base image
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/760

> ### TL;DR
> Rebuilds the driver images on the current Red Hat base image.
> 
> ### What changed?
> - The UBI 9 minimal base image moves to its latest build, in both the released image and the one used by CI
> 
> ### How to test?
> 1. Pull the resulting image and confirm the driver runs.
> 2. Confirm the base image build number in the image labels matches the one in the Dockerfile.
> 
> ### Why make this change?
> The pinned base image had fallen behind the current build, so images were being produced on an older base carrying older system packages. Both Dockerfiles pin it separately and have to move together, or the image CI tests differs from the image that ships.

### <a name="pr-761"></a>PR #761 - chore(deps): update the CSI sidecars to their current releases
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/761

> ### TL;DR
> Updates the Kubernetes CSI sidecar containers deployed alongside the driver.
> 
> ### What changed?
> - liveness probe, attacher, provisioner, node driver registrar, resizer, snapshotter and external health monitor all move to their current releases
> - No configuration change is required: every option the chart passes still exists, and no new permissions are needed
> 
> ### How to test?
> 1. Upgrade the chart and confirm all controller and node pods reach Running.
> 2. Create a PVC, expand it, snapshot it, and delete it.
> 3. Confirm no sidecar container restarts.
> 
> ### Why make this change?
> Routine currency, and these had drifted further behind than intended. Two things worth watching after upgrading: the provisioner now runs a periodic clean-up pass over snapshots in the cluster, which is new steady-state activity; and the external health monitor is at the last release that supports the volume health interface the driver implements today.

### <a name="pr-762"></a>PR #762 - ci: add make update-sidecars and repair the Renovate config
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/762

> ### TL;DR
> Adds a command that reports which CSI sidecars are out of date, and repairs the automation that was supposed to be doing it.
> 
> ### What changed?
> - `make update-sidecars` reports which sidecars are behind their latest release; `make update-sidecars APPLY=1` updates the chart
> - Fixed the automatic dependency configuration, which pointed at a directory that no longer exists and so had stopped updating anything
> - Added the two sidecars that were never covered by it
> 
> ### How to test?
> 1. Run `make update-sidecars` and confirm it reports every sidecar as current.
> 2. Edit one sidecar version in the chart to an older release and run it again; confirm it reports that one as behind and exits non-zero.
> 
> ### Why make this change?
> The sidecars had fallen several releases behind with nothing flagging it. The cause was that the automatic updater was watching a path left over from before the chart moved, so it silently matched no files. This repairs that and adds a check that does not depend on that configuration being correct.

### <a name="pr-763"></a>PR #763 - ci: run the Go unit tests on every pull request
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/763

> ### TL;DR
> The Go unit tests now run automatically on every pull request.
> 
> ### What changed?
> - A new job runs the unit tests and the Go static checks on each pull request
> - It starts at the same time as the build rather than waiting for it, so a test failure is reported early
> - Tests run with the race detector enabled
> 
> ### How to test?
> 1. Open a pull request and confirm a `test-go` check appears alongside the build.
> 2. Push a commit that breaks a unit test and confirm the check fails.
> 
> ### Why make this change?
> Only the end-to-end storage tests ran automatically; the unit tests covering volume identifiers, the WEKA API client, quota handling and mount reference counting were run by hand, so a broken one could reach the branch unnoticed. The race detector matters here because most of those tests guard concurrent access, and without it a data race passes silently. The tests need no WEKA cluster and take about ninety seconds. Nothing changes for anyone running the driver.
# Release v2.9.0
## What's Changed

### New Features
* feat: implement external volume health monitoring via WEKA REST API by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/708 [(more details)](#pr-708)

### Improvements
* feat: add TTL-based filesystem name cache to API client by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/717 [(more details)](#pr-717)
* feat: add paginated API response support for quota fetching by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/715 [(more details)](#pr-715)

### Bug Fixes
* fix: accept the TenantAdmin API role for CSI operations by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/751 [(more details)](#pr-751)
* fix: correct NFS sync/async option translation in AsNfs mount option conversion by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/719 [(more details)](#pr-719)
* fix: make ApiClient safe for concurrent use, and fix what review found by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/718 [(more details)](#pr-718)
* fix: invalidate filesystem cache before deletion to prevent stale UID snapshot listing by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/714 [(more details)](#pr-714)
* fix: default semaphore weight to 1 for ops absent from maxConcurrencyPerOp by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/713 [(more details)](#pr-713)
* fix: replace Mutex with RWMutex in ApiStore to prevent concurrent map access races by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/712 [(more details)](#pr-712)
* fix: optimize gc resource consumption and support tenants with same filesystem name  by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/710 [(more details)](#pr-710)

### Documentation
* ci: adopt the v2 workflows and the new sanity harness by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/750 [(more details)](#pr-750)

### Miscellaneous
* fix(ci): write artifacthub changes as a string, and stop committing scratch by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/753 [(more details)](#pr-753)
* fix(ci): draft the release notes for the right branch, bounded by the last tag by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/752 [(more details)](#pr-752)

---
## PR Details

### <a name="pr-753"></a>PR #753 - fix(ci): write artifacthub changes as a string, and stop committing scratch
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/753

> ### TL;DR
> Fixes the release job, which produced an unloadable Helm chart and so never published v2.9.0.
> 
> ### What changed?
> - The `artifacthub.io/changes` chart annotation is written as text, and left out entirely when there is nothing to record
> - The release commit now includes only the files the release rewrites, instead of everything left lying in the build workspace
> - Removed a stray build scratch file that had been committed to the repository
> 
> ### How to test?
> 1. Run the release workflow.
> 2. Confirm it completes, and that a version tag, a GitHub release and a published Helm chart all appear.
> 3. Run `helm show chart charts/csi-wekafsplugin` and confirm it loads.
> 
> ### Why make this change?
> The release job wrote a list where Helm requires text, which made `Chart.yaml` impossible to load. Chart publishing failed on it, and that single failure also cost the version tag and the GitHub release, so v2.9.0 never went out even though the release commit had already landed. Nothing changes for anyone running the driver.

### <a name="pr-752"></a>PR #752 - fix(ci): draft the release notes for the right branch, bounded by the last tag
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/752

> ### TL;DR
> Release note drafts now cover only the release being cut, instead of repeating work that already shipped.
> 
> ### What changed?
> - The draft job lists only pull requests merged since the newest tag reachable from the branch being released
> - Removed a 30-item cap that silently dropped the oldest pull requests of a cycle
> - The release branch now defaults to `main`, and a push drafts notes for the branch that was pushed
> 
> ### How to test?
> 1. Run the `draft-v2` workflow against `main`.
> 2. Open the draft release it creates.
> 3. Confirm it lists only pull requests merged after the previous release tag — for `main` today, that is 15.
> 
> ### Why make this change?
> The job asked for merged pull requests with no cut-off date at all, so it returned whichever 30 had merged most recently regardless of the release they belonged to. The v2.9.0 draft consequently repeated work released in 2.8.4 through 2.8.9, and would have started losing genuine 2.9.0 entries off the bottom once more than 30 pull requests merged in the cycle. Nothing changes for anyone running the driver.

### <a name="pr-751"></a>PR #751 - fix: accept the TenantAdmin API role for CSI operations
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/751

> ### TL;DR
> Fixed a bug that made the driver unusable for customers whose storage API user has the TenantAdmin role.
> 
> ### What changed?
> - The driver now accepts the `TenantAdmin` role for its storage API user, alongside the already-supported `CSI`, `ClusterAdmin`, and `OrgAdmin` roles
> 
> ### How to test?
> 1. Configure the driver's storage API credentials with a user that has the `TenantAdmin` role
> 2. Create a PVC
> 3. Confirm the volume provisions successfully instead of failing with a permissions error
> 
> ### Why make this change?
> TenantAdmin grants full administrative control within its organization — the same scope OrgAdmin has — but it was missing from the driver's accepted-role list, blocking those customers entirely.

### <a name="pr-750"></a>PR #750 - ci: adopt the v2 workflows and the new sanity harness
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/750

> ### TL;DR
> No user-visible change — a faster internal build and test pipeline for developers.
> 
> ### What changed?
> - Adopted a faster continuous integration pipeline for building and testing the driver
> - Fixed a gap where automated storage tests never ran on pull requests opened as drafts
> 
> ### How to test?
> 1. Not applicable to users of the driver
> 2. Nothing changes in how the driver runs, is built, or is deployed
> 
> ### Why make this change?
> The previous pipeline was slow, and a trigger gap meant draft pull requests silently skipped storage test coverage, letting issues slip through unnoticed.

### <a name="pr-719"></a>PR #719 - fix: correct NFS sync/async option translation in AsNfs mount option conversion
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/719

> ### TL;DR
> Fixed an NFS mount bug that could apply the opposite write-caching behavior to what was requested.
> 
> ### What changed?
> - Corrected the `coherent` and `force_direct` NFS mount options so they now correctly set synchronous ("sync") writes instead of buffered ("async") writes
> 
> ### How to test?
> 1. Mount a volume over NFS using the `coherent` or `force_direct` mount option
> 2. Check the effective mount options on the client (for example, via `mount` or `/proc/mounts`)
> 3. Confirm `sync` is applied instead of `async`
> 
> ### Why make this change?
> The incorrect translation caused writes to be buffered instead of written through immediately, silently weakening the durability guarantee these options are meant to provide.

### <a name="pr-718"></a>PR #718 - fix: make ApiClient safe for concurrent use, and fix what review found
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/718

> ### TL;DR
> Fixed bugs that could crash the driver, or make it misjudge storage cluster features, when many volume operations ran at once.
> 
> ### What changed?
> - Made the driver's storage API client safe for many volume operations running at once
> - Fixed a partly-failed login being treated as successful, which left the driver with wrong assumptions about cluster capabilities for up to an hour (refusing valid volumes, skipping quota enforcement, or rejecting valid organizations)
> 
> ### How to test?
> Only reliably shown by automated tests, since the failures depend on timing under concurrent load:
> 1. Run the automated concurrency test suite added in this change
> 2. It reproduces the crashes and wrong behavior on the old code, and passes cleanly after the fix
> 
> ### Why make this change?
> Under concurrent load — normal in production, since Kubernetes creates and deletes many volumes in parallel — the driver could crash or silently run on wrong assumptions, causing hard-to-diagnose failures.

### <a name="pr-717"></a>PR #717 - feat: add TTL-based filesystem name cache to API client
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/717

> ### TL;DR
> Filesystem lookups are now cached briefly, cutting repeated calls to the storage system.
> 
> ### What changed?
> - Filesystem lookups reuse a recent result for a short time instead of querying the storage system every time.
> - Operations that need guaranteed fresh data are unaffected and always fetch live data.
> 
> ### How to test?
> 1. This is an internal performance optimization, verified by automated tests included in this change.
> 2. Operators may notice fewer repeated filesystem lookup calls to the storage cluster when the same filesystems are used repeatedly.
> 
> ### Why make this change?
> Workloads that resolve many filesystems in quick succession were generating a burst of repeated, identical requests to the storage cluster.

### <a name="pr-715"></a>PR #715 - feat: add paginated API response support for quota fetching
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/715

> ### TL;DR
> Quota lists are no longer silently truncated when a filesystem has more quotas than fit in one page of results.
> 
> ### What changed?
> - The API client now follows the storage system's pagination and combines all pages into the full result.
> - Hardened handling for older clusters without pagination support, and for empty or malformed pages.
> 
> ### How to test?
> 1. Query quotas on a filesystem with more quota entries than fit in a single page.
> 2. Verify the full list is returned, not just the first page.
> 3. Also covered extensively by automated tests.
> 
> ### Why make this change?
> The client wasn't following the "next page" pointer the storage system returns for long lists, so any sufficiently long list - most noticeably quotas - was silently incomplete.

### <a name="pr-714"></a>PR #714 - fix: invalidate filesystem cache before deletion to prevent stale UID snapshot listing
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/714

> ### TL;DR
> Deleting a filesystem could use stale cached data, letting deletion proceed even if the filesystem still had snapshots.
> 
> ### What changed?
> - The snapshot check performed before deleting a filesystem now always reads current data instead of a cached copy.
> - The cached record for a filesystem is cleared immediately once its deletion begins.
> 
> ### How to test?
> 1. This depends on an internal timing window that isn't practical to reproduce manually.
> 2. Covered by automated tests added in this change.
> 
> ### Why make this change?
> A cache used to avoid extra lookups wasn't cleared at the right time, so a delete could occasionally act on outdated filesystem information.

### <a name="pr-713"></a>PR #713 - fix: default semaphore weight to 1 for ops absent from maxConcurrencyPerOp
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/713

> ### TL;DR
> Operations without an explicit concurrency limit were being blocked entirely instead of allowed to run.
> 
> ### What changed?
> - Operations missing a configured concurrency limit now default to allowing 1 at a time, instead of 0.
> - Operations explicitly configured with a limit of 0 still remain blocked, as intended.
> 
> ### How to test?
> 1. Deploy the CSI driver.
> 2. Trigger an operation that has no explicit concurrency limit configured.
> 3. Verify it completes normally instead of hanging until it times out.
> 
> ### Why make this change?
> An operation without a configured limit defaulted to a concurrency of zero, which can never be acquired, so it would hang until its request timed out rather than running or failing clearly.

### <a name="pr-712"></a>PR #712 - fix: replace Mutex with RWMutex in ApiStore to prevent concurrent map access races
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/712

> ### TL;DR
> Fixes a bug that could crash the CSI controller under concurrent use
> 
> ### What changed?
> - Fixed unsafe concurrent access to the driver's internal cache of Weka cluster connections
> - Prevents a crash when many requests need cluster credentials at the same time
> 
> ### How to test?
> 1. Covered by automated concurrency tests included in this change
> 2. Before the fix, the symptom was occasional controller pod restarts under heavy simultaneous volume operations; this should no longer occur
> 
> ### Why make this change?
> The controller could crash outright when multiple requests needing storage-cluster credentials were handled at the same time — more likely at scale or with many simultaneous volume operations.

### <a name="pr-710"></a>PR #710 - fix: optimize gc resource consumption and support tenants with same filesystem name 
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/710

> ### TL;DR
> Fixes deleted-volume cleanup colliding across tenants sharing a filesystem name
> 
> ### What changed?
> - Cleanup of deleted volumes is now tracked per tenant, not just by filesystem name
> - Fixes trash left behind indefinitely when two tenants both use a name like `default`
> - Upgraded the deletion tool, lowering cleanup CPU/time cost
> 
> ### How to test?
> 1. Create identically-named filesystems under two different tenants
> 2. Provision and delete volumes on each
> 3. Confirm both tenants' deleted data is fully cleaned up, without one blocking the other
> 
> ### Why make this change?
> A tenant's deleted volume data could previously be left behind indefinitely if another tenant used the same filesystem name — a real risk in shared, multi-tenant clusters.

### <a name="pr-708"></a>PR #708 - feat: implement external volume health monitoring via WEKA REST API
by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/708

> ### TL;DR
> Kubernetes can now detect and report when a volume's storage becomes unhealthy
> 
> ### What changed?
> - Periodic health checks for each volume against the Weka cluster
> - Unhealthy volumes surface as abnormal conditions on the PVC
> - Reports actual used capacity alongside health status
> - On by default, checks every 5 minutes; requires Weka 4.3+
> 
> ### How to test?
> 1. Deploy the CSI driver and create a PVC
> 2. Remove the volume's backing filesystem on the Weka cluster
> 3. Run `kubectl describe pvc <name>` and confirm an abnormal condition appears within 5 minutes
> 
> ### Why make this change?
> Previously Kubernetes couldn't tell if a volume's storage was still intact; problems went unnoticed until an application failed. This gives ongoing visibility into real volume health.
# Release v2.8.9
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### Bug Fixes
* fix: close opened fd, when context deadline exceeded by @assafgi in https://github.com/weka/csi-wekafs/pull/700
* fix: dependentbot security alerts by @assafgi in https://github.com/weka/csi-wekafs/pull/702
* chore(deps): update UBI minimal to 9.8 by @ryan-keswick in https://github.com/weka/csi-wekafs/pull/706

## New Contributors
* @ryan-keswick made their first contribution in https://github.com/weka/csi-wekafs/pull/706

# Release v2.8.8
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### Bug Fixes
* fix: remmove gc purge bound to grpc context by @assafgi in https://github.com/weka/csi-wekafs/pull/696
* fix: missing xargs by @assafgi in https://github.com/weka/csi-wekafs/pull/697


# Release v2.8.7
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### Bug Fixes
* fix: prevent controller mount stacking under concurrent provisioning by cloning MountOptions map (CSI-420) by @kristina-solovyova in https://github.com/weka/csi-wekafs/pull/694
* fix: preserve thin FS on FS expansion by @assafgi in https://github.com/weka/csi-wekafs/pull/693


# Release v2.8.6
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### Bug Fixes
* fix: remove duplicated node server cluster role permissions by @assafgi in https://github.com/weka/csi-wekafs/pull/689
* feat: propagate zone/region topology from CreateVolume requests by @assafgi in https://github.com/weka/csi-wekafs/pull/690
### Miscellaneous
* docs: fix autoExpandFilesystems description by @assafgi in https://github.com/weka/csi-wekafs/pull/688


# Release v2.8.5
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New features
* feat: add per-pod and per-PVC mount option overrides via annotations by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/678
* feat: support standard topology.kubernetes.io/zone key in CSI NodeGetInfo by @assafgi in https://github.com/weka/csi-wekafs/pull/685
### Bug Fixes
* fix: initialize K8s manager in node-only mode to enable per-pod mount option overrides by @kristina-solovyova in https://github.com/weka/csi-wekafs/pull/681
### Miscellaneous
* docs: clarify mount option override version requirement, examples by @kristina-solovyova in https://github.com/weka/csi-wekafs/pull/684


# Release v2.8.4
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### Bug Fixes
* fix: make csi health check optional and add csi node porbe logs by @assafgi in https://github.com/weka/csi-wekafs/pull/680


# Release v2.8.3
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### Bug Fixes
* fix: remove semconv schema attributes from otel trace provider by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/662
* fix: update OCP SecurityContextConstraints to allow emptyDir volume type by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/661
* fix: propagate unmount func errors, fix decRef order, isolate mount paths by CSI role by @kristina-solovyova in https://github.com/weka/csi-wekafs/pull/659
* fix: prevent liveness probe from hanging on unresponsive WekaFS, propagate context (CSI-412) by @kristina-solovyova in https://github.com/weka/csi-wekafs/pull/660
* fix: mount selinux filesystem in node server daemonset to correctly manage labels in UBI image by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/676
### Miscellaneous
* chore: trigger sanity workflow on ready_for_review events by @kristina-solovyova in https://github.com/weka/csi-wekafs/pull/663
* docs: add OCP namespace pod-security label instructions, regenerate readme by @kristina-solovyova in https://github.com/weka/csi-wekafs/pull/675


# Release v2.8.2
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### Bug Fixes
* fix: add timeout to frontends check by @assafgi in https://github.com/weka/csi-wekafs/pull/646
* fix: add socket file permissions for containers using WekaFS (CSI-405) by @assafgi in https://github.com/weka/csi-wekafs/pull/647
* fix: security issues by @rugggger in https://github.com/weka/csi-wekafs/pull/650
### Miscellaneous
* chore(deps): bump go.opentelemetry.io/otel/sdk from 1.36.0 to 1.40.0 by @dependabot[bot] in https://github.com/weka/csi-wekafs/pull/644

## New Contributors
* @dependabot[bot] made their first contribution in https://github.com/weka/csi-wekafs/pull/644

# Release v2.8.1
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New features
* feat: support creation of owned filesystems by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/639
### Improvements
* feat: make CSI aware of error 403 on getMountToken by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/638
### Bug Fixes
* feat: unify controller leader election in driver and gate sidecars with wait-for-leader by @caspx in https://github.com/weka/csi-wekafs/pull/633
* fix(CSI-404): health check fails if any frontend is disconnected by @caspx in https://github.com/weka/csi-wekafs/pull/643
### Miscellaneous
* docs: add nodeAffinity configuration to all static PV examples by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/634
* docs: update directory-backed volumes description in usage.md by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/642


# Release v2.8.0
- feat: add per file system encryption support
- feat: add support for preventing over provisioning of directory backed PVCs
- fix: add driver level container name configuration for multi client set deployments
- fix: panic when "Frontend is not connected" is present in /proc/wekafs/interface
# Release v2.7.8
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### Bug Fixes
* fix: add controller missing priority class set and controller and node requests and limits config by @assafgi in https://github.com/weka/csi-wekafs/pull/621


# Release v2.7.7
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### Bug Fixes
* fix: add namespace to controller Role by @aplulu in https://github.com/weka/csi-wekafs/pull/603

## New Contributors
* @aplulu made their first contribution in https://github.com/weka/csi-wekafs/pull/603

# Release v2.7.6
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### Bug Fixes
* fix: server stop labels cleanup condition by @assafgi in https://github.com/weka/csi-wekafs/pull/602

## New Contributors
* @assafgi made their first contribution in https://github.com/weka/csi-wekafs/pull/602

# Release v2.7.5
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### Improvements
* feat(CSI-376): improve lookup of local containers to rely on driver interface before REST API by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/551
### Bug Fixes
* fix(CSI-375): deletion of volume may be stuck if its contents were already trashed by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/550
* fix(CSI-377): node labels are cleaned up upon startup / termination even if labels managed externally by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/553
* fix(CSI-373): cannot mmap() on weka CSI volumes with SELinux enforced by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/554
### Miscellaneous
* chore(deps): update sidecars as of 2025-07-27 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/556


# Release v2.7.4
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### Improvements
* feat(CSI-360): make node topology labeling configurable in favor of auxilary management via operator by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/526
### Bug Fixes
* fix: minimize race condition on weka driver check by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/527
### Miscellaneous
* chore(deps): update codacy/git-version action to v2.8.2 by @renovate in https://github.com/weka/csi-wekafs/pull/520


# Release v2.7.3
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### Improvements
* fix(WEKAPP-490309): use resolution of inode via API for CSI role starting from Weka 4.4.7 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/517
* feat(CSI-359): allow retention of SElinux policy machine configuration on OCP clusters by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/515
### Bug Fixes
* fix(CSI-358): rotation to another API andpoint does not happen on error 503 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/514
### Miscellaneous
* ci: make sure to use latest docker buildx to support GH cache v2 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/516


# Release v2.7.2
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
This version incorporates minor performance improvements and switches tracing from Jaeger to OTLP
### Improvements
* feat(CSI-342): get filesystem free space via API without requiring fs mount by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/476
### Miscellaneous
* feat(CSI-317): switch from jaeger to otlptracegrpc by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/492


# Release v2.7.1
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
This release includes bug fixes and stability improvements

### Improvements
* feat(CSI-356): avoid failback to xattr upon quota set error by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/494
### Bug Fixes
* feat(CSI-355): if user of role CSI cannot resolvePath via API, switch to mount by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/493
* fix(CSI-357): server default mount options take precedence over custom ones by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/495

### Known limitations
* Due to current limitation of WEKA software, publishing snapshot-backed volumes via NFS transport is not supported and could result in stale file handle error when trying to access the volume contents from within the pod.This limitation applies to both new snapshot-backed volumes and to any volumes that were cloned from existing PersistentVolume or Snapshot.

# Release v2.7.0
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
This release provides ability to provision encrypted volumes using a cluster-wide KMS configuration.
The encryption functionality is not complete and will provide additional abilities in next major version.

### New features
* feat(CSI-315): partial support of encrypted volumes for FS backing by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/442
### Improvements
* refactor(CSI-330): use native kubernetes client for handling labels and remove reliance on kubectl init container by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/460
* fix(CSI-331): add terminationGracePeriodSeconds to controller and pod workloads by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/462
* feat(CSI-337): no readiness check and labels removal when weka client not running by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/469
* feat(CSI-340): use API for inode resolution on path for setting quota by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/473
* feat(CSI-341): cache filesystem and snapshot objects to avoid multiple similar API calls by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/474
### Bug Fixes
* fix(CSI-326): invalid socket dir path causing 2 instances of CSI plugin to interfere with each other by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/453
* fix(CSI-329): report volume accessible topology with label corresponding to driver name for multiple instances in large clusters by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/461
* fix(CSI-332): collision on OCP machineConfigs when installing multiple instances due to same name by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/463
* fix(CSI-330): label cleanup should be done only on node server by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/472
* fix(CSI-333): acl mount option mishandled by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/468
* fix(CSI-343): panic on apiclient when endpoint list is empty by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/475
* fix(CSI-350): avoid wekafs mounter from mounting if weka is not running by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/484
* fix(CSI-351): incorrect error message when creating PVC with CSI secret missing endpoints by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/485
* fix(CSI-352): topology.csi.weka.io/transport=wekafs & topology.wekafs.csi/node labels missing on weka client restart by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/486
* fix: do not wait 2 times when deleting nfs permissions by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/488
### Miscellaneous
* ci(chore): update workflows to use larger server group by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/459
* chore(deps): update go dependencies as of 2025-02-16 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/464
* chore(deps): update go dependencies as of 2025-03-15 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/487
### Known limitations
* Due to current limitation of WEKA software, publishing snapshot-backed volumes via NFS transport is not supported and could result in stale file handle error when trying to access the volume contents from within the pod.This limitation applies to both new snapshot-backed volumes and to any volumes that were cloned from existing PersistentVolume or Snapshot.

# Release v2.7.0-beta
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
This release provides ability to provision encrypted volumes using a cluster-wide KMS configuration.
The encryption functionality is not complete and will provide additional abilities in next major version.

### New features
* feat(CSI-315): partial support of encrypted volumes for FS backing by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/442
### Bug Fixes
* fix(CSI-326): invalid socket dir path causing 2 instances of CSI plugin to interfere with each other by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/453


# Release v2.6.2
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
This version resolves issues that could occur during accessing CSI-published volumes on SELinux-enabled nodes.
Since the issue is related to switching to RedHat Universal Base Image (UBI9), the interim solution is to revert switching to UBI.
In the following versions, a better solution will be incorporated and the plugin will be again based on UBI9 image.

### Improvements
* refactor(CSI-272): move NFS client registration to APIClient startup rather than on each mount by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/436
* refactor(CSI-318): add configurable wait for filesystem / snapshot deletion by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/437
### Bug Fixes
* fix(CSI-322): revert CSI-309 migrate from Alpine to RedHat UBI9 base image by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/448
* fix(CSI-320): print raw entry in log when endpoint address fails to be parsed" by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/443
* fix(CSI-323): when snapshot of directory backed volumes is prohibited, incorrect error message is shown stating volume is legacy by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/449
### Miscellaneous
* chore(deps): optimize CSI sanity speed during CI by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/438


# Release v2.6.1
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New features
* feat(CSI-321): provide ability to add custom labels to CSI components by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/445
### Miscellaneous
* chore(deps): update helm/chart-testing-action action to v2.7.0 by @renovate in https://github.com/weka/csi-wekafs/pull/433


# Release v2.6.0
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New features
* feat(CSI-300): add arm64 support by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/379* 
* feat(CSI-312): add topology awareness by providing accessibleTopology in PV creation by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/426
* feat(CSI-313): add configuration for skipping out-of-band volume garbage collection by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/427
### Improvements
* feat(CSI-310): drop container_name mount option from volume context by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/408
* feat(CSI-311): add CSI driver version used for provisioning a PV into volumeContext by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/409
* feat(CSI-308): add support for ReadWriteOncePod, ReadOnlyOncePod by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/399
* feat(CSI-309): migrate from Alpine to RedHat UBI9 base image by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/400
### Bug fixes
* refactor(CSI-305): change mount Map logic for WEKAFS to align with NFS and support same fs name on SCMC by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/383
* chore(deps): improve the way of locar to delete multi-depth directories by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/422
* fix(CSI-306): compatibility for sync_on_close not logged by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/395
### Miscellaneous
* chore(deps): add LICENSE to UBI /licenses by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/418
* chore(deps): update golang dependencies as of 2024-12-09 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/410
* chore(deps): update helm/kind-action action to v1.12.0 by @renovate in https://github.com/weka/csi-wekafs/pull/414
* chore(deps): update registry.access.redhat.com/ubi9/ubi to v9.5-1736404036 by @renovate in https://github.com/weka/csi-wekafs/pull/421
* fix(deps): update golang.org/x/exp digest to 7588d65 by @renovate in https://github.com/weka/csi-wekafs/pull/407
* fix(deps): update module google.golang.org/grpc to v1.69.4 by @renovate in https://github.com/weka/csi-wekafs/pull/406
* fix(deps): update module google.golang.org/protobuf to v1.36.2 by @renovate in https://github.com/weka/csi-wekafs/pull/415
* chore(deps): add labels to CSI Docker image by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/425
* chore(deps): update go dependencies as of 2025-01-19 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/429


# Release v2.5.1
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### Improvements
* feat(CSI-295): add affinity for controller and separated nodeSelector for controller and node by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/377
* feat(CSI-302): convert controller StatefulSet to Deployment by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/381
* feat(CSI-303): add livenessProbe to attacher sidecar by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/382
### Bug Fixes
* fix(CSI-294): caCertificate, NfsTargetIps, localContainerName are not hashed in API client by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/371
* fix(CSI-292): parse NFS version 3.0 to correctly pass it to mountoption by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/372
* fix(CSI-297): nfsTargetIps override is handled incorreclty when empty by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/374
* fix(CSI-296): node registration fails after switch transport from NFS to Wekafs due to label conflict by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/375
* feat(CSI-301): bump locar to version 0.4.2 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/380
### Miscellaneous
* docs: fix the example of static provisioning of directory-backed volume by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/370
* chore(deps): update actions/checkout digest to 11bd719 by @renovate in https://github.com/weka/csi-wekafs/pull/352
* fix(deps): update kubernetes packages to v0.31.2 by @renovate in https://github.com/weka/csi-wekafs/pull/376
* chore(deps): update registry.k8s.io/kubernetes/kubectl to v1.31.2 by @renovate in https://github.com/weka/csi-wekafs/pull/373
* fix(deps): update golang.org/x/exp digest to f66d83c by @renovate in https://github.com/weka/csi-wekafs/pull/349
* fix(deps): update module github.com/prometheus/client_golang to v1.20.5 by @renovate in https://github.com/weka/csi-wekafs/pull/369

### Known limitations
* Due to current limitation of WEKA software, publishing snapshot-backed volumes via NFS transport is not supported and could result in `stale file handle` error when trying to access the volume contents from within the pod. 
  This limitation applies to both new snapshot-backed volumes and to any volumes that were cloned from existing PersistentVolume or Snapshot.

# Release v2.5.0
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New features
* feat(CSI-253): support custom CA certificate in API secret by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/324
   This enhancement allows providing a base64-encoded CA certificate in X509 format for secure API connectivity
* feat(CSI-213): support NFS transport by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/299
   This feature provides a way to provision and publish WEKA CSI volumes via NFS transport for clusters that cannot be installed with Native WEKA client software. For additional information, refer to https://github.com/weka/csi-wekafs/blob/main/docs/NFS.md
* feat(CSI-252): implement kubelet PVC stats by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/322
   This feature provides a way to monitor WEKA CSI volume usage statistics via kubelet statistics collection. 
   The following statistics are supported:
    * `kubelet_volume_stats_capacity_bytes`
    * `kubelet_volume_stats_available_bytes`
    * `kubelet_volume_stats_used_bytes`
    * `kubelet_volume_stats_inodes`
    * `kubelet_volume_stats_inodes_free`
    * `kubelet_volume_stats_inodes_used`

### Known limitations
* Due to current limitation of WEKA software, publishing snapshot-backed volumes via NFS transport is not supported and could result in `stale file handle` error when trying to access the volume contents from within the pod. 
  This limitation applies to both new snapshot-backed volumes and to any volumes that were cloned from existing PersistentVolume or Snapshot.
### Improvements
* feat(CSI-244): match subnets if existing in client rule by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/315
* feat(CSI-245): allow specifying client group for NFS by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/316
* feat(CSI-249): optimize NFS mounter to use multiple targets by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/318
* feat(CSI-247): implement InterfaceGroup.GetRandomIpAddress() by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/319
* refactor(CSI-250): do not maintain redundant active mounts from node server after publishing volume by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/320
* fix(CSI-258): make NFS protocol version configurable by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/334
* feat(CSI-259): report mount transport in node topology by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/337
* feat(CSI-268): support NFS target IPs override via API secret by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/343
* fix(CSI-274): add sleep before mount if nfs was reconfigured by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/353
* chore(deps): add OTEL tracing and span logging for GRPC server by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/361
* feat(CSI-288): validate API user role prior to performing ops by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/365
* feat(CSI-289): add default nfs option for rdirplus by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/368
### Bug Fixes
* fix(CSI-241): disregard sync_on_close in mountmap per FS by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/310
* fix(CSI-241): conflict in metrics between node and controller by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/325
* fix(CSI-243): service accounts for CSI plugin assume ImagePullSecret and cause error messages. by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/311
* feat(CSI-239): moveToTrash does not return error to upper layers by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/312
* fix(CSI-241): fix unmountWithOptions to use map key rather than options.String() by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/317
* chore(deps): update official documentation URL by @AriAttias in https://github.com/weka/csi-wekafs/pull/303
* fix(CSI-256): avoid multiple mounts to same filesystem on same mountpoint by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/331
* fix(CSI-257): wekafsmount refcount is decreased even if unmount failed by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/332
* fix(CSI-260): lookup of NFS interface group fails when empty name provided by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/341
* fix(CSI-270): filesystem-backed volumes cannot be deleted due to stale NFS permissions by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/344
* fix(CSI-269): nfsmount mountPoint may be incorrect in certain cases by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/345
* fix(CSI-273): remove rdirplus from mountoptions by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/355
* fix(CSI-275): version of NFS is only set to V4 during NFS permission creation by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/354
* fix(CSI-276): allow unpublish even if publish failed with stale file handle by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/356
* feat(CSI-286): whitespace not trimmed for localContainerName in CSI secret by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/364
### Miscellaneous
* chore(deps): combine chmod with ADD in Dockerfile by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/313
* chore(deps): update packages to latest versions and Go to 1.22.5 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/314
* docs(CSI-254): update official docs link in Helm templates and README by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/323
* fix(CSI-255): remove unmaintained kubectl-sidecar image by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/330
* fix(deps): update module github.com/prometheus/client_golang to v1.20.4 by @renovate in https://github.com/weka/csi-wekafs/pull/338
* fix(deps): update module google.golang.org/grpc to v1.67.0 by @renovate in https://github.com/weka/csi-wekafs/pull/339
* ci(CSI-213): add NFS sanity by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/340
* chore(deps): update Go dependencies to latest by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/357

## New Contributors
* @AriAttias made their first contribution in https://github.com/weka/csi-wekafs/pull/303

# Release v2.5.0-beta2
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### Improvements
* feat(CSI-259): report mount transport in node topology by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/337
* feat(CSI-268): support NFS target IPs override via API secret by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/343
### Bug Fixes
* fix(CSI-260): lookup of NFS interface group fails when empty name provided by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/341
* fix(CSI-270): filesystem-backed volumes cannot be deleted due to stale NFS permissions by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/344
* fix(CSI-269): nfsmount mountPoint may be incorrect in certain cases by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/345
### Miscellaneous
* fix(deps): update module github.com/prometheus/client_golang to v1.20.4 by @renovate in https://github.com/weka/csi-wekafs/pull/338
* fix(deps): update module google.golang.org/grpc to v1.67.0 by @renovate in https://github.com/weka/csi-wekafs/pull/339
* ci(CSI-213): add NFS sanity by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/340


**Full Changelog**: https://github.com/weka/csi-wekafs/compare/v2.5.0-beta...main

<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New features
* feat(CSI-253): support custom CA certificate in API secret by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/324
* feat(CSI-213): support NFS transport by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/299
* feat(CSI-252): implement kubelet PVC stats by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/322
### Improvements
* feat(CSI-244): match subnets if existing in client rule by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/315
* feat(CSI-245): allow specifying client group for NFS by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/316
* feat(CSI-249): optimize NFS mounter to use multiple targets by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/318
* feat(CSI-247): implement InterfaceGroup.GetRandomIpAddress() by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/319
* refactor(CSI-250): do not maintain redundant active mounts from node server after publishing volume by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/320
* fix(CSI-258): make NFS protocol version configurable by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/334
* feat(CSI-259): report mount transport in node topology by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/337
* feat(CSI-268): support NFS target IPs override via API secret by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/343
### Bug Fixes
* fix(CSI-241): disregard sync_on_close in mountmap per FS by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/310
* fix(CSI-241): conflict in metrics between node and controller by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/325
* fix(CSI-243): service accounts for CSI plugin assume ImagePullSecret and cause error messages. by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/311
* feat(CSI-239): moveToTrash does not return error to upper layers by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/312
* fix(CSI-241): fix unmountWithOptions to use map key rather than options.String() by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/317
* chore(deps): update official documentation URL by @AriAttias in https://github.com/weka/csi-wekafs/pull/303
* fix(CSI-256): avoid multiple mounts to same filesystem on same mountpoint by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/331
* fix(CSI-257): wekafsmount refcount is decreased even if unmount failed by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/332
* fix(CSI-260): lookup of NFS interface group fails when empty name provided by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/341
* fix(CSI-270): filesystem-backed volumes cannot be deleted due to stale NFS permissions by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/344
* fix(CSI-269): nfsmount mountPoint may be incorrect in certain cases by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/345
### Miscellaneous
* chore(deps): combine chmod with ADD in Dockerfile by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/313
* chore(deps): update packages to latest versions and Go to 1.22.5 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/314
* docs(CSI-254): update official docs link in Helm templates and README by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/323
* fix(CSI-255): remove unmaintained kubectl-sidecar image by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/330
* fix(deps): update module github.com/prometheus/client_golang to v1.20.4 by @renovate in https://github.com/weka/csi-wekafs/pull/338
* fix(deps): update module google.golang.org/grpc to v1.67.0 by @renovate in https://github.com/weka/csi-wekafs/pull/339
* ci(CSI-213): add NFS sanity by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/340

## New Contributors
* @AriAttias made their first contribution in https://github.com/weka/csi-wekafs/pull/303

# Release v2.5.0-beta
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New features
* feat(CSI-253): support custom CA certificate in API secret by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/324
* feat(CSI-213): support NFS transport by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/299
* feat(CSI-247): implement InterfaceGroup.GetRandomIpAddress() by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/319
* feat(CSI-252): implement kubelet PVC stats by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/322
### Improvements
* feat(CSI-244): match subnets if existing in client rule by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/315
* feat(CSI-245): allow specifying client group for NFS by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/316
* feat(CSI-249): optimize NFS mounter to use multiple targets by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/318
* refactor(CSI-250): do not maintain redundant active mounts from node server after publishing volume by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/320
* fix(CSI-258): make NFS protocol version configurable by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/334
### Bug Fixes
* fix(CSI-241): disregard sync_on_close in mountmap per FS by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/310
* fix(CSI-241): conflict in metrics between node and controller by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/325
* fix(CSI-243): service accounts for CSI plugin assume ImagePullSecret and cause error messages. by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/311
* feat(CSI-239): moveToTrash does not return error to upper layers by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/312
* fix(CSI-241): fix unmountWithOptions to use map key rather than options.String() by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/317
* chore(deps): update official documentation URL by @AriAttias in https://github.com/weka/csi-wekafs/pull/303
* fix(CSI-256): avoid multiple mounts to same filesystem on same mountpoint by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/331
* fix(CSI-257): wekafsmount refcount is decreased even if unmount failed by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/332
### Miscellaneous
* chore(deps): combine chmod with ADD in Dockerfile by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/313
* chore(deps): update packages to latest versions and Go to 1.22.5 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/314
* docs(CSI-254): update official docs link in Helm templates and README by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/323
* fix(CSI-255): remove unmaintained kubectl-sidecar image by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/330

## New Contributors
* @AriAttias made their first contribution in https://github.com/weka/csi-wekafs/pull/303

# Release v2.4.1
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New features
* fix(CSI-226): support IPv6 in APIclient by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/287
* feat(CSI-227): allow host networking via configuration by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/288
### Improvements
* fix(CSI-237): increase parralelism of PV deletions by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/295
### Bug Fixes
* fix(CSI-224,WEKAPP-417375): race condition on multiple volume deletion in parallel by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/286
* fix(CSI-236): for OCP installations, only 1 machineConfigPolicy was created by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/294
### Miscellaneous
* chore(deps): update dependencies to latest version by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/278
* chore(deps): put installation slack link in code block by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/291
* chore(deps): allow WEKAPP tickets in lint by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/290
* chore(deps): bump Go dependencies to latest version by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/297


# Release v2.3.4
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New features
* fix(CSI-226): support IPv6 in APIclient by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/287
* feat(CSI-227): allow host networking via configuration by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/288
### Improvements
* fix(CSI-237): increase parralelism of PV deletions by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/295
### Bug Fixes
* fix(CSI-224,WEKAPP-417375): race condition on multiple volume deletion in parallel by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/286
* fix(CSI-236): for OCP installations, only 1 machineConfigPolicy was created by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/294
### Miscellaneous
* chore(deps): update dependencies to latest version by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/278
* chore(deps): put installation slack link in code block by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/291
* chore(deps): allow WEKAPP tickets in lint by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/290
* chore(deps): bump Go dependencies to latest version by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/297


# Release v2.4.0
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New Features
* feat(CSI-211): support new API paths nodes->processes as per cluster version by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/269
* feat(CSI-215): improve lookup for frontend containers to include protocols by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/269
* feat(CSI-209): automatically update API endpoints on re-login by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/269
* feat(CSI-221): support configurable fsGroupPolicy by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/269
* feat(CSI-219): add securityContextConstraints for CSI on OCP by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/269
* feat(CSI-220): automatically determine selinux for OCP nodes by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/269

### Bug Fixes
* fix(CSI-217): Containers are filtered by status but not by state by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/269
* fix(CSI-223): mount still attempted when local container name is missing by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/269

### Miscellaneous
* chore(deps): update azure/setup-helm action to v4 by @renovate in https://github.com/weka/csi-wekafs/pull/243
* chore(deps): update helm/kind-action action to v1.10.0 by @renovate in https://github.com/weka/csi-wekafs/pull/240
* chore(deps): update actions/checkout digest to 692973e by @renovate in https://github.com/weka/csi-wekafs/pull/256
* fix(deps): update module github.com/google/uuid to v1.6.0 by @renovate in https://github.com/weka/csi-wekafs/pull/221
* fix(deps): update golang.org/x/exp digest to 7f521ea by @renovate in https://github.com/weka/csi-wekafs/pull/257
* fix(deps): update module google.golang.org/grpc to v1.64.0 by @renovate in https://github.com/weka/csi-wekafs/pull/224
* fix(deps): update module github.com/rs/zerolog to v1.33.0 by @renovate in https://github.com/weka/csi-wekafs/pull/235
* chore(deps): update docker/build-push-action action to v6 by @renovate in https://github.com/weka/csi-wekafs/pull/264
* fix(deps): update module google.golang.org/protobuf to v1.34.2 by @renovate in https://github.com/weka/csi-wekafs/pull/263
* chore(deps): update softprops/action-gh-release action to v2 by @renovate in https://github.com/weka/csi-wekafs/pull/265
* fix(deps): update module github.com/hashicorp/go-version to v1.7.0 by @renovate in https://github.com/weka/csi-wekafs/pull/260
* chore(deps): update dependency go to v1.22.4 by @renovate in https://github.com/weka/csi-wekafs/pull/259


# Release v2.3.4
<!-- Release notes generated using configuration in .github/release.yaml at main -->



# Release v2.3.2
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed

### Bug Fixes
* fix(CSI-170): error not reported when moving directory to trash by @sergeyberezansky in in https://github.com/weka/csi-wekafs/pull/184

### Miscellaneous
* chore(deps): update helm/chart-testing-action action to v2.6.1 by @renovate in https://github.com/weka/csi-wekafs/pull/184
* chore(deps): update helm/chart-releaser-action action to v1.6.0 by @renovate in https://github.com/weka/csi-wekafs/pull/183


# Release v2.3.1
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed

### Features
* feat(CSI-166): update CSI spec to 1.9.0 by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/178

### Bug Fixes
* fix(CSI-163): missing ca-certificates package in wekafs container image by @sergeyberezansky  in https://github.com/weka/csi-wekafs/pull/179

### Miscellaneous
* chore(deps): update actions/checkout digest to b4ffde6 by @renovate in https://github.com/weka/csi-wekafs/pull/161
* chore(deps): update stefanzweifel/git-auto-commit-action action to v5 by @renovate in https://github.com/weka/csi-wekafs/pull/167
* chore(deps): update helm/chart-testing-action action to v2.6.0 by @renovate in https://github.com/weka/csi-wekafs/pull/181
* chore(deps): bump dependencies  by @sergeyberezansky  in https://github.com/weka/csi-wekafs/pull/177


# Release v2.3.0
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New features
* feat(CSI-159): add weka driver monitoring for readiness probe by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/58
### Miscellaneous
* chore(deps): update actions/checkout action to v4 by @renovate in https://github.com/weka/csi-wekafs/pull/152
* fix(deps): update kubernetes packages to v0.28.1 by @renovate in https://github.com/weka/csi-wekafs/pull/139
* fix(deps): update module github.com/google/uuid to v1.3.1 by @renovate in https://github.com/weka/csi-wekafs/pull/148
* fix(deps): update module github.com/rs/zerolog to v1.30.0 by @renovate in https://github.com/weka/csi-wekafs/pull/146
* fix(deps): update module google.golang.org/grpc to v1.58.0 by @renovate in https://github.com/weka/csi-wekafs/pull/145
* fix(deps): update module github.com/kubernetes-csi/csi-lib-utils to v0.15.0 by @renovate in https://github.com/weka/csi-wekafs/pull/149
* fix(deps): update opentelemetry-go monorepo to v1.17.0 by @renovate in https://github.com/weka/csi-wekafs/pull/151
* fix(deps): update golang.org/x/exp digest to 9212866 by @renovate in https://github.com/weka/csi-wekafs/pull/144
* chore(deps): update docker/build-push-action action to v5 by @renovate in https://github.com/weka/csi-wekafs/pull/154
* chore(deps): update docker/login-action action to v3 by @renovate in https://github.com/weka/csi-wekafs/pull/155
* chore(deps): update docker/setup-buildx-action action to v3 by @renovate in https://github.com/weka/csi-wekafs/pull/156


# Release v2.2.1
<!-- Release notes generated using configuration in .github/release.yaml at main -->



# Release v2.2.0
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New features
* feat(CSI-122): support multiple Weka clusters on same nodes by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/134
### Miscellaneous
* fix(deps): update module google.golang.org/grpc to v1.56.2 by @renovate in https://github.com/weka/csi-wekafs/pull/135
* fix(deps): update golang.org/x/exp digest to 613f0c0 by @renovate in https://github.com/weka/csi-wekafs/pull/136
* chore(deps): update helm/kind-action action to v1.8.0 by @renovate in https://github.com/weka/csi-wekafs/pull/137


# Release v2.1.2
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### Bug Fixes
* feat(CSI-57): acl mount option by @dontbreakit in https://github.com/weka/csi-wekafs/pull/128
* fix(CSI-118): cannot initialize API client with non-root organization by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/131
### Miscellaneous
* ci(CSI-116): prefix v for all components validation, also CSI-117 by @dontbreakit in https://github.com/weka/csi-wekafs/pull/129
* fix(deps): update golang.org/x/exp digest to 97b1e66 by @renovate in https://github.com/weka/csi-wekafs/pull/126
* fix(deps): update module google.golang.org/protobuf to v1.31.0 by @renovate in https://github.com/weka/csi-wekafs/pull/125


# Release 2.1.1
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed

### Bug fixes
* fix(CSI-75): compatibilityMap has duplicate parameter for same functionality https://github.com/weka/csi-wekafs/pull/120
* fix(CSI-76): filtering Rest API allowed only from 4.1 but should be from 4.0 https://github.com/weka/csi-wekafs/pull/120
* fix(CSI-110): CSI does not propagate error when failing to init API client from secrets https://github.com/weka/csi-wekafs/pull/120
* fix(CSI-112): panic when creating CSI snapshot-based volume and failing to initialize API client https://github.com/weka/csi-wekafs/pull/120
* fix(CSI-113) plugin incorrectly handles secret with API endpoints separated by newline rather than comma https://github.com/weka/csi-wekafs/pull/120

### Miscellaneous
* fix(CSI-111): Replace deprecated ioutil.ReadFile / WriteFile https://github.com/weka/csi-wekafs/pull/120
* docs(CSI-115): document incorrectly states version of Weka for snapshot quotas https://github.com/weka/csi-wekafs/pull/123

**Full Changelog**: https://github.com/weka/csi-wekafs/compare/v2.1.0...v2.1.1

# Release v2.1.0
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### New features
* feat(CSI-67): sign helm chart by @dontbreakit in https://github.com/weka/csi-wekafs/pull/116


### Security
* fix(CSI-109): update registry.k8s.io/sig-storage/csi-snapshotter to v6.2.2 by @renovate in https://github.com/weka/csi-wekafs/pull/113
* update Golang dependencies for the csi binary
  * fix(deps): update module golang.org/x/sync to v0.3.0 by @renovate in https://github.com/weka/csi-wekafs/pull/105
  * fix(deps): update module k8s.io/apimachinery to v0.27.3 by @renovate in https://github.com/weka/csi-wekafs/pull/106
  * fix(deps): update module github.com/prometheus/client_golang to v1.16.0 by @renovate in https://github.com/weka/csi-wekafs/pull/107
  * fix(deps): update module google.golang.org/grpc to v1.56.1 by @renovate in https://github.com/weka/csi-wekafs/pull/108
  * fix(deps): update module github.com/kubernetes-csi/csi-lib-utils to v0.14.0 by @renovate in https://github.com/weka/csi-wekafs/pull/117


# Release v2.0.1
<!-- Release notes generated using configuration in .github/release.yaml at main -->

## What's Changed
### Bug Fixes
* fix(CSI-74): no error returned when fetching info from weka cluster fails by @dontbreakit & @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/102
* fix(CSI-107): revert csi-attacher by @dontbreakit in https://github.com/weka/csi-wekafs/pull/103


# Release 2.0.0
<!-- Release notes generated using configuration in .github/release.yaml at master -->
## What's Changed
Weka CSI Plugin v2.0.0 has a comprehensive set of improvenents and new functionality:
* Support of different backings for CSI volumes (filesystem, writable snapshot, directory)
* CSI snapshot and volume cloning support
* `fsGroup` support
* Custom mount options per storageClass
* Redundant CSI controllers
* Restructuring of CI and release workflows

> **NOTE:** some of the functionality provided by Weka CSI Plugin 2.0.0 requires Weka software of version 4.2 or higher. Please refer to [documentation](README.md) for additional information

> **NOTE:** To better understand the different types of volume backings and their implications, refer to documentation.

### New features
* feat: Support of new volumes from content source by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/11
* feat: Support Mount options by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/18
* feat: Add fsGroup support on CSI driver by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/20
* feat: Support different backing types for CSI volumes by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/69
* feat: official support for multiple controller server replicas by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/47
 
### Improvements
* feat: configurable log format (colorized human-readable logs or JSON structured logs) by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/26
* feat: OpenTelemetry tracing support by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/26
* feat: support of mutually exclusive mount options by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/54
* feat: Add concurrency limitation for multiple requests by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/56
* refactor: concurrency improvements by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/68

### Bug Fixes
* fix: Correctly calculate capacity for FS-based volume expansion (fixu… by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/15
* refactor: do not recover lost mounts and shorten default mountOptions by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/21
* fix: plugin might crash when trying to create dir-based volume on non… by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/29
* fix: CSI-47 Snapshot volumes run out of space after filling FS space by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/35
* fix: WEKAPP-298226 volumes published with ReadOnlyMany were writable by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/36
* fix: initial filesystem capacity conversion to bytes is invalid by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/38
* fix: loozen snapshot id validation for static provisioning by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/41
* fix: re-enable writecache by default by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/51
* fix: make sure op is written correctly for each function by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/67

### Miscellaneous
* style: add more logging to initial FS resize by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/37
* Add Helm linting and install test by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/13
* Push updated docs to main branch straight after PR merge by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/19
* docs: modify helm docs templates by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/22
* chore: add S3 chart upload GH task by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/23
* chore: auto increase version on feat git commit by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/24
* feat: Bump versions of packages by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/25
* chore: change docker build via native buildx GH action by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/27
* ci: add csi-sanity action to PRs by @dontbreakit in https://github.com/weka/csi-wekafs/pull/30
* ci: add release action by @dontbreakit in https://github.com/weka/csi-wekafs/pull/34
* docs: Improve documentation on mount options and different volume types by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/39
* chore: Bump CSI sidecar images to latest version by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/42
* docs: fix capacityEnforcement comment inside storageClass examples by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/46
* Add notifications to slack by @dontbreakit in https://github.com/weka/csi-wekafs/pull/53
* docs: Improve release.yaml to include additional PR labels by @sergeyberezansky in https://github.com/weka/csi-wekafs/pull/70

## Upgrade Implications
In order to support `fsGroup` functionality, the CSIDriver manifest had to be modified. Since this type of Kubernetes objects is defined as immutable, upgrading Helm release with the new version might fail.
Hence, when upgrading from version below 2.0.0, a complete uninstall and reinstall of Helm release is required. 
> NOTE: it is not required to remove any Secrets, storageClass definitions, PersistentVolumes or PersistentVolumeClaims.

## Deprecation Notice
Support of legacy volumes without API binding will be removed in next major release of Weka CSI Plugin. New features rely on API connectivity to Weka cluster and will not be supported on API unbound volumes. Please make sure to migrate all existing volumes to API based scheme prior to next version upgrade. 

# Release 0.8.4
## Bug Fixes
- Fixed an error which caused the CSI Node component to fail starting on Selinux-enabled hosts
- Fixed installation notes to correctly show the helm commands required for seeing the release

# Release 0.8.3
## Bug Fixes
- Fixed a race condition due to which CSI Node component running on same node with 
  CSI Controller component could fail to start

# Release 0.8.2
## Bug Fixes
- Fixed README.md to correct SELinux README.md URL

# Release 0.8.1
## Bug Fixes
- Fix invalid link to CSI SELinux documentation on ArtifactHub page
- Fix version strings are not updated inside Helm chart README.md

# Release 0.8.0
## New features
### SELinux support
Weka CSI Plugin can now work with SELinux-enabled Kubernetes clusters.  
> **NOTE:** Special configuration is required to deploy the Weka CSI plugin in SELinux-compatible mode  
> Refer to [SELinux Support Readme](selinux/README.md) for additional information
## Improvements
- Helm Charts were separated on per-object basis for better supportability
- Custom `kubelet` path may be set, e.g. for using Kubernetes installed into non-default directory 

## Bug Fixes
- Part of new settings in `values.yaml` were not documented
- Improved logging on failure to mount a filesystem due to authorization error
- Fixed a situation in which `csi-registrar` container (part of node server) could enter crash loop due to `csi.Node.v1` not found

# Release 0.7.4
## New features
### Support for authenticated FileSystems and additional organizations
This functionality is supported for Weka clusters of version 3.14 and up
- Filesystems set with auth-required=true can be used for CSI volumes
- Filesystems in non-root organization can be used for CSI volumes

# Release 0.7.3
## Improvements
- Volume ownership and permissions configuration can be set via [storageClass parameters](examples/dynamic_api/storageclass-wekafs-dir-api.yaml)
- Automated doc generation via helm-doc

# Release 0.7.2
## Improvements
- Upgrade sidecar components to latest versions on gcr.io

# Release 0.7.1
## Improvements
- Upgrade sidecar components to latest versions

# Release 0.7.0
## New Features
### Directory Quota support via Weka REST API
- When new dir/v1 volume is created, it is automatically bound to API quota object
- Can be set to either HARD (forbid writes with E_NOSPC) or SOFT (alerts only)
- Supported for dynamic volumes only in this version
- Requires a modification of storage class (or creation of new storage class)
- Requires a Secret creation that contains API connection information
- Current limitation: only new volumes will be set with quota. For setting quota on existing volumes, use migration script in `migration` directory
- Old volumes can be still expanded using a Legacy API secret (see values.yaml), but user is highly encouraged to migrate workloads to new storage class
- Requires Weka software of v3.13 and above. If cluster version is below v3.13, quotas will not be applied.

### Multiple Weka Clusters on same Kubernetes Control Plane
Multiple simultaneous Weka clusters are now supported by a single CSI controller.
This configuration implies a large Kubernetes cluster, which is connected to multiple
Weka clusters, e.g. in different availability zones. 

In such case, single CSI controller may take care of provisioning all volumes.
Please remember to utilize PVC annotations to ensure the PVC is bound to correct Kubernetes node.
>**NOTE:** Support for making a single Kubernetes node a member in multiple Weka clusters
> is not available at this time, and will be introduced in future Weka software versions.

## Improvements
- Build process simplified and Dockerized  
  This allows developers to build the software from sources locally
- Release process was streamlined
- Logging improvements were introduced with refined log levels
- New examples provided for using Weka REST API
- New topology label that allows scheduling of pods only on Kubernetes nodes having CSI node component.  
In order to schedule pods on supporting nodes, add this NodeSelector: ```topology.csi.weka.io/global: "true"```

## Bug Fixes
- `Failed to remove entry...` error messages appeared in logs for every inner directory during PV deletion

## Known Issues
- Authenticated mounts are not supported in current version of CSI plugin

# Release 0.6.6
## Bug fixes
- Changed default mount options to writecache to improve inter-pod performance over CSI volumes

# Release 0.6.5
## Bug fixes
- In rare circumstances, CSI plugin may fail to publish a volume after node server pod restart

# Release 0.6.4
## Improvements
- CSI node driver does not crash when node is not configured as Weka client

# Release 0.6.3
## New Features
- Deployment supported via Helm public repo
- Repository listed on ArtifactHub

## Improvements
- Fixed version strings SymVer2 compatibility
- Added values.schema.json
- Added post-installation notes
- Added documentation on values

# Release 0.6.2
## New Features
- Separation of controller and node plugin components for increased performance and stability 
- Support of deployment via [Helm](https://helm.sh/) in addition to the previous deployment scheme
- Support of adding node taints and tolerations via helm deployment

## Improvements
- Cleanup script now handles entities of all versions
- Plugin logs are now much more readable
- Docker tag pattern was changed from "latest" to version tag

## Known Issues
- During deployment, on slow networks, a node pod can arbitrary enter `CrashLoopBackoff` 
due to node-driver-registrar container loading before wekafs container
In such case, delete the container and it will be recreated automatically.

## Upgrade Steps
In order to upgrade an existing deployment from version below 0.6.0, 
the previous version has to be uninstalled first: 
 
```
./deploy/util/cleanup.sh
```

Then, a new version can be deployed, by following either one of the procedures below:
- [helm public repo](https://artifacthub.io/packages/helm/csi-wekafs/csi-wekafsplugin) (recommended)
- [deploy script](./README.md)
- [helm local installation](deploy/helm/csi-wekafsplugin/LOCAL.md)


# Release 0.5.0
Initial release
