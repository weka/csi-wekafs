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

	"github.com/rs/zerolog/log"
)

// MounterGroup holds every transport's mounter for the life of the process, rather than the driver
// choosing one at startup. A node can then serve some volumes over native wekafs and others over
// NFS at the same time - which is what talking to two clusters over different protocols requires -
// and a volume that was mounted over one transport is always unmounted through that same mounter,
// however the driver's preference may have shifted since.
//
// Both mounters are constructed either way. Which of them may be used is a runtime property, held on
// the mounter itself and flipped by Probe as the Weka client comes and goes underneath.
type MounterGroup struct {
	nfs    AnyMounter
	wekafs AnyMounter
}

func NewMounterGroup(ctx context.Context, driver *WekaFsDriver) *MounterGroup {
	logger := log.Ctx(ctx)
	logger.Info().Msg("Configuring mounter group")

	mg := &MounterGroup{
		nfs:    newNfsMounter(ctx, driver),
		wekafs: newWekafsMounter(ctx, driver),
	}

	// The starting state only; Probe re-evaluates it on every call.
	switch {
	case driver.config.useNfs:
		logger.Warn().Msg("Enforcing NFS transport due to configuration")
		mg.nfs.Enable()
		mg.wekafs.Disable()
	case driver.config.allowNfsFailback && !isWekaRunning(ctx):
		logger.Warn().Msg("Weka driver not found, failing back to NFS transport")
		mg.nfs.Enable()
		mg.wekafs.Disable()
	default:
		// NFS stays available so a volume explicitly provisioned over it can still be served; wekafs
		// is simply preferred for anything that does not ask for a transport.
		mg.wekafs.Enable()
		if driver.config.allowNfsFailback {
			mg.nfs.Enable()
		} else {
			mg.nfs.Disable()
		}
	}

	logger.Info().
		Bool("wekafs_enabled", mg.wekafs.isEnabled()).
		Bool("nfs_enabled", mg.nfs.isEnabled()).
		Msg("Mounter group configured")
	return mg
}

// GetMounterByTransport returns the mounter for exactly this transport, enabled or not. Unmounting
// has to reach a disabled mounter: a volume mounted over NFS before a failback still has to be
// unmounted over NFS, and refusing here would strand the mount.
func (mg *MounterGroup) GetMounterByTransport(ctx context.Context, transport DataTransport) AnyMounter {
	switch transport {
	case dataTransportNfs:
		return mg.nfs
	case dataTransportWekafs:
		return mg.wekafs
	default:
		log.Ctx(ctx).Error().Str("transport", string(transport)).Msg("Unknown transport requested")
		return nil
	}
}

// GetPreferredMounter returns the first enabled mounter in TransportPreference order, for callers
// that are creating a mount rather than reusing one and so have no transport to honour.
func (mg *MounterGroup) GetPreferredMounter(ctx context.Context) AnyMounter {
	for _, transport := range TransportPreference {
		if m := mg.GetMounterByTransport(ctx, transport); m != nil && m.isEnabled() {
			return m
		}
	}
	log.Ctx(ctx).Error().Msg("No enabled mounter available for any transport")
	return nil
}

// nodeTransportLabel names the transport a node advertises, or "" when it has none to advertise.
//
// Both the CSI topology segment in NodeGetInfo and the Kubernetes node label are answered from here,
// so the two cannot disagree. They used to be derived separately: NodeGetInfo from the mounter Probe
// had just enabled, and SetNodeLabels from a second isWekaRunning call of its own, on a context
// carrying no probe timeout. Probe's answer is the one with consequences, since it is what enables
// and disables the mounters - so a client that was alive but slower than healthProbeWekaTimeout
// could leave the node labelled wekafs while its wekafs mounter sat disabled, attracting exactly the
// workloads it had just stopped being able to mount.
//
// It also stops the node asking the same question twice per probe. isWekaRunning reads and parses
// the driver's frontend list, and every probe did that twice, ten seconds apart, on every node,
// forever - visible at trace level as "All frontends connected" printed twice a few milliseconds
// apart on every cycle.
func nodeTransportLabel(ctx context.Context, mounters *MounterGroup) string {
	if mounters == nil {
		return ""
	}
	if m := mounters.GetPreferredMounter(ctx); m != nil {
		return string(m.getTransport())
	}
	return ""
}

// setMounterEnabled flips a mounter and logs only the transitions, so a Probe running every couple of
// seconds does not fill the log with the state it was already in.
func setMounterEnabled(m AnyMounter, enabled bool) {
	if m == nil || m.isEnabled() == enabled {
		return
	}
	if enabled {
		m.Enable()
		log.Info().Str("transport", string(m.getTransport())).Msg("Transport enabled")
		return
	}
	m.Disable()
	log.Warn().Str("transport", string(m.getTransport())).Msg("Transport disabled")
}
