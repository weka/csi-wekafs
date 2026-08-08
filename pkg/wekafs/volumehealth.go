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
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	v1 "k8s.io/api/core/v1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
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

	// fromCache honours a filesystem object a caller already resolved, which is how a reconciler
	// sweep avoids repeating one identical lookup per volume. A single ControllerGetVolume starts
	// with an empty cache and so still fetches fresh.
	fsObj, err := v.getFilesystemObj(ctx, true)
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
	// left to recover the Weka API credentials from.
	pv, err := cs.getPersistentVolumeByHandle(ctx, volumeID)
	if err != nil {
		return nil, err
	}

	volume, condition, err := cs.describeVolume(ctx, pv, nil)
	if err != nil {
		return nil, err
	}
	return &csi.ControllerGetVolumeResponse{
		Volume: volume,
		Status: &csi.ControllerGetVolumeResponse_VolumeStatus{VolumeCondition: condition},
	}, nil
}

// describeVolume resolves a PersistentVolume into the capacity and condition reported by both
// ControllerGetVolume and ListVolumes. A nil condition with a nil error means the condition could
// not be established, which callers must report as unknown rather than as abnormal.
// The filesystems cache may be nil, in which case every lookup goes to the Weka API.
func (cs *ControllerServer) describeVolume(ctx context.Context, pv *v1.PersistentVolume, filesystems *filesystemCache) (*csi.Volume, *csi.VolumeCondition, error) {
	volumeID := pv.Spec.CSI.VolumeHandle
	logger := log.Ctx(ctx).With().Str("volume_id", volumeID).Logger()

	// The PersistentVolume holds the requested size, which stands in whenever the backend cannot
	// supply one of its own.
	volume := &csi.Volume{VolumeId: volumeID, CapacityBytes: pvCapacityBytes(pv)}

	client, err := cs.apiClientFromPersistentVolume(ctx, pv)
	if err != nil {
		return volume, nil, status.Errorf(codes.Unavailable, "could not reach the Weka API for volume %s: %v", volumeID, err)
	}
	if client == nil {
		logger.Warn().Str("pv", pv.Name).Msg("No Weka API credentials available for volume, reporting condition as unknown")
		return volume, nil, nil
	}

	vol, err := NewVolumeFromId(ctx, volumeID, client, cs)
	if err != nil {
		return volume, nil, err
	}
	// Seed the volume with an already-resolved filesystem, and publish whatever it resolved so the
	// next volume on the same filesystem can skip the lookup.
	vol.fileSystemObject = filesystems.get(vol.FilesystemName)
	defer func() { filesystems.put(vol.FilesystemName, vol.fileSystemObject) }()

	health, err := vol.ProbeHealth(ctx)
	if err != nil {
		if errors.Is(err, ErrVolumeHealthUndetermined) {
			logger.Warn().Err(err).Msg("Reporting volume condition as unknown")
			return volume, nil, nil
		}
		return volume, nil, status.Errorf(codes.Internal, "failed to determine condition of volume %s: %v", volumeID, err)
	}

	if health.Capacity > 0 {
		volume.CapacityBytes = health.Capacity
	}
	if health.Abnormal {
		logger.Warn().Str("condition", health.Message).Msg("Volume is abnormal")
	}
	return volume, &csi.VolumeCondition{Abnormal: health.Abnormal, Message: health.Message}, nil
}

