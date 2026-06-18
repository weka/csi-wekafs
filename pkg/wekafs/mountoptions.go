package wekafs

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
)

const (
	selinuxContextWekaFs     = "wekafs_csi_volume_t"
	selinuxContextNfs        = "nfs_t"
	MountOptionSyncOnClose   = "sync_on_close"
	MountOptionReadOnly      = "ro"
	MountOptionWriteCache    = "writecache"
	MountOptionCoherent      = "coherent"
	MountOptionForceDirect   = "forcedirect"
	MountOptionContainerName = "container_name"
	MountOptionAcl           = "acl"
	MountOptionNfsAsync      = "async"
	MountOptionNfsSync       = "async"
	MountOptionNfsHard       = "hard"
	MountOptionNfsNoac       = "noac"
	MountOptionNfsAc         = "ac"
	MountOptionNfsRdirPlus   = "rdirplus"
	MountOptionReadCache     = "readcache"
	MountProtocolWekafs      = "wekafs"
	MountProtocolNfs         = "nfs"
	DefaultNfsMountOptions   = MountOptionNfsHard + "," + MountOptionNfsAsync + "," + MountOptionNfsRdirPlus
)

type mountOption struct {
	option string
	value  string
}

type mutuallyExclusiveMountOptionSet []string

func (o *mountOption) String() string {
	ret := o.option
	if o.value != "" {
		ret += "=" + o.value
	}
	return ret
}

// newMountOptionFromString accepts a single mount option string from mount and parses it into separate option and optional value
func newMountOptionFromString(optstring string) mountOption {
	parts := strings.Split(optstring, "=")
	value := ""
	if len(parts) == 2 {
		value = parts[1]
	}
	return mountOption{
		option: parts[0],
		value:  value,
	}
}

type MountOptions struct {
	customOptions  map[string]mountOption
	excludeOptions []string
}

// cloneCustomOptions returns a deep copy of the customOptions map. MountOptions
// methods that derive a new value (AddOption/RemoveOption/MergedWith) MUST clone
// the map rather than share the reference: a shared map means mutating the
// "derived" options also mutates the original, which corrupts callers that use
// MountOptions.String() as a stable identity (e.g. the mount refcount key in
// getRefcountIdx).
func (opts MountOptions) cloneCustomOptions() map[string]mountOption {
	cloned := make(map[string]mountOption, len(opts.customOptions))
	for k, v := range opts.customOptions {
		cloned[k] = v
	}
	return cloned
}

// setOption adds/replaces an option in the receiver's map IN PLACE. Only safe on a
// MountOptions whose map the caller exclusively owns (a freshly constructed value,
// or an intentional in-place mutation). Derivation that must not touch the receiver
// uses AddOption instead.
func (opts MountOptions) setOption(optstring string) {
	o := newMountOptionFromString(optstring)
	opts.customOptions[o.option] = o
}

// Merge merges mount options. The other object always take precedence over the original
func (opts MountOptions) Merge(other MountOptions, exclusives []mutuallyExclusiveMountOptionSet) {
	for _, otherOpt := range other.customOptions {
		opts.customOptions[otherOpt.option] = otherOpt
		// iterate on all sets of mutually exclusive options
		for _, exclusiveOptsSet := range exclusives {
			// iterate on all options in one exclusive set
			for _, opt := range exclusiveOptsSet {
				if otherOpt.option == opt {
					// if the option exists in exclusiveOptsSet, we need to drop all its alternatives from original options
					for _, optionToDrop := range exclusiveOptsSet {
						if optionToDrop != opt {
							delete(opts.customOptions, optionToDrop)
						}
					}
					break // stop iterating on the rest
				}
			}
		}
	}

	for _, otherOpt := range other.excludeOptions {
		delete(opts.customOptions, otherOpt)
	}
}

// MergedWith returns a new object merged with other object
func (opts MountOptions) MergedWith(other MountOptions, exclusives []mutuallyExclusiveMountOptionSet) MountOptions {
	ret := MountOptions{
		customOptions:  opts.cloneCustomOptions(),
		excludeOptions: opts.excludeOptions,
	}
	ret.Merge(other, exclusives)
	return ret
}

func (opts MountOptions) AddOption(optstring string) MountOptions {
	ret := MountOptions{
		customOptions:  opts.cloneCustomOptions(),
		excludeOptions: opts.excludeOptions,
	}
	opt := newMountOptionFromString(optstring)
	ret.customOptions[opt.option] = opt
	return ret
}

func (opts MountOptions) RemoveOption(optstring string) MountOptions {
	ret := MountOptions{
		customOptions:  opts.cloneCustomOptions(),
		excludeOptions: opts.excludeOptions,
	}
	opt := newMountOptionFromString(optstring)
	delete(ret.customOptions, opt.option)
	return ret
}

