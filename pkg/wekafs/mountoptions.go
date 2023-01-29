package wekafs

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
)

const (
	selinuxContext = "wekafs_csi_volume"
)

type MountOptions struct {
	customOptions  []string
	excludeOptions []string
	xattr          bool
	selinuxSupport bool
}

// Merge merges mount options. The other object always take precedence over the original
func (o *MountOptions) Merge(other *MountOptions) {
	if other == nil {
		return
	}

	for _, otherOpt := range other.customOptions {
		for _, opt := range o.customOptions {
			if opt == otherOpt {
				break
			}
			o.customOptions = append(o.customOptions, otherOpt)
		}
	}

	o.xattr = other.xattr
	o.selinuxSupport = other.selinuxSupport

	var removeIndexes []int
	for _, otherOpt := range other.excludeOptions {
		for i, opt := range o.customOptions {
			if opt == otherOpt {
				removeIndexes = append(removeIndexes, i)
			}
		}
	}
	for _, i := range removeIndexes {
		o.customOptions = append(o.customOptions[:i], o.customOptions[i+1:]...)
	}
}

// MergedWith returns a new object merged with other object
func (o *MountOptions) MergedWith(other *MountOptions) *MountOptions {
	ret := &MountOptions{
		customOptions:  o.customOptions,
		excludeOptions: o.excludeOptions,
		xattr:          o.xattr,
		selinuxSupport: o.selinuxSupport,
	}
	ret.Merge(other)

	return ret
}

func (o *MountOptions) getOpts() []string {
	ret := o.customOptions
	sort.Strings(ret)
	if o.xattr {
		ret = append(ret, "acl")
	}
	if o.selinuxSupport {
		ret = append(ret, fmt.Sprintf("fscontext=\"system_u:object_r:%s_t:s0\"", selinuxContext))
	}
	return ret
}

func (o *MountOptions) String() string {
	return strings.Join(o.getOpts(), ",")
}

func (o *MountOptions) Hash() uint32 {
	h := fnv.New32a()
	s := fmt.Sprintln(o.getOpts())
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

func getDefaultMountOptions() *MountOptions {
	return &MountOptions{
		customOptions:  []string{""},
		excludeOptions: nil,
		xattr:          false,
		selinuxSupport: false,
	}

}