func (cs *ControllerServer) ListVolumes(ctx context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	op := "ListVolumes"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)

	logger := log.Ctx(ctx)
	logger.Debug().Int32("max_entries", req.GetMaxEntries()).Msg(">>>> Received request")
	defer logger.Debug().Msg("<<<< Completed processing request")

	if err := cs.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_LIST_VOLUMES); err != nil {
		logger.Err(err).Msg("Volume listing is not enabled")
		return nil, err
	}
	if req.GetMaxEntries() < 0 {
		return nil, status.Error(codes.InvalidArgument, "max_entries cannot be negative")
	}
	after, err := decodeListVolumesToken(req.GetStartingToken())
	if err != nil {
		// The spec mandates ABORTED here specifically, so the CO knows to restart the listing
		// from the beginning rather than treating this as a transient failure.
		return nil, status.Errorf(codes.Aborted, "invalid starting_token: %v", err)
	}

	ctx, cancel := context.WithTimeout(ctx, cs.getConfig().grpcRequestTimeout)
	defer cancel()

	remaining, err := cs.listDriverPersistentVolumes(ctx, after)
	if err != nil {
		return nil, err
	}

	page, nextToken := paginateVolumes(remaining, int(req.GetMaxEntries()))

	response := &csi.ListVolumesResponse{Entries: cs.describeVolumes(ctx, page), NextToken: nextToken}
	logger.Debug().Int("entries", len(response.Entries)).Bool("more_pages", nextToken != "").Msg("Listed volumes")
	return response, nil
}

// paginateVolumes cuts one page out of the remaining volumes and mints the cursor to resume from,
// which is empty once the page is the last one. A pageSize of zero or less means the CO left
// max_entries unset and the driver picks the page size.
func paginateVolumes(remaining []*v1.PersistentVolume, pageSize int) (page []*v1.PersistentVolume, nextToken string) {
	if pageSize <= 0 {
		pageSize = listVolumesPageSize
	}
	if len(remaining) <= pageSize {
		return remaining, ""
	}
	page = remaining[:pageSize]
	return page, encodeListVolumesToken(page[len(page)-1].Spec.CSI.VolumeHandle)
}

// describeVolumes builds a page of entries from the reconciler's cache, doing no Weka API work at
// all. That is the point: the health monitor applies its --timeout to an entire paginated sweep
// rather than to one page, so probing inline here put a hard ceiling on how many volumes could ever
// be monitored. Volumes the reconciler has not reached yet, or whose result has aged out, are listed
// without a condition rather than guessed at.
func (cs *ControllerServer) describeVolumes(ctx context.Context, pvs []*v1.PersistentVolume) []*csi.ListVolumesResponse_Entry {
	entries := make([]*csi.ListVolumesResponse_Entry, len(pvs))
	uncached := 0
	for i, pv := range pvs {
		handle := pv.Spec.CSI.VolumeHandle
		// The PersistentVolume holds the requested size, which stands in until a probe supplies the
		// size the backend actually reports.
		volume := &csi.Volume{VolumeId: handle, CapacityBytes: pvCapacityBytes(pv)}

		var condition *csi.VolumeCondition
		if entry, ok := cs.conditionCache.lookup(handle); ok {
			if entry.capacity > 0 {
				volume.CapacityBytes = entry.capacity
			}
			if entry.known {
				condition = &csi.VolumeCondition{Abnormal: entry.abnormal, Message: entry.message}
			}
		} else {
			uncached++
		}

		// published_node_ids is left unset: this driver has no controller publish step, and it does
		// not advertise LIST_VOLUMES_PUBLISHED_NODES.
		entries[i] = &csi.ListVolumesResponse_Entry{
			Volume: volume,
			Status: &csi.ListVolumesResponse_VolumeStatus{VolumeCondition: condition},
		}
	}
	if uncached > 0 {
		log.Ctx(ctx).Debug().Int("uncached", uncached).Int("page", len(pvs)).
			Msg("Some volumes have no fresh condition yet, listing them as unknown")
	}
	return entries
}