func (opts MountOptions) hasOption(optstring string) bool {
	opt := newMountOptionFromString(optstring)
	_, exists := opts.customOptions[opt.option]
	return exists
}

func (opts MountOptions) getOptionValue(optstring string) string {
	opt := newMountOptionFromString(optstring)
	o, exists := opts.customOptions[opt.option]
	if exists {
		return o.value
	}
	return ""
}

func (opts MountOptions) getOpts() []mountOption {
	var ret []mountOption
	keys := make([]string, 0, len(opts.customOptions))

	for k := range opts.customOptions {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		ret = append(ret, opts.customOptions[k])
	}
	return ret
}

func (opts MountOptions) Strings() []string {
	var ret []string
	for _, o := range opts.getOpts() {
		ret = append(ret, o.String())
	}
	return ret
}

func (opts MountOptions) String() string {
	return strings.Join(opts.Strings(), ",")
}

func (opts MountOptions) Hash() uint32 {
	h := fnv.New32a()
	s := fmt.Sprintln(opts.getOpts())
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

func (opts MountOptions) AsMapKey() string {
	ret := opts
	// TODO: if adding any other version-agnostic options, add them here
	excludedOpts := []string{MountOptionSyncOnClose}
	for _, o := range excludedOpts {
		ret = ret.RemoveOption(o)
	}
	return ret.String()
}

func (opts MountOptions) AsVolumeContext() string {
	ret := opts
	excludedOpts := []string{MountOptionSyncOnClose, MountOptionContainerName}
	for _, o := range excludedOpts {
		ret = ret.RemoveOption(o)
	}
	return ret.String()
}

// setSelinux mutates the receiver's options in place (it operates directly on the
// shared customOptions map rather than via AddOption, which now returns a new value).
func (opts MountOptions) setSelinux(selinuxSupport bool, mountProtocol string) {
	if selinuxSupport {
		var o mountOption
		if mountProtocol == MountProtocolWekafs {
			o = newMountOptionFromString(fmt.Sprintf("fscontext=\"system_u:object_r:%s:s0\"", selinuxContextWekaFs))
		} else if mountProtocol == MountProtocolNfs {
			o = newMountOptionFromString(fmt.Sprintf("context=\"system_u:object_r:%s:s0\"", selinuxContextNfs))
		}
		if o.option != "" {
			opts.customOptions[o.option] = o
		}
		// due to STIG and other security requirements we need to enable ACLs otherwise mount might fail, CSI-333
		opts.setOption(MountOptionAcl)
	} else {
		if mountProtocol == MountProtocolWekafs {
			delete(opts.customOptions, "fscontext")
		}
		if mountProtocol == MountProtocolNfs {
			delete(opts.customOptions, "context")
		}
	}
}

func (opts MountOptions) AsNfs() MountOptions {
	// ret owns a freshly constructed map, so we mutate it in place via setOption
	// rather than AddOption (which would clone the map on every call).
	ret := NewMountOptionsFromString(DefaultNfsMountOptions)
	for _, o := range opts.getOpts() {
		switch o.option {
		case MountOptionWriteCache:
			ret.setOption(MountOptionNfsAsync)
		case MountOptionCoherent:
			ret.setOption(MountOptionNfsSync)
			ret.setOption(MountOptionNfsNoac)
		case MountOptionForceDirect:
			ret.setOption(MountOptionNfsSync)
			ret.setOption(MountOptionNfsNoac)
		case MountOptionReadCache:
			ret.setOption(MountOptionNfsAc)
		case "dentry_max_age_positive":
			ret.setOption(fmt.Sprintf("acdirmax=%s", o.value))
			ret.setOption(fmt.Sprintf("acregmax=%s", o.value))
		default:
			continue
		}
	}
	return ret
}

func NewMountOptionsFromString(optsString string) MountOptions {
	if optsString == "" {
		return NewMountOptions([]string{})
	}
	optstrings := strings.Split(optsString, ",")
	return NewMountOptions(optstrings)
}

func NewMountOptions(optstrings []string) MountOptions {
	ret := MountOptions{
		customOptions:  make(map[string]mountOption),
		excludeOptions: []string{},
	}
	for _, optstring := range optstrings {
		s := strings.TrimSpace(optstring)
		if s != "" {
			o := newMountOptionFromString(s)
			ret.customOptions[o.option] = o
		}
	}
	return ret
}

func getDefaultMountOptions() MountOptions {
	var defaultOptions []string

	ret := MountOptions{
		customOptions:  make(map[string]mountOption),
		excludeOptions: []string{""},
	}
	for _, optstring := range defaultOptions {
		opt := newMountOptionFromString(optstring)
		ret.customOptions[opt.option] = opt
	}
	return ret
}
