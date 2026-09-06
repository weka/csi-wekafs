/*
Copyright 2017 The Kubernetes Authors.

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
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	v1 "k8s.io/api/core/v1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// applyNodeLabels fetches nodeName through client and ensures all of desired are present on it, issuing
// an Update only if something is actually missing or different. client is d.manager.GetClient(), whose
// cache for the Node type is scoped to this node alone (see initManager in driver.go), so this Get costs
// no API round trip once the informer has synced - safe to call on every 10s Probe. Exactly one Get, and
// at most one Update.
func applyNodeLabels(ctx context.Context, client runtimeclient.Client, nodeName string, desired map[string]string) error {
	node := &v1.Node{}
	if err := client.Get(ctx, runtimeclient.ObjectKey{Name: nodeName}, node); err != nil {
		return fmt.Errorf("failed to get node object from Kubernetes: %w", err)
	}

	if node.Labels == nil {
		node.Labels = make(map[string]string, len(desired))
	}

	updateNeeded := false
	for label, value := range desired {
		if existing, ok := node.Labels[label]; !ok || existing != value {
			log.Info().Str("label", fmt.Sprintf("%s=%s", label, value)).Str("node", node.Name).Msg("Setting label on node")
			node.Labels[label] = value
			updateNeeded = true
		}
	}

	if !updateNeeded {
		return nil
	}

	if err := client.Update(ctx, node); err != nil {
		return fmt.Errorf("failed to update node labels: %w", err)
	}
	log.Info().Msg("Successfully updated labels on node")
	return nil
}

// removeNodeLabels fetches nodeName through client and deletes labelsToRemove from it, always issuing an
// Update - this runs on start/stop, not per Probe, so correctness (removing stale labels even if some
// were already gone) matters more here than shaving off an Update.
func removeNodeLabels(ctx context.Context, client runtimeclient.Client, nodeName string, labelsToRemove []string) error {
	node := &v1.Node{}
	if err := client.Get(ctx, runtimeclient.ObjectKey{Name: nodeName}, node); err != nil {
		return fmt.Errorf("failed to get node: %w", err)
	}

	for _, label := range labelsToRemove {
		delete(node.Labels, label)
		log.Info().Str("label", label).Str("node", node.Name).Msg("Removing label from node")
	}

	if err := client.Update(ctx, node); err != nil {
		return fmt.Errorf("failed to update node labels: %w", err)
	}
	return nil
}

// SetNodeLabels applies this node's topology/transport labels via the controller-runtime manager's
// cached client. It is a no-op - logged, not panicking - if the manager was never initialized (e.g.
// initManager failed and only logged a warning; see Run() in driver.go).
// SetNodeLabels records this node's topology labels. wekafsAvailable is passed in rather than
// determined here: the only caller is Probe, which has just established it as wekafsReady, and
// asking again meant reading and parsing the driver's frontend list twice per probe on every node,
// forever. On the path that reaches the transport decision below - not dev mode, since this returns
// early there, and not forced to NFS, which is checked first - wekafsReady means exactly what this
// needs: the Weka client is up.
func (d *WekaFsDriver) SetNodeLabels(ctx context.Context, wekafsAvailable bool) {
	if d.config.isInDevMode() {
		return
	}

	if d.csiMode != CsiModeNode {
		return
	}

	if d.manager == nil {
		log.Ctx(ctx).Warn().Msg("Kubernetes manager not initialized, skipping node label update")
		return
	}

	transport := func() string {
		if d.config.useNfs {
			return "nfs"
		}
		if d.config.allowNfsFailback && !wekafsAvailable {
			return "nfs"
		}
		return "wekafs"
	}()

	desired := map[string]string{
		TopologyKeyNode: d.nodeID,
		fmt.Sprintf(TopologyLabelNodePattern, d.name):      d.nodeID,
		fmt.Sprintf(TopologyLabelWekaLocalPattern, d.name): "true",
		fmt.Sprintf(TopologyLabelTransportPattern, d.name): transport,
	}

	if err := applyNodeLabels(ctx, d.manager.GetClient(), d.nodeID, desired); err != nil {
		log.Ctx(ctx).Error().Err(err).Msg("Failed to set node labels")
	}
}

// managedNodeLabelKeys returns the full set of node label keys CleanupNodeLabels removes for driverName:
// the four per-driver keys SetNodeLabels actively manages (TopologyKeyNode plus the three
// TopologyLabel*Pattern keys with driverName substituted in), plus two now-legacy global label keys
// kept here purely for cleanup, so a node labeled by an older driver version does not retain them
// across an upgrade.
func managedNodeLabelKeys(driverName string) []string {
	patterned := []string{TopologyLabelNodePattern, TopologyLabelTransportPattern, TopologyLabelWekaLocalPattern}
	for i, pattern := range patterned {
		patterned[i] = fmt.Sprintf(pattern, driverName)
	}
	return append(patterned, TopologyKeyNode, TopologyLabelTransportGlobal, TopologyLabelNodeGlobal)
}

// CleanupNodeLabels removes this driver's managed node-topology labels via the controller-runtime
// manager's cached client. It is a no-op - logged, not panicking - if the manager was never initialized.
func (d *WekaFsDriver) CleanupNodeLabels(ctx context.Context) {
	if d.config.isInDevMode() {
		return
	}

	if d.manager == nil {
		log.Ctx(ctx).Warn().Msg("Kubernetes manager not initialized, skipping node label cleanup")
		return
	}

	if err := removeNodeLabels(ctx, d.manager.GetClient(), d.nodeID, managedNodeLabelKeys(d.name)); err != nil {
		log.Ctx(ctx).Error().Err(err).Msg("Failed to remove node labels")
		return
	}

	log.Info().Msg("Successfully removed labels from node")
}

// readNodeTopologyLabels reads the standard topology.kubernetes.io/zone and region
// labels from the Kubernetes node object and stores them on the NodeServer.
// Called once at startup before gRPC registration.
func (d *WekaFsDriver) readNodeTopologyLabels(ctx context.Context) {
	if d.ns == nil || d.manager == nil {
		return
	}
	// Use the API reader (direct client) instead of the cached client because
	// this runs before the manager is started and its informer cache is synced.
	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	node := &v1.Node{}
	if err := d.manager.GetAPIReader().Get(readCtx, runtimeclient.ObjectKey{Name: d.nodeID}, node); err != nil {
		log.Warn().Err(err).Msg("Failed to get node object for reading topology labels")
		return
	}
	if zone, ok := node.Labels[TopologyKeyZone]; ok {
		d.ns.zone = zone
	}
	if region, ok := node.Labels[TopologyKeyRegion]; ok {
		d.ns.region = region
	}
	log.Info().Str("zone", d.ns.zone).Str("region", d.ns.region).Msg("Read standard topology labels from node")
}
