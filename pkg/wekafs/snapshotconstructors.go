package wekafs

import (
	"context"
	"github.com/golang/glog"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
)

func NewSnapshotFromVolumeCreate(ctx context.Context, name string, sourceVolume Volume, apiClient *apiclient.ApiClient) (Snapshot, error) {
	glog.V(5).Infoln("Creating new snapshot representation object from volume creation", name)

	if apiClient != nil {
		glog.V(5).Infoln("Successfully bound snapshot to backend API", apiClient.Credentials.String())
	}
	hash := calculateSnapshotParamsHash(name, sourceVolume.GetId())
	snap := &UnifiedSnapshot{
		paramsHash: &hash,
		srcVolume:  sourceVolume,
		apiClient:  apiClient,
	}
	return snap, nil
}

func NewSnapshotFromId(ctx context.Context, id string, apiClient *apiclient.ApiClient) (Snapshot, error) {
	glog.V(5).Infoln("Creating new snapshot representation object from snapshot ID", id)
	if err := validateSnapshotId(id); err != nil {
		return &UnifiedSnapshot{}, err
	}

	if apiClient != nil {
		glog.V(5).Infoln("Successfully bound snapshot to backend API", apiClient.Credentials.String())
	}
	Uid := GetSnapshotUuid(id)
	paramsHash := GetSnapshotParamsHash(id)
	s := &UnifiedSnapshot{
		id:         &id,
		Uid:        Uid,
		paramsHash: &paramsHash,
		apiClient:  apiClient,
	}
	glog.V(5).Infoln("Successfully initialized snapshot ID", s.String())
	return s, nil
}
