package wekafs

import (
	"context"
	"errors"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/glog"
	"github.com/google/uuid"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/apiclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"time"
)

const (
	WekaSnapshotNamePrefixForSnapshots = "csisnap-"
	MaxSnapshotDeletionDuration        = time.Hour * 2 // Max time to delete snapshot
)

type UnifiedSnapshot struct {
	id         *string
	Uid        *uuid.UUID
	paramsHash *string

	srcVolume Volume
	apiClient *apiclient.ApiClient
}

func (s *UnifiedSnapshot) String() string {
	return "SNAPSHOT ID: " + s.GetId() + " paramsHash: " + s.getParamsHash() + " Uid: " + s.GetUid().String()
}

func (s *UnifiedSnapshot) getCsiSnapshot(ctx context.Context) *csi.Snapshot {
	snapObj, err := s.getObject(ctx)
	if err != nil {
		return &csi.Snapshot{}
	}

	return &csi.Snapshot{
		SnapshotId:     s.GetId(),
		SourceVolumeId: s.srcVolume.GetId(),
		CreationTime:   time2Timestamp(snapObj.CreationTime),
		ReadyToUse:     !snapObj.IsRemoving,
	}
}

func (s *UnifiedSnapshot) GetId() string {
	if s.id != nil {
		return *s.id
	}
	return ""
}

func (s *UnifiedSnapshot) GetUid() uuid.UUID {
	if s.Uid != nil {
		return *s.Uid
	}
	return uuid.Nil
}

func (s *UnifiedSnapshot) Exists(ctx context.Context) (bool, error) {
	glog.V(3).Infoln("Checking if snapshot exists:", s.getParamsHash())
	snapObj, err := s.getObject(ctx)
	if err != nil {
		glog.V(3).Infoln(
			"Failed to fetch snapshot", s.GetId(), "from underlying storage, snapshot named",
			s.getInternalSnapName(), "was not found", err.Error())
		return false, err
	}
	if snapObj == nil || snapObj.Uid == uuid.Nil {
		glog.V(3).Infoln("Snapshot", s.GetId(), "does not exist")
		return false, nil
	}
	if snapObj.IsRemoving {
		glog.Infoln("Snapshot exists, but marked for deletion:", s.String())
		//TODO: handle this in some pretty way. Otherwise we will fail to create new snap with same name
	}
	glog.Infoln("Snapshot doesn't exist:", s.String())
	return true, nil
}

func (s *UnifiedSnapshot) Create(ctx context.Context) error {
	// check if source FS actually exists
	volExists, err := s.srcVolume.Exists(ctx)
	if err != nil {
		return status.Errorf(codes.Internal, "Failed to check for existence of source volume")
	}
	if !volExists {
		return status.Errorf(codes.InvalidArgument, "Source volume was not found")
	}
	srcVol, _ := s.srcVolume.getFilesystemObj(ctx)

	// check if already exists and return OK right away
	snapObj, err := s.getObject(ctx)
	if err != nil {
		return status.Errorf(codes.Internal, "Failed to check if snapshot already exists")
	}
	if snapObj != nil {
		s.generateIdAfterCreation()
		glog.Errorln(s, s.GetId(), s.getCsiSnapshot(ctx))
		return nil // for idempotence
	}

	sr := &apiclient.SnapshotCreateRequest{
		Name:          s.getInternalSnapName(),
		AccessPoint:   s.getInternalAccessPoint(),
		SourceSnapUid: nil,
		IsWritable:    false,
		FsUid:         srcVol.Uid, // TODO: possible error, need to improve logic of checking existence to avoid 2 API different calls
	}

	snap := &apiclient.Snapshot{}

	if err := s.apiClient.CreateSnapshot(ctx, sr, snap); err != nil {
		return status.Errorf(codes.Internal, fmt.Sprintln("Failed to create snapshot", err.Error()))
	}
	s.Uid = &(snap.Uid)
	s.generateIdAfterCreation()
	glog.Infoln("Created UnifiedSnapshot", s)
	return nil
}