// listDriverPersistentVolumes returns this driver's PersistentVolumes ordered by volume handle,
// starting after the given handle. The stable ordering is what makes the pagination token a
// cursor rather than an offset, so volumes appearing or disappearing between pages cannot shift
// the remaining pages and cause one to be skipped.
func (cs *ControllerServer) listDriverPersistentVolumes(ctx context.Context, after string) ([]*v1.PersistentVolume, error) {
	if cs.manager == nil {
		return nil, status.Error(codes.Unavailable, "kubernetes client is unavailable, cannot list volumes")
	}
	pvList := &v1.PersistentVolumeList{}
	// A sweep pages through the whole fleet, so every page would otherwise deep-copy every cached
	// PV out of the informer. These objects are only ever read here, never mutated.
	if err := cs.manager.GetClient().List(ctx, pvList, runtimeclient.UnsafeDisableDeepCopy); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list persistent volumes: %v", err)
	}
	return volumesAfterHandle(pvList.Items, cs.getConfig().GetDriver().name, after), nil
}

// volumesAfterHandle keeps this driver's PersistentVolumes whose handle sorts after the cursor,
// ordered by handle.
func volumesAfterHandle(items []v1.PersistentVolume, driverName, after string) []*v1.PersistentVolume {
	matching := make([]*v1.PersistentVolume, 0, len(items))
	for i := range items {
		pv := &items[i]
		if !isDriverPersistentVolume(pv, driverName) || pv.Spec.CSI.VolumeHandle <= after {
			continue
		}
		matching = append(matching, pv)
	}
	sort.Slice(matching, func(i, j int) bool {
		return matching[i].Spec.CSI.VolumeHandle < matching[j].Spec.CSI.VolumeHandle
	})
	return matching
}

// encodeListVolumesToken mints a pagination cursor pointing at the last handle of a page.
func encodeListVolumesToken(handle string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(listVolumesTokenPrefix + handle))
}

// decodeListVolumesToken recovers the handle a previous page ended at. The prefix makes tokens
// this driver did not mint detectable, so they can be rejected with ABORTED instead of being
// silently misread as a starting position.
func decodeListVolumesToken(token string) (string, error) {
	if token == "" {
		return "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", errors.New("token is not a valid cursor")
	}
	cursor, found := strings.CutPrefix(string(raw), listVolumesTokenPrefix)
	if !found {
		return "", errors.New("token was not issued by this driver")
	}
	return cursor, nil
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
		if isDriverPersistentVolume(pv, driverName) {
			return pv, nil
		}
	}
	return nil, status.Errorf(codes.NotFound, "no persistent volume of driver %s exists for volume %s", driverName, volumeID)
}

// isDriverPersistentVolume reports whether a PersistentVolume is backed by this CSI driver.
func isDriverPersistentVolume(pv *v1.PersistentVolume, driverName string) bool {
	return pv.Spec.CSI != nil && pv.Spec.CSI.Driver == driverName
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

// filesystemCache memoizes filesystem lookups for the span of a single ListVolumes call.
//
// Of the API calls a health probe makes, the filesystem lookup is identical for every volume that
// shares a filesystem, so without this a page repeats it once per volume. It pays off for
// directory-backed volumes, and equally for snapshot-backed ones, since a storage class pins all of
// its volumes to one filesystem. Filesystem-backed volumes each own their filesystem, so they get
// one cache entry apiece and no benefit - but no penalty either.
//
// Scoping this to one call bounds the staleness it can introduce: a filesystem removed while a page
// is in flight is still reported as present until the next page.
type filesystemCache struct {
	sync.Mutex
	byName map[string]*apiclient.FileSystem
}

func newFilesystemCache() *filesystemCache {
	return &filesystemCache{byName: make(map[string]*apiclient.FileSystem)}
}

// get returns a previously resolved filesystem, or nil to mean "look it up". A nil cache always
// misses, which is how single-volume callers opt out.
func (fc *filesystemCache) get(name string) *apiclient.FileSystem {
	if fc == nil {
		return nil
	}
	fc.Lock()
	defer fc.Unlock()
	return fc.byName[name]
}

func (fc *filesystemCache) put(name string, fs *apiclient.FileSystem) {
	if fc == nil || fs == nil {
		return
	}
	fc.Lock()
	defer fc.Unlock()
	fc.byName[name] = fs
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
