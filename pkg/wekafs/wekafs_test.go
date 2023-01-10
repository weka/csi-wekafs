package wekafs

import "testing"

func TestAsciiFilter(t *testing.T) {
	testPattern := func(s, expected string, maxLength int) {
		result := getAsciiPart(s, maxLength)
		if result != expected {
			t.Errorf("String: %s[:%d], Expected: %s != Result:%s", s, maxLength, expected, result)
		}
		t.Logf("PASS: String:%s[:%d] -> Result: %s", s, maxLength, result)
	}
	testPattern("abc:%абс", "abc:", 64)
	testPattern("+=-_", "-_", 64)
	testPattern("123/", "123", 64)
	testPattern("abcdef", "abc", 3)
	testPattern("abcdef", "abc", 3)
	testPattern("|^", "", 3)
}

func TestVolumeType(t *testing.T) {
	testPattern := func(s string, vt VolumeType) {
		str := sliceVolumeTypeFromVolumeId(s)
		if str != vt {
			t.Errorf("VolumeID: %s, FAILED: %s", s, str)
			return
		}
		t.Logf("PASS: VolumeId:%s (%s)", s, vt)
	}

	testPattern("dir/v1/filesystem/4e1243bd22c66e76c2ba9eddc1f91394e57f9f83-some_dirName", VolumeTypeDirV1)
	testPattern("dir/v1/filesystem/4e1243bd22c66e76c2ba9eddc1f91394e57f9f83/some_dirName", VolumeTypeDirV1)
	testPattern("weka/v1/filesystem", VolumeTypeUnified)
	testPattern("weka/v1/filesystem:snapshotname", VolumeTypeUnified)
	testPattern("weka/v1/filesystem:snapshotname/dirascii-some_dirName", VolumeTypeUnified)
	testPattern("weka/v1/filesystem/dirascii-some_dirName", VolumeTypeUnified)
}

func TestVolumeId(t *testing.T) {
	testPattern := func(s string) {
		err := validateVolumeId(s)
		if err != nil {
			t.Errorf("VolumeID: %s, FAILED: %s", s, err)
			return
		}
		t.Logf("PASS: VolumeId:%s (%s)", s, sliceVolumeTypeFromVolumeId(s))
	}

	testBadPattern := func(s string) {
		err := validateVolumeId(s)
		if err == nil {
			t.Errorf("VolumeID: %s (%s), FALSE PASS", s, sliceVolumeTypeFromVolumeId(s))
			return
		}
		t.Logf("PASS: VolumeId:%s, did not validate, err: %s", s, err)
	}
	// DirVolume
	testPattern("dir/v1/filesystem/4e1243bd22c66e76c2ba9eddc1f91394e57f9f83-some_dirName")
	testBadPattern("dir/v1/filesystem") // only filesystem name, no internal path
	testBadPattern("dir/v1")            // no filesystem name, only volumeType
	testBadPattern("/var/log/messages") // volumeType starts with / - bad path

	// FsVolume
	testPattern("fs/v1/filesystem") // OK
	testBadPattern("fs/v1")         // no filesystem name, only volumeType
	testBadPattern("fs/filesystem") // no version

	// FsSnap
	testBadPattern("fssnap/v1") // no filesystem and no snapshot

	testPattern("weka/v1/filesystem")
	testPattern("weka/v1/filesystem:snapshotname")
	testPattern("weka/v1/filesystem:snapshotname/dirascii-some_dirName")
	testPattern("weka/v1/filesystem/dirascii-some_dirName")
}
