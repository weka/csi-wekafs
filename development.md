# Development

## Chart versions

The charts are versioned by CI at build time, not in git.

`Chart.yaml` and `values.yaml` in this repository carry **the last released version**. They are not
updated as development proceeds. When a build runs, CI stamps the version it computed into its own
workspace, packages the chart from that, publishes it, and throws the workspace away - so the
published chart carries the real build version while the committed one does not change.

This is deliberate. The alternative, committing the stamped version back to `dev` after every push,
was how it used to work, and it had three problems: `dev` and `main` disagreed about files whose
only purpose is to be overwritten, so every merge between them conflicted; the commit was pushed
with a token that re-triggers workflows, so a build could set off another build; and it happened in
a job that runs *after* the chart is packaged and published, so it never affected the published
artifact at all.

### Installing from a working tree

Because the committed chart names the last release, installing it straight from a clone gives you
that release's image tags, not the ones your tree builds. To install what you have:

```console
make update-charts        # stamp the charts with the version derived from git describe
helm upgrade --install csi-wekafs -n csi-wekafs --create-namespace charts/csi-wekafsplugin
make update-charts RESTORE=1   # put the committed versions back
```

`make update-charts VERSION=v9.9.9` stamps an explicit version instead.

Do not commit the result. The diff will show more than the version lines, because `yq` rewrites the
file and drops blank lines - CI does the same, so what you install locally matches what it
publishes.

### Where the released version comes from

A release is the one place the version *is* committed: the release workflow stamps `Chart.yaml` and
`values.yaml` on `main` and commits them as `Release vX.Y.Z`. `dev` then picks that up the next
time it is rebased onto `main`, which is why the committed version is always the last release.

# debug

to debug in your IDE:

1. set the KUBECONFIG to the cluster you want to debug
2. run `make deploy-debug` - this will build and deploy the debug image to QUAY repo `csi-wekafs-debug`
3. the script will also deploy the debug image and echo the commands for port-forwarding
4. find the pod you want to debug and `kubectl port-forward <pod-name> 2345:2345 -n $$NAMESPACE"`
5. in your Goland IDE create a "Go Remote" debug to host: localhost port: 2345
6. happy debugging!
