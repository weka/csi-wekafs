/*
Copyright 2019-2025 Weka.io LTD and The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package wekafs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/rs/zerolog/log"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	v1 "k8s.io/api/core/v1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// pvIndexVolumeHandle names the field index that maps a CSI volume handle to its PersistentVolume.
	pvIndexVolumeHandle = "spec.csi.volumeHandle"
	// volumeSecretCacheTTL bounds how long a Secret read for a volume health check is reused. It
	// matches the chart's default health monitor interval, so a full sweep of the cluster costs
	// about one apiserver read per distinct Secret rather than one per volume.
	volumeSecretCacheTTL = 5 * time.Minute
	volumeHealthyMessage = "volume exists on the Weka cluster and is reachable via the Weka API"
)

// ErrVolumeHealthUndetermined is returned when the volume condition cannot be established
// over the API alone, so the caller must report it as unknown rather than guess.
var ErrVolumeHealthUndetermined = errors.New("volume condition cannot be determined without mounting the filesystem")

// VolumeHealth is the outcome of a mount-free, API-only inspection of a volume.
type VolumeHealth struct {
	Abnormal bool
	Message  string
	// Capacity is the volume size in bytes as reported by the backend, or 0 when the backend
	// does not track it (legacy volumes that keep their size in an extended attribute).
	Capacity int64
}

func abnormalVolumeHealth(format string, args ...interface{}) *VolumeHealth {
	return &VolumeHealth{Abnormal: true, Message: fmt.Sprintf(format, args...)}
}

// ProbeHealth inspects the volume using the Weka REST API only, never mounting the filesystem,
// and reports whether the volume is usable along with its backend-tracked size.
//
// It returns ErrVolumeHealthUndetermined when the answer would require a mount - the volume is
// then neither healthy nor abnormal as far as this driver can tell.
func (v *Volume) ProbeHealth(ctx context.Context) (*VolumeHealth, error) {
	op := "ProbeVolumeHealth"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)

	logger := log.Ctx(ctx).With().Str("volume_id", v.GetId()).Logger()

	if v.apiClient == nil {
		return nil, ErrVolumeHealthUndetermined
	}

	fsObj, err := v.getFilesystemObj(ctx, false)
	if err != nil {
		return nil, err
	}
	if fsObj == nil {
		return abnormalVolumeHealth("filesystem %s does not exist on the Weka cluster", v.FilesystemName), nil
	}
	if fsObj.IsRemoving {
		return abnormalVolumeHealth("filesystem %s is being removed", v.FilesystemName), nil
	}

	// Every volume type is a path inside the filesystem - the filesystem root for filesystem-backed
	// volumes, and a path through the snapshot's access point for snapshot-backed ones, so resolving
	// it also proves the snapshot is still there. Resolving to an inode is the only way to prove
	// existence without mounting, and the inode is what the quota is keyed by. Capacity always comes
	// from that quota, including for filesystem-backed volumes, which carry one on their root inode.
	if !v.apiClient.SupportsResolvePathToInode() {
		return nil, ErrVolumeHealthUndetermined
	}
	relativePath := v.GetRelativePath(ctx)
	inodeId, err := v.apiClient.ResolvePathToInode(ctx, fsObj, relativePath)
	if err != nil {
		if errors.Is(err, apiclient.ObjectNotFoundError) {
			return abnormalVolumeHealth("path %s does not exist on filesystem %s", relativePath, v.FilesystemName), nil
		}
		return nil, err
	}

	quota, err := v.apiClient.GetQuotaByFileSystemAndInode(ctx, fsObj, inodeId)
	if err != nil {
		if errors.Is(err, apiclient.ObjectNotFoundError) {
			// A volume without a quota is not broken - legacy volumes never had one. The inode
			// resolved, so the volume is there; its size just cannot come from the API.
			logger.Debug().Str("path", relativePath).Msg("Volume has no quota, capacity is not known from API")
			return &VolumeHealth{Message: volumeHealthyMessage}, nil
		}
		return nil, err
	}
	return &VolumeHealth{Message: volumeHealthyMessage, Capacity: int64(quota.GetCapacityLimit())}, nil
}

func (cs *ControllerServer) ControllerGetVolume(ctx context.Context, req *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	op := "ControllerGetVolume"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)

	volumeID := req.GetVolumeId()
	logger := log.Ctx(ctx).With().Str("volume_id", volumeID).Logger()
	logger.Debug().Msg(">>>> Received request")
	defer logger.Debug().Msg("<<<< Completed processing request")

	if err := cs.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_GET_VOLUME); err != nil {
		logger.Err(err).Msg("Volume health reporting is not enabled")
		return nil, err
	}
	if err := validateVolumeId(volumeID); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, cs.getConfig().grpcRequestTimeout)
	defer cancel()

	// ControllerGetVolumeRequest carries no secrets, so the PersistentVolume is the only place
	// left to recover the Weka API credentials from. It also holds the requested size, which is
	// the fallback answer whenever the backend cannot supply one.
	pv, err := cs.getPersistentVolumeByHandle(ctx, volumeID)
	if err != nil {
		return nil, err
	}
	response := &csi.ControllerGetVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      volumeID,
			CapacityBytes: pvCapacityBytes(pv),
		},
		Status: &csi.ControllerGetVolumeResponse_VolumeStatus{},
	}

	client, err := cs.apiClientFromPersistentVolume(ctx, pv)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "could not reach the Weka API for volume %s: %v", volumeID, err)
	}
	if client == nil {
		logger.Warn().Str("pv", pv.Name).Msg("No Weka API credentials available for volume, reporting condition as unknown")
		return response, nil
	}

	volume, err := NewVolumeFromId(ctx, volumeID, client, cs)
	if err != nil {
		return nil, err
	}

	health, err := volume.ProbeHealth(ctx)
	if err != nil {
		if errors.Is(err, ErrVolumeHealthUndetermined) {
			logger.Warn().Err(err).Msg("Reporting volume condition as unknown")
			return response, nil
		}
		return nil, status.Errorf(codes.Internal, "failed to determine condition of volume %s: %v", volumeID, err)
	}

	if health.Capacity > 0 {
		response.Volume.CapacityBytes = health.Capacity
	}
	response.Status.VolumeCondition = &csi.VolumeCondition{
		Abnormal: health.Abnormal,
		Message:  health.Message,
	}
	if health.Abnormal {
		logger.Warn().Str("condition", health.Message).Msg("Volume is abnormal")
	}
	return response, nil
}

// getPersistentVolumeByHandle finds the PersistentVolume carrying the given CSI volume handle.
// It is served from the manager's PV informer through a field index, so it costs no API call.
func (cs *ControllerServer) getPersistentVolumeByHandle(ctx context.Context, volumeID string) (*v1.PersistentVolume, error) {
	if cs.manager == nil {
		return nil, status.Error(codes.Unavailable, "kubernetes client is unavailable, cannot look up volume")
	}
	pvList := &v1.PersistentVolumeList{}
	if err := cs.manager.GetClient().List(ctx, pvList, runtimeclient.MatchingFields{pvIndexVolumeHandle: volumeID}); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to look up persistent volume for volume %s: %v", volumeID, err)
	}
	driverName := cs.getConfig().GetDriver().name
	for i := range pvList.Items {
		pv := &pvList.Items[i]
		if pv.Spec.CSI != nil && pv.Spec.CSI.Driver == driverName {
			return pv, nil
		}
	}
	return nil, status.Errorf(codes.NotFound, "no persistent volume of driver %s exists for volume %s", driverName, volumeID)
}

// apiClientFromPersistentVolume builds an API client from the Secret the PersistentVolume points
// at. A nil client with no error means no credentials are available and the caller must treat the
// volume condition as unknown.
func (cs *ControllerServer) apiClientFromPersistentVolume(ctx context.Context, pv *v1.PersistentVolume) (*apiclient.ApiClient, error) {
	ref := preferredSecretRef(pv)
	if ref == nil {
		// Statically provisioned volumes may reference no Secret at all - fall back to the
		// cluster-wide legacy secret if the driver was started with one.
		return cs.getApiStore().GetClientFromSecrets(ctx, nil)
	}
	key := ref.Namespace + "/" + ref.Name
	secrets, cached := cs.secretCache.lookup(key)
	if !cached {
		var err error
		if secrets, err = cs.readSecret(ctx, ref); err != nil {
			return nil, err
		}
		cs.secretCache.store(key, secrets)
	}
	return cs.getApiStore().GetClientFromSecrets(ctx, secrets)
}

// preferredSecretRef picks the Secret to authenticate with, preferring the refs meant for
// controller-side calls. The provisioner secret is never recorded on the PersistentVolume.
func preferredSecretRef(pv *v1.PersistentVolume) *v1.SecretReference {
	if pv.Spec.CSI == nil {
		return nil
	}
	for _, ref := range []*v1.SecretReference{
		pv.Spec.CSI.ControllerExpandSecretRef,
		pv.Spec.CSI.ControllerPublishSecretRef,
		pv.Spec.CSI.NodeStageSecretRef,
		pv.Spec.CSI.NodePublishSecretRef,
	} {
		if ref != nil && ref.Name != "" && ref.Namespace != "" {
			return ref
		}
	}
	return nil
}

func (cs *ControllerServer) readSecret(ctx context.Context, ref *v1.SecretReference) (map[string]string, error) {
	secret := &v1.Secret{}
	// Deliberately an uncached read: going through the manager's client would start an informer
	// that mirrors every Secret in the cluster into this pod's memory.
	key := runtimeclient.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}
	if err := cs.manager.GetAPIReader().Get(ctx, key, secret); err != nil {
		return nil, err
	}
	secrets := make(map[string]string, len(secret.Data))
	for k, val := range secret.Data {
		secrets[k] = string(val)
	}
	return secrets, nil
}

func pvCapacityBytes(pv *v1.PersistentVolume) int64 {
	if capacity, ok := pv.Spec.Capacity[v1.ResourceStorage]; ok {
		return capacity.Value()
	}
	return 0
}

// secretCache memoizes Secrets read while answering ControllerGetVolume. The health monitor
// sweeps every volume on a fixed interval and in practice all volumes of a cluster share a
// single API Secret, so without this each sweep would issue one apiserver read per volume.
type secretCache struct {
	sync.Mutex
	ttl     time.Duration
	entries map[string]secretCacheEntry
}

type secretCacheEntry struct {
	secrets   map[string]string
	fetchedAt time.Time
}

func newSecretCache(ttl time.Duration) *secretCache {
	return &secretCache{ttl: ttl, entries: make(map[string]secretCacheEntry)}
}

// lookup returns the cached Secret contents, if they are present and still fresh. The lock is
// never held across the apiserver read, so one slow read cannot stall health checks of volumes
// that use a different Secret.
func (sc *secretCache) lookup(key string) (map[string]string, bool) {
	sc.Lock()
	defer sc.Unlock()
	entry, ok := sc.entries[key]
	if !ok || time.Since(entry.fetchedAt) >= sc.ttl {
		return nil, false
	}
	return entry.secrets, true
}

func (sc *secretCache) store(key string, secrets map[string]string) {
	sc.Lock()
	defer sc.Unlock()
	sc.entries[key] = secretCacheEntry{secrets: secrets, fetchedAt: time.Now()}
}
