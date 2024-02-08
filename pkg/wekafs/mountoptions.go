package wekafs

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
)

const (
	selinuxContext         = "wekafs_csi_volume"
	MountOptionSyncOnClose = "sync_on_close"
	MountOptionReadOnly    = "ro"
	MountOptionWriteCache  = "writecache"
	MountOptionCoherent    = "coherent"
	MountOptionReadCache   = "readcache"
	MountOptionAcl         = "acl"
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
		customOptions:  opts.customOptions,
		excludeOptions: opts.excludeOptions,
	}
	ret.Merge(other, exclusives)
	return ret
}

func (opts MountOptions) AddOption(optstring string) MountOptions {
	ret := MountOptions{
		customOptions:  opts.customOptions,
		excludeOptions: opts.excludeOptions,
	}
	opt := newMountOptionFromString(optstring)
	ret.customOptions[opt.option] = opt
	return ret
}

func (opts MountOptions) RemoveOption(optstring string) MountOptions {
	ret := MountOptions{
		customOptions:  opts.customOptions,
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

func (opts MountOptions) setSelinux(selinuxSupport bool) {
	if selinuxSupport {
		o := newMountOptionFromString(fmt.Sprintf("fscontext=\"system_u:object_r:%s_t:s0\"", selinuxContext))
		opts.customOptions[o.option] = o
		opts.AddOption(MountOptionAcl)
	} else {
		delete(opts.customOptions, "fscontext")
	}
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
		excludeOptions: []string{},
	}
	for _, optstring := range defaultOptions {
		opt := newMountOptionFromString(optstring)
		ret.customOptions[opt.option] = opt
	}
	return ret
}
