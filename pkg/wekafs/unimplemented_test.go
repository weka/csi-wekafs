package wekafs

import (
	"context"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The RPCs the driver does not implement must decline, not panic. A panic in a gRPC handler takes
// down the whole controller or node process, and with it every other volume operation in flight on
// that pod - so an unsupported call from a container orchestrator or a sidecar probing for a
// capability would be a fleet-wide outage rather than one failed request.
//
// The servers are constructed bare on purpose: these handlers must not touch driver state, which is
// also what lets them answer safely before the driver is fully wired up.
func TestUnimplementedRPCsDeclineInsteadOfPanicking(t *testing.T) {
	ctx := context.Background()
	cs := &ControllerServer{}
	ns := &NodeServer{}

	calls := map[string]func() error{
		"ControllerPublishVolume": func() error {
			_, err := cs.ControllerPublishVolume(ctx, &csi.ControllerPublishVolumeRequest{})
			return err
		},
		"ControllerUnpublishVolume": func() error {
			_, err := cs.ControllerUnpublishVolume(ctx, &csi.ControllerUnpublishVolumeRequest{})
			return err
		},
		"GetCapacity": func() error {
			_, err := cs.GetCapacity(ctx, &csi.GetCapacityRequest{})
			return err
		},
		"ControllerModifyVolume": func() error {
			_, err := cs.ControllerModifyVolume(ctx, &csi.ControllerModifyVolumeRequest{})
			return err
		},
		"ListSnapshots": func() error {
			_, err := cs.ListSnapshots(ctx, &csi.ListSnapshotsRequest{})
			return err
		},
		"NodeStageVolume": func() error {
			_, err := ns.NodeStageVolume(ctx, &csi.NodeStageVolumeRequest{})
			return err
		},
		"NodeUnstageVolume": func() error {
			_, err := ns.NodeUnstageVolume(ctx, &csi.NodeUnstageVolumeRequest{})
			return err
		},
		"NodeExpandVolume": func() error {
			_, err := ns.NodeExpandVolume(ctx, &csi.NodeExpandVolumeRequest{})
			return err
		},
	}

	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			// A panic here fails this subtest rather than tearing down the whole run, so one
			// regression does not hide the state of the others.
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panicked instead of returning an error: %v", r)
				}
			}()

			err := call()
			if err == nil {
				t.Fatal("returned no error, but the RPC is not implemented")
			}
			if got := status.Code(err); got != codes.Unimplemented {
				t.Errorf("returned code %v, want %v - a CO only knows to stop asking on Unimplemented",
					got, codes.Unimplemented)
			}
		})
	}
}

// Every RPC above must stay absent from the advertised capabilities: advertising one the driver
// answers with Unimplemented would have the CO call it and fail, which is the contradiction this
// pairing exists to prevent.
func TestUnimplementedRPCsAreNotAdvertised(t *testing.T) {
	forbiddenController := []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
		csi.ControllerServiceCapability_RPC_GET_CAPACITY,
		csi.ControllerServiceCapability_RPC_LIST_SNAPSHOTS,
		csi.ControllerServiceCapability_RPC_MODIFY_VOLUME,
	}
	// Snapshot and clone support are config-gated, so a zero config would leave the capability list
	// at its smallest and make this pass without checking much. Turn on everything that can be
	// turned on without a live manager, so the assertion runs against the widest set the driver
	// ever advertises.
	maximal := &DriverConfig{advertiseSnapshotSupport: true, advertiseVolumeCloneSupport: true}
	cs := NewControllerServer("test-node", nil, nil, maximal, nil)
	if len(cs.caps) == 0 {
		t.Fatal("no controller capabilities advertised - this test would pass without checking anything")
	}
	for _, c := range cs.caps {
		for _, forbidden := range forbiddenController {
			if c.GetRpc().GetType() == forbidden {
				t.Errorf("controller advertises %v, but the RPC returns Unimplemented", forbidden)
			}
		}
	}

	ns := NewNodeServer("test-node", 0, nil, nil, maximal)
	if len(ns.caps) == 0 {
		t.Fatal("no node capabilities advertised - this test would pass without checking anything")
	}
	for _, c := range ns.caps {
		if c.GetRpc().GetType() == csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME ||
			c.GetRpc().GetType() == csi.NodeServiceCapability_RPC_EXPAND_VOLUME {
			t.Errorf("node advertises %v, but the RPC returns Unimplemented", c.GetRpc().GetType())
		}
	}
}