func (s *UnifiedSnapshot) getInnerPath() string {
	if s.srcVolume != nil {
		return s.srcVolume.getInnerPath()
	}
	return GetSnapshotInternalPath(*s.id)
}

func (s *UnifiedSnapshot) getParamsHash() string {
	return *s.paramsHash
}
func (s *UnifiedSnapshot) generateIdAfterCreation() {
	fsName := GetFSName(s.srcVolume.GetId())
	hash := s.getParamsHash()
	id := string(VolumeTypeUnifiedSnap) + "/" + fsName + ":" + s.Uid.String()

	innerPath := s.getInnerPath()
	if innerPath != "" {
		id += "/" + innerPath
	}
	id += ":" + hash
	s.id = &id
}

func (s *UnifiedSnapshot) updateAfterDeletion() {
	s.id = nil
	s.Uid = nil
}

func (s *UnifiedSnapshot) getObject(ctx context.Context) (*apiclient.Snapshot, error) {
	if s.apiClient == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "Could not bind snapshot %s to API endpoint", s.GetId())
	}
	snap := &apiclient.Snapshot{}
	var err error
	if s.Uid != nil && *(s.Uid) != uuid.Nil {
		glog.Infoln("Attempting to get object by UID", s.Uid.String())
		err = s.apiClient.GetSnapshotByUid(ctx, *s.Uid, snap)
	} else {
		glog.Infoln("Attempting to get object by name", s.getInternalSnapName())
		snap, err = s.apiClient.GetSnapshotByName(ctx, s.getInternalSnapName())
	}
	if err == apiclient.ObjectNotFoundError {
		return nil, nil // we know that volume doesn't exist
	} else if err != nil {
		glog.Errorln("Failed to fetch snap object by name:", err.Error())
	}
	if snap.Uid != uuid.Nil {
		s.Uid = &snap.Uid
	}

	return snap, nil
}

func (s *UnifiedSnapshot) Delete(ctx context.Context) error {
	exists, err := s.Exists(ctx)
	if err != nil {
		return err
	}
	if !exists {
		glog.Infoln("Could not find snapshot, probably already deleted:", s.String())
		return nil
	}

	glog.V(3).Infoln("Deleting snapshot", s.GetId())
	snapd := &apiclient.SnapshotDeleteRequest{Uid: *s.Uid}
	err = s.apiClient.DeleteSnapshot(ctx, snapd)
	if err != nil {
		if err == apiclient.ObjectNotFoundError {
			glog.Infoln("Deletion of snapshot failed due to not existing")
			return nil
		}
		glog.Errorln("Failed to perform Delete on snapshot", err)
		return err
	}
	// we need to wait till it is deleted
	retryInterval := time.Second
	maxretryInterval := time.Minute

	for start := time.Now(); time.Since(start) < MaxSnapshotDeletionDuration; {
		snap, err := s.getObject(ctx)
		if err != nil {
			if err == apiclient.ObjectNotFoundError {
				return nil
			}
		}
		if snap.Uid == uuid.Nil {
			glog.V(5).Infoln("Snapshot", s.GetId(), "was deleted successfully")
			return nil
		} else if !snap.IsRemoving {
			return errors.New("Snapshot was not marked for deletion although should")
		} else {
			glog.V(4).Infoln("Snapshot is still deleting on system")
		}
		time.Sleep(retryInterval)
		retryInterval = Min(retryInterval*2, maxretryInterval)
	}
	return errors.New("Failed to remove snapshot on time after 30 seconds")

}

func (s *UnifiedSnapshot) getSourceVolumeId() string {
	return s.srcVolume.GetId()
}

func (s *UnifiedSnapshot) getInternalSnapName() string {
	return WekaSnapshotNamePrefixForSnapshots + s.getParamsHash()[MaxHashLengthForObjectNames:]
}

func (s *UnifiedSnapshot) getInternalAccessPoint() string {
	return s.getParamsHash()[:MaxHashLengthForObjectNames]
}
