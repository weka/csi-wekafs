package wekafs

import (
	"context"
	"github.com/wekafs/csi-wekafs/pkg/wekafs/db"
	"golang.org/x/sync/semaphore"
	"google.golang.org/grpc/codes"
	"os"

	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func setupTestNodeServer() *NodeServer {
	// make sure to clean up the database
	_ = os.Remove(db.DBPath)

	database, _ := db.GetDatabase(context.Background())
	return &NodeServer{
		nodeID: "test-node",
		config: &DriverConfig{
			grpcRequestTimeout: 60 * time.Second,
			maxConcurrencyPerOp: map[string]int64{
				"NodePublishVolume": 10,
			},
			debugPath: "/tmp/wekafs",
		},
		semaphores: make(map[string]*semaphore.Weighted),
		database:   database,
	}
}

func TestNodeServer_ensurePublishingIsAllowed(t *testing.T) {
	ns := setupTestNodeServer()
	ctx := context.Background()

	volumeID := uuid.New().String()
	targetPath := "/test/path"
	node := "test-node"
	accessMode := csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER

	req := &csi.NodePublishVolumeRequest{
		VolumeId: volumeID,
		VolumeCapability: &csi.VolumeCapability{
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: accessMode,
			},
		},
		VolumeContext: map[string]string{
			"node": node,
			"pod":  "test-pod",
		},
		TargetPath: targetPath,
	}

	// trivial, new attachment and nothing else
	resp, err := ns.ensurePublishingIsAllowed(ctx, req)
	assert.NoError(t, err)
	assert.Nil(t, resp)

	// duplicate attachment, same request again but after already locking, should succeed
	ns.SetPvcLock(ctx, volumeID, targetPath, node, accessMode.String())

	resp, err = ns.ensurePublishingIsAllowed(ctx, req)
	assert.NoError(t, err)
	assert.Nil(t, resp)

	// duplicate request of same attachment with different access type, should fail
	req.VolumeCapability.AccessMode.Mode = csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY

	resp, err = ns.ensurePublishingIsAllowed(ctx, req)
	_, expectedErr := NodePublishVolumeError(ctx, codes.FailedPrecondition, "Volume already published with a different access type")
	assert.Equal(t, expectedErr, err)

	// duplicate request of same attachment with different target path, should fail due to SINGLE_NODE_SINGLE_WRITER
	req.VolumeCapability.AccessMode.Mode = csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER
	req.TargetPath = "/test/path2"
	resp, err = NodePublishVolumeError(ctx, codes.FailedPrecondition, "Volume already published with a different target path")
	_, expectedErr = ns.ensurePublishingIsAllowed(ctx, req)
	assert.Error(t, err)

}
