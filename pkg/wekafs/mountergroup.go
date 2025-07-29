package wekafs

import (
	"context"

	"github.com/rs/zerolog/log"
)

type MounterGroup struct {
	nfs    AnyMounter
	wekafs AnyMounter
}

func NewMounterGroup(ctx context.Context, driver *WekaFsDriver) *MounterGroup {
	ret := &MounterGroup{}
	log.Info().Msg("Configuring Mounter Group")
	ret.nfs = newNfsMounter(ctx, driver)
	ret.wekafs = newWekafsMounter(ctx, driver)

	if driver.config.useNfs {
		log.Warn().Msg("Enforcing NFS transport due to configuration")
		ret.nfs.Enable()
		ret.wekafs.Disable()

	} else if driver.config.allowNfsFailback {
		ret.nfs.Enable()
		if !isWekaRunning(ctx) {
			ret.wekafs.Disable()
			log.Warn().Msg("Weka Driver not found. Failing back to NFS transport")
		}
	} else if !isWekaRunning(ctx) {
		ret.wekafs.Disable()
		log.Warn().Msg("Weka Driver not found. Failing back to NFS transport")
	}
	return ret
}

func (mg *MounterGroup) GetMounterByTransport(ctx context.Context, transport DataTransport) AnyMounter {
	logger := log.Ctx(ctx)
	if transport == dataTransportNfs {
		return mg.nfs
	} else if transport == dataTransportWekafs {
		return mg.wekafs
	} else {
		logger.Error().Msgf("Unknown transport type: %s", transport)
		return nil
	}
}

func (mg *MounterGroup) GetPreferredMounter(ctx context.Context) AnyMounter {
	for _, t := range TransportPreference {
		m := mg.GetMounterByTransport(ctx, t)
		if m.isEnabled() {
			return m
		}
	}
	log.Ctx(ctx).Error().Msg("No enabled mounter found")
	return nil
}
