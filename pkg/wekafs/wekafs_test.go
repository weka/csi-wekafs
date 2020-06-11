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
	testPattern := func(s string) {
		str := GetVolumeType(s)
		if str != VolumeTypeDirV1 {
			t.Errorf("VolumeID: %s, FAILED: %s", s, str)
			return
		}
		t.Logf("PASS: VolumeId:%s", s)
	}

	testPattern("dir/v1/filesystem/4e1243bd22c66e76c2ba9eddc1f91394e57f9f83-some_dirName")
	testPattern("dir/v1/filesystem/4e1243bd22c66e76c2ba9eddc1f91394e57f9f83/some_dirName")
}

func TestVolumeId(t *testing.T) {
	testPattern := func(s string) {
		err := validateVolumeId(s)
		if err != nil {
			t.Errorf("VolumeID: %s, FAILED: %s", s, err)
			return
		}
		t.Logf("PASS: VolumeId:%s", s)
	}

	testBadPattern := func(s string) {
		err := validateVolumeId(s)
		if err == nil {
			t.Errorf("VolumeID: %s, FALSE PASS", s)
			return
		}
		t.Logf("PASS: VolumeId:%s, did not validate, err: %s", s, err)
	}
	testPattern("dir/v1/filesystem/4e1243bd22c66e76c2ba9eddc1f91394e57f9f83-some_dirName")
	testBadPattern("dir/v1/filesystem/4e1243bd22c66e76c2ba9eddc1f91394e57f9f83/some_dirName")
}
