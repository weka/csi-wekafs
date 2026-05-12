# Overview

## Example Intentions
In rare circumstances, additional mount options (e.g. different caching settings) are desired for a particular flow.
This example collection demonstrates multiple approaches to customize mount options:

1. **StorageClass mount options** - Static options defined at StorageClass level
2. **Pod annotations** - Dynamic per-PVC overrides specified on pods
3. **PVC annotations** - Dynamic overrides that apply to all pods mounting the PVC
4. **Combined pod + PVC annotations** - Demonstrating precedence and interaction between different annotation levels

## Mount Option Override Annotations

Weka CSI plugin supports mount option overrides via Kubernetes annotations on pods and PVCs. This allows dynamic customization
of mount options without modifying the StorageClass.

### Pod-level Overrides

Use the `weka.io/mount-options-overrides` annotation on pods to specify per-PVC mount option overrides.

**Format**: One entry per line (or separated by `;`)
```
<pvc-name-regex>: <mount-option-modifiers>
```

**Mount option modifiers**: Comma-separated list with optional `+` (add) or `-` (remove) prefix
```
+readcache          # Add readcache option
-forcedirect        # Remove forcedirect option
inode_bits=64       # Add option (no prefix = add)
+writecache,+noatime  # Multiple options
```

**Example**:
```yaml
annotations:
  weka.io/mount-options-overrides: |
    my-volume-.*: -forcedirect, +readcache
    cache-volume: +writecache
```

### PVC-level Overrides

Use the `weka.io/mount-options-override` annotation on PVCs to specify mount option overrides that apply to all pods mounting the PVC.

**Format**: Similar to pod-level, but without the PVC name regex (applies to all pods)
```
<mount-option-modifiers>
```

**Example**:
```yaml
metadata:
  annotations:
    weka.io/mount-options-override: -forcedirect, +readcache
```

### Application Order

Mount options are applied in the following order (later overrides earlier):
1. StorageClass default options
2. Node Publish default options
3. PVC annotation options (`weka.io/mount-options-override`)
4. Pod annotation options (`weka.io/mount-options-overrides`) - first matching pattern wins

## Supported Mount Options

Common mount options for Weka filesystem include:
- `readcache` / `-readcache`
- `writecache` / `-writecache`
- `forcedirect` / `-forcedirect`
- `coherent`
- `noatime` / `atime`
- `readahead_kb=<size>`
- `dentry_max_age_positive=<seconds>`
- And others as supported by your Weka cluster

# Workflow

## Basic Mount Options Example (StorageClass-level)
> All commands below may be executed by `kubectl apply -f <FILE>.yaml`
1. Create storageclass `storageclass-wekafs-mountoptions`
2. Create CSI secret `csi-wekafs-api-secret`  (Located in [../common/csi-wekafs-api-secret.yaml](../common/csi-wekafs-api-secret.yaml)) 
3. Provision a new volume `pvc-wekafs-fs-mountoptions`
4. Create application that writes timestamp every 10 seconds into `/data/temp.txt`: `csi-app-fs-mountoptions` 
5. Attach to the application and validate the options are added correctly 
   ```
   kubectl exec csi-app-fs-mountoptions -- mount -t wekafs
   ```
   The output should resemble this: 
   ```
   csivol-pvc-15a45f20-Z72GJXDCEWQ5 on /data type wekafs (rw,relatime,readcache,noatime,readahead_kb=32768,dentry_max_age_positive=1000,dentry_max_age_negative=0)
   ```

## Pod Annotation Override Example
> See `pod-annotation-override.yaml`
1. Create CSI secret `csi-wekafs-api-secret` (Located in [../common/csi-wekafs-api-secret.yaml](../common/csi-wekafs-api-secret.yaml))
2. Apply pod YAML that includes `weka.io/mount-options-overrides` annotation
3. Verify mount options reflect the annotation overrides using:
   ```
   kubectl exec csi-app-pod-override -- mount -t wekafs
   ```

## PVC Annotation Override Example
> See `pvc-annotation-override.yaml`
1. Create CSI secret `csi-wekafs-api-secret` (Located in [../common/csi-wekafs-api-secret.yaml](../common/csi-wekafs-api-secret.yaml))
2. Apply PVC and pod YAML
3. The PVC has `weka.io/mount-options-override` annotation that applies to all pods
4. Verify mount options reflect the PVC annotation overrides using:
   ```
   kubectl exec csi-app-pvc-override -- mount -t wekafs
   ```

## Combined Pod and PVC Annotation Example
> See `pod-and-pvc-annotation-combined.yaml`
1. Create CSI secret `csi-wekafs-api-secret` (Located in [../common/csi-wekafs-api-secret.yaml](../common/csi-wekafs-api-secret.yaml))
2. Apply combined YAML with PVC and multiple pods
3. The PVC has base overrides via `weka.io/mount-options-override`
4. Each pod adds additional per-PVC overrides via `weka.io/mount-options-overrides`
5. Verify different mount options for different pods using:
   ```
   kubectl exec csi-app-combined-override-1 -- mount -t wekafs
   kubectl exec csi-app-combined-override-2 -- mount -t wekafs
   ```
   The pods should have different mount options due to their pod-level annotations taking precedence

## Real-World Deployment Example
> See `real-world-deployment.yaml`

Comprehensive example showing:
1. Multi-tier application (database, web, cache)
2. Different PVC-level settings for different tiers
3. Pod-level per-tier tuning
4. A/B testing scenario with two variants using same storage but different options

This demonstrates how mount option overrides enable flexible, workload-specific optimization.

## Additional Resources

- **MOUNT_OPTION_OVERRIDES.md** - Comprehensive guide with syntax, examples, troubleshooting, and best practices
- **pod-annotation-override.yaml** - Simple pod-level annotation example
- **pvc-annotation-override.yaml** - Simple PVC-level annotation example
- **pod-and-pvc-annotation-combined.yaml** - Combined annotation example showing precedence
- **real-world-deployment.yaml** - Production-like multi-tier deployment example

