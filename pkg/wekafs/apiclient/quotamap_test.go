package apiclient

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The Weka API spells the collection "fileSystems". A hand-assembled path here once said
// "filesystems", which every other request in this client would have contradicted and which would
// have 404'd the first time the bulk fetch ran. Pinned against the canonical helper rather than
// against a literal, so the two cannot drift apart again.
func TestQuotaListRequestUsesTheCanonicalFilesystemPath(t *testing.T) {
	fsUid := uuid.New()
	req := &QuotaListRequest{FilesystemUid: fsUid}

	got := req.getApiUrl(nil)
	want := (&Quota{FilesystemUid: fsUid}).GetBasePath(nil)

	if got != want {
		t.Errorf("expected the list path to match Quota's own base path\n  got:  %s\n  want: %s", got, want)
	}
	if !strings.Contains(got, "fileSystems") {
		t.Errorf("expected the API's camelCase collection name, got %s", got)
	}
}
