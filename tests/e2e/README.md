# End-to-end tests

These run the Kubernetes external-storage test suite against csi-wekafs installed on a Weka
cluster that the run builds from nothing, over both mount transports.

Trigger it from the Actions tab: **e2e-bliss**. It never runs on a push.

## Why this exists alongside `tests/csi-sanity`

The sanity suite drives the plugin's two gRPC sockets directly. That makes it fast and it does
catch contract violations, but four things are structurally out of its reach:

| | csi-sanity | e2e-bliss |
| --- | --- | --- |
| Kubernetes | none at all — no StorageClass, PVC, pod or sidecar | real cluster, real workloads |
| Transport | NFS only; the `_nfs` test functions are the only ones wired up | native Weka **and** NFS |
| Mounting | skips `NodeStageVolume` / `NodeUnstageVolume`, so nothing mounts | the kubelet mounts every volume |
| Weka cluster | whichever one the self-hosted runner can reach | installed for the run, then discarded |

So the sidecars, the Helm chart, the native mount path and the install itself have had no
automated coverage. That is what this closes.

## What a run does

1. `bliss provision oci-k3s` builds a k3s cluster on OCI, named `csi-e2e-<run id>`.
2. `bliss install --no-csi` installs the Weka operator and a Weka cluster. `--no-csi` is
   deliberate: the driver under test comes from the checkout, not from a release.
3. The weka-operator generates a CSI user and writes `weka-csi-<cluster>`. `install-csi.sh`
   copies the keys the chart needs into `csi-wekafs/csi-wekafs-api-secret`.
4. For each transport: install the chart from `charts/csi-wekafsplugin` with
   `values-<transport>.yaml`, check the mount really used that transport, run the suite,
   uninstall.
5. Logs, events and the JUnit report are uploaded whatever the outcome.
6. The cluster is deleted — unless tests failed and `keep_cluster_on_failure` is set, which is
   the default.

## Checking the transport, and why it is a separate step

`verify-transport.sh` mounts one volume and reads `/proc/mounts` in the pod, asserting `wekafs`
for the native leg and `nfs4`/`nfs` for the NFS one, then writes a file to prove the mount
works rather than merely exists.

The external-storage suite cannot do this. It asks for a volume, gets one, and is satisfied, so
a native leg quietly served over NFS would pass the whole matrix while testing one transport
twice. NFS failback does exactly that by design; `values-wekafs.yaml` turns it off, but a
setting is an intention and this is the proof. The idea is taken from the operator team's own
CSI test plan, which ends by checking that the mount on the node is of `wekafs` type.

## Running the tests against a cluster you already have

The slow part is the cluster, so the test step stands alone. With `KUBECONFIG` pointing at a
cluster that already has the driver installed:

```bash
./tests/e2e/run-e2e.sh wekafs
./tests/e2e/run-e2e.sh nfs
```

To install the driver first:

```bash
export CSI_IMAGE_TAG=v2.9.4 WEKA_CLUSTER=my-cluster TRANSPORT=wekafs
./tests/e2e/install-csi.sh
```

`E2E_FOCUS`, `E2E_SKIP`, `E2E_PARALLEL` and `E2E_TIMEOUT` override what runs; anything after the
transport argument is passed through to the suite.

## Editing the driver definitions

`manifests/testdriver-*.yaml` tell the suite what the driver can do. The capability names are
matched as strings by the suite, and an unrecognised one is **ignored rather than rejected** —
a typo silently deletes a whole family of tests and the run still goes green. When changing
them, check the names against `testsuites.Capability` in the Kubernetes release the suite is
built from, and change a capability only because the driver's advertised
`ControllerGetCapabilities` or `NodeGetCapabilities` changed.

Today the driver advertises no block volume capability, and no `EXPAND_VOLUME` on the node
service — capacity is a Weka quota, so a resize finishes at the controller. `block` and
`nodeExpansion` are off for those reasons, not as a simplification.

## Clusters are reaped, not expired

bliss has no TTL: `provision` takes no expiry flag and `delete` is manual. A cluster kept for
debugging therefore runs until someone removes it. The **e2e-bliss-reap** workflow runs hourly
and deletes deployments named `csi-e2e-*` that are older than 12 hours, judged by the
`created_at` that `bliss info --all --json` reports. Tags are not used for this: `--tag` reaches
the cloud provider's resources but is not stored in bliss's state, so `bliss info` cannot report
or filter on it.

The `csi-e2e-` prefix is the only thing keeping the reaper away from hand-built clusters, so
`reap-clusters.sh` refuses to run with an empty prefix, and `expired_clusters_test.py` covers
that and the other ways the filter could pick the wrong cluster:

```bash
python3 -m unittest discover -s tests/e2e -p '*_test.py'
```

## Configuration

Repository secrets:

| Secret | What it is |
| --- | --- |
| `OCI_CONFIG` | the `~/.oci/config` INI. `key_file` is rewritten at run time, so its value does not matter |
| `OCI_API_PRIVATE_KEY` | OCI API signing key, PEM |
| `OCI_COMPARTMENT_ID` | compartment OCID the cluster is built in |
| `BLISS_OCI_CONFIG_JSON` | the provisioning JSON passed to `bliss provision --config-file` |
| `BLISS_SSH_PUBLIC_KEY` | public key installed on the cluster nodes |
| `DOCKER_USERNAME`, `DOCKER_PASSWORD` | quay.io pull credentials, passed to `bliss install` |
| `PAT` | reads releases from the private `weka/bliss` repo |

Repository variables: `BLISS_VERSION` pins the bliss release (defaults to `latest`).
