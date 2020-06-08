package wekafs

import "testing"

func TestAsciiFilter(t *testing.T) {
	testPattern := func(s, expected string, maxLength int) {
		result := getAsciiPart(s, maxLength)
		if result != expected{
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
