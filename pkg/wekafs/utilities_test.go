package wekafs

import (
	"github.com/stretchr/testify/assert"
	"os"
	"testing"
)

func TestGetMountContainerNameFromActualMountPoint(t *testing.T) {
	// Create a temporary file to mock /proc/mounts
	tmpFile, err := os.CreateTemp("", "mounts")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	// Write mock data to the temporary file
	mockData := `
dev/sdc1 /boot/efi vfat rw,relatime,fmask=0077,dmask=0077,codepage=437,iocharset=ascii,shortname=winnt,errors=remount-ro 0 0
fusectl /sys/fs/fuse/connections fusectl rw,relatime 0 0
binfmt_misc /proc/sys/fs/binfmt_misc binfmt_misc rw,relatime 0 0
/etc/auto.misc /misc autofs rw,relatime,fd=7,pgrp=2304,timeout=300,minproto=5,maxproto=5,indirect,pipe_ino=34520 0 0
-hosts /net autofs rw,relatime,fd=13,pgrp=2304,timeout=300,minproto=5,maxproto=5,indirect,pipe_ino=36373 0 0
/etc/auto.weka-smb /wekasmb autofs rw,relatime,fd=19,pgrp=2304,timeout=300,minproto=5,maxproto=5,indirect,pipe_ino=34528 0 0
/etc/auto.weka-smb /wekasmb-persistent autofs rw,relatime,fd=25,pgrp=2304,timeout=0,minproto=5,maxproto=5,indirect,pipe_ino=34531 0 0
/etc/auto.weka-kw /wekakwfs autofs rw,relatime,fd=31,pgrp=2304,timeout=300,minproto=5,maxproto=5,indirect,pipe_ino=34535 0 0
/etc/auto.weka-kw /wekakwfs-persistent autofs rw,relatime,fd=37,pgrp=2304,timeout=0,minproto=5,maxproto=5,indirect,pipe_ino=35191 0 0
tmpfs /run/user/0 tmpfs rw,nosuid,nodev,relatime,size=2238124k,mode=700 0 0
10.108.97.126/default /mnt/weka wekafs rw,relatime,writecache,inode_bits=auto,readahead_kb=32768,dentry_max_age_positive=1000,dentry_max_age_negative=0,container_name=client 0 0
10.108.97.126/default /mnt/weka wekafs rw,relatime,writecache,inode_bits=auto,readahead_kb=32768,dentry_max_age_positive=1000,dentry_max_age_negative=0,container_name=client 0 0
default /run/weka-fs-mounts/default-DTQLAJ6KO6IUCZE23RBIM26YYUQNWKST-42b24381dc12client wekafs rw,relatime,writecache,inode_bits=auto,readahead_kb=32768,dentry_max_age_positive=1000,dentry_max_age_negative=0,container_name=42b24381dc12client 0 0
default /run/weka-fs-mounts/default-DTQLAJ6KO6IUCZE23RBIM26YYUQNWKKK wekafs rw,relatime,writecache,inode_bits=auto,readahead_kb=32768,dentry_max_age_positive=1000,dentry_max_age_negative=0 0 0
default /run/weka-fs-mounts/default-DTQLAJ6KO6IUCZE23RBIM26YYUQNWKSS wekafs rw,relatime,writecache,inode_bits=auto,readahead_kb=32768,dentry_max_age_positive=1000,dentry_max_age_negative=0,container_name=containername 0 0
default /run/weka-fs-mounts/default-DTQLAJ6KO6IUCZE23RBIM26YYUQNWKAA-mystrangeclient wekafs rw,relatime,writecache,inode_bits=auto,readahead_kb=32768,dentry_max_age_positive=1000,dentry_max_age_negative=0 0 0

`
	_, err = tmpFile.WriteString(mockData)
	assert.NoError(t, err)
	tmpFile.Close()

	// Redirect the function to read from the temporary file
	originalProcMountsPath := ProcMountsPath
	defer func() { ProcMountsPath = originalProcMountsPath }()
	ProcMountsPath = tmpFile.Name()

	// Call the function and check the result
	containerName, err := GetMountContainerNameFromActualMountPoint("/mnt/weka")
	assert.NoError(t, err)
	assert.Equal(t, "client", containerName)

	containerName, err = GetMountContainerNameFromActualMountPoint("/run/weka-fs-mounts/default-DTQLAJ6KO6IUCZE23RBIM26YYUQNWKST-42b24381dc12client")
	assert.NoError(t, err)
	assert.Equal(t, "42b24381dc12client", containerName)

	containerName, err = GetMountContainerNameFromActualMountPoint("/run/weka-fs-mounts/default-DTQLAJ6KO6IUCZE23RBIM26YYUQNWKKK")
	assert.NoError(t, err)
	assert.Equal(t, "", containerName)

	containerName, err = GetMountContainerNameFromActualMountPoint("/run/weka-fs-mounts/default-DTQLAJ6KO6IUCZE23RBIM26YYUQNWKSS")
	assert.NoError(t, err)
	assert.Equal(t, "containername", containerName)

	containerName, err = GetMountContainerNameFromActualMountPoint("/run/weka-fs-mounts/default-DTQLAJ6KO6IUCZE23RBIM26YYUQNWKSS-NONEXISTENT")
	assert.Error(t, err)
	assert.Equal(t, "", containerName)

	containerName, err = GetMountContainerNameFromActualMountPoint("/run/weka-fs-mounts/default-DTQLAJ6KO6IUCZE23RBIM26YYUQNWKAA-mystrangeclient")
	assert.NoError(t, err)
	assert.Equal(t, "", containerName)

	containerName, err = GetMountContainerNameFromActualMountPoint("/run/weka-fs-mounts/default-NONEXISTENT")
	assert.Error(t, err)
	assert.Equal(t, "", containerName)

}

func TestHashToValidConfigMapKey(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"example", "vynezyjzjomfh7ad67odhnkjnznxyup4"},
		{"anotherExample", "vofn7cikcmoojakjzmh47cz6w4zti7dm"},
		{"yetAnotherExample", "vvxauaoztcqrolsxl2f3jngt3n6khbf2"},
		{"pv-45f5fca8-2b1f-42f6-811c-17a8c27584c5", "vacka6yjtwgrebyscwb6lrrpdiknc3js"},
		{"pv-45f5fca9-2b1f-42f6-811c-17a8c27584c5", "vxa7cmihpgur7jmwockf43nauawu6n5s"},
		{"pv-45f5fca8-2b1a-42f6-811c-17a8c27584c5", "v6ho3xvfiyc4j3hgz4xy6jv7rxhaldbz"},
		{"pv-45f5fca8-2b1f-42fa-811c-17a8c27584c5", "v32ojj3tv6ijzoepbs5uxcg3gn3xnnqx"},
		{"pv-45f5fca8-2b1f-42f6-811d-17a8c27584c5", "v5ddxghhrbzz4l7d3ddfi5jndbeuw5ex"},
		{"pv-45f5fca8-2b1f-42f6-811c-17a8c27584c6", "vqn3maadqgwtztmqsiudvo5vrs74jlca"},
	}

	for _, tc := range testCases {
		result := HashToValidConfigMapKey(tc.input)
		assert.Equal(t, tc.expected, result, "Expected %s but got %s", tc.expected, result)
	}
}
