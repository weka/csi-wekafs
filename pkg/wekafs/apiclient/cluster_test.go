package apiclient

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetFrontendsFromDriver(t *testing.T) {
	testCases := []struct {
		name              string
		fileContent       string
		expectedCount     int
		expectedConnected int
		expectError       bool
	}{
		{
			name: "single frontend connected",
			fileContent: `IO node version ba1c189f860b10eb
GW driver_state: DRIVER_ACCEPTING
Active mounts: 0
Container=c2d49fce12edclient FE 0: Connected frontend pid 166696
Error counters
NS_num_enospc_errors: 0
`,
			expectedCount:     1,
			expectedConnected: 1,
			expectError:       false,
		},
		{
			name: "multiple frontends all connected",
			fileContent: `IO node version 2e694b326b50d08a
GW driver_state: DRIVER_ACCEPTING
Active mounts: 4
Container=c88742862af5client FE 0: Connected frontend pid 4081176
Container=c88742862af5client FE 1: Connected frontend pid 4081177
Error counters
NS_num_enospc_errors: 0
`,
			expectedCount:     2,
			expectedConnected: 2,
			expectError:       false,
		},
		{
			name: "one frontend disconnected",
			fileContent: `IO node version 2e694b326b50d08a
Container=client1 FE 0: Connected frontend pid 1234
Container=client1 FE 1: Frontend is not connected
`,
			expectedCount:     2,
			expectedConnected: 1,
			expectError:       false,
		},
		{
			name: "all frontends disconnected",
			fileContent: `IO node version 2e694b326b50d08a
Container=client1 FE 0: Frontend is not connected
Container=client1 FE 1: Frontend is not connected
`,
			expectedCount:     2,
			expectedConnected: 0,
			expectError:       false,
		},
		{
			name: "no frontends - driver accepting but no containers",
			fileContent: `IO node version 2e694b326b50d08a
GW driver_state: DRIVER_ACCEPTING
Active mounts: 0
Error counters
NS_num_enospc_errors: 0
`,
			expectedCount:     0,
			expectedConnected: 0,
			expectError:       false,
		},
		{
			name: "multiple containers mixed state",
			fileContent: `Container=clientA FE 0: Connected frontend pid 111
Container=clientA FE 1: Connected frontend pid 222
Container=clientB FE 0: Frontend is not connected
Container=clientB FE 1: Connected frontend pid 333
`,
			expectedCount:     4,
			expectedConnected: 3,
			expectError:       false,
		},
		{
			name:              "empty file",
			fileContent:       "",
			expectedCount:     0,
			expectedConnected: 0,
			expectError:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create temp file with test content
			tmpFile, err := os.CreateTemp("", "proc_wekafs_*")
			assert.NoError(t, err)
			defer os.Remove(tmpFile.Name())

			_, _ = tmpFile.WriteString(tc.fileContent)
			_ = tmpFile.Close()

			// Override the path
			origPath := ProcFsPath
			ProcFsPath = tmpFile.Name()
			defer func() { ProcFsPath = origPath }()

			frontends, err := GetFrontendsFromDriver(context.Background())

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Len(t, frontends, tc.expectedCount)

			connected := 0
			for _, f := range frontends {
				if f.Connected {
					connected++
				}
			}
			assert.Equal(t, tc.expectedConnected, connected)
		})
	}
}

func TestGetFrontendsFromDriver_FileNotFound(t *testing.T) {
	origPath := ProcFsPath
	ProcFsPath = "/nonexistent/path/to/interface"
	defer func() { ProcFsPath = origPath }()

	_, err := GetFrontendsFromDriver(context.Background())
	assert.Error(t, err)
}

func TestGetFrontendsFromDriver_ParsesContainerDetails(t *testing.T) {
	fileContent := `Container=mycontainer FE 2: Connected frontend pid 12345
`
	tmpFile, err := os.CreateTemp("", "proc_wekafs_*")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, _ = tmpFile.WriteString(fileContent)
	_ = tmpFile.Close()

	origPath := ProcFsPath
	ProcFsPath = tmpFile.Name()
	defer func() { ProcFsPath = origPath }()

	frontends, err := GetFrontendsFromDriver(context.Background())
	assert.NoError(t, err)
	assert.Len(t, frontends, 1)

	fe := frontends[0]
	assert.Equal(t, "mycontainer", fe.ContainerName)
	assert.Equal(t, 2, fe.ContainerId)
	assert.Equal(t, 12345, fe.Pid)
	assert.True(t, fe.Connected)
}
