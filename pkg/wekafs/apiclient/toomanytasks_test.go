package apiclient

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realTooManyTasksBody is the response captured from a cluster during bulk volume provisioning,
// verbatim. Using the real shape is the point: the previous struct tag looked plausible and parsed
// nothing, so a hand-written approximation would not have caught it.
const realTooManyTasksBody = `{"message":"Operation cannot start because there are already 32 tasks running",` +
	`"data":{"tasks_num":32,"exceptionClass":["CannotStartOperationTooManyTasks","BadStateException","OperationFailedException"]}}`

func TestParseErrorCodesLiftsExceptionClasses(t *testing.T) {
	r := &ApiResponse{}
	require.NoError(t, json.Unmarshal([]byte(realTooManyTasksBody), r))

	// Before parseErrorCodes runs, nothing has read the nested classes out of Data.
	assert.Empty(t, r.ErrorCodes, "unmarshalling alone must not be expected to populate ErrorCodes")

	r.parseErrorCodes()

	assert.Equal(t,
		[]string{"CannotStartOperationTooManyTasks", "BadStateException", "OperationFailedException"},
		r.ErrorCodes)
	assert.True(t, r.HasErrorCode(ExceptionClassTooManyTasks))
	assert.False(t, r.HasErrorCode("SomeOtherException"))
}

func TestParseErrorCodesToleratesOtherShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"success with an object", `{"data":{"uid":"abc","name":"fs"}}`},
		{"success with a list", `{"data":[{"uid":"abc"},{"uid":"def"}]}`},
		{"success with a scalar", `{"data":42}`},
		{"no data key at all", `{"message":"gone"}`},
		{"null data", `{"data":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &ApiResponse{}
			require.NoError(t, json.Unmarshal([]byte(tc.body), r))
			r.parseErrorCodes() // must not panic, must not invent codes
			assert.Empty(t, r.ErrorCodes)
			assert.False(t, r.HasErrorCode(ExceptionClassTooManyTasks))
		})
	}
}

func TestHasErrorCodeOnNilResponse(t *testing.T) {
	var r *ApiResponse
	assert.False(t, r.HasErrorCode(ExceptionClassTooManyTasks), "a nil response must answer, not panic")
}

// TestIsTooManyTasksError covers the classification the retry path turns on. A full task queue has
// to be distinguishable from every other HTTP 400, because those must stay non-transient.
func TestIsTooManyTasksError(t *testing.T) {
	full := &ApiResponse{HttpStatusCode: 400}
	require.NoError(t, json.Unmarshal([]byte(realTooManyTasksBody), full))
	full.parseErrorCodes()

	otherBadRequest := &ApiResponse{HttpStatusCode: 400}
	require.NoError(t, json.Unmarshal(
		[]byte(`{"message":"bad","data":{"exceptionClass":["IllegalArgumentException"]}}`), otherBadRequest))
	otherBadRequest.parseErrorCodes()

	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"task queue full, pointer", &ApiBadRequestError{ApiResponse: full}, true},
		{"task queue full, value", ApiBadRequestError{ApiResponse: full}, true},
		{"task queue full, wrapped non-transient", ApiNonTransientError{&ApiBadRequestError{ApiResponse: full}}, true},
		{"a different bad request", &ApiBadRequestError{ApiResponse: otherBadRequest}, false},
		{"error carrying no response", &ApiBadRequestError{}, false},
		{"unrelated error type", &ApiNotFoundError{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsTooManyTasksError(tc.err))
		})
	}
}

// TestTooManyTasksBacksOffHarder pins the reason the constants differ: an ordinary transient error
// retries at a flat interval, while a full task queue - which clears only as tasks finish - waits
// longer each time, without any single wait growing past the cap.
func TestTooManyTasksBacksOffHarder(t *testing.T) {
	assert.Greater(t, RetryBackoffTooManyTasksFactor, RetryBackoffExponentialFactor,
		"a full task queue must back off faster than an ordinary transient error")

	// Worst case the retry loop can reach: every attempt doubles from the base interval, capped.
	sleep := ApiRetryIntervalSeconds
	total := 0
	for i := 1; i < ApiRetryMaxCount; i++ {
		sleep = sleep * RetryBackoffTooManyTasksFactor
		if sleep > MaxRetryBackoffTooManyTasksSeconds {
			sleep = MaxRetryBackoffTooManyTasksSeconds
		}
		total += sleep
	}
	// Jitter adds at most half of each sleep on top, so allow for that when comparing to the
	// sidecar timeout the whole operation has to fit inside.
	assert.Less(t, total+total/2, ApiHttpTimeOutSeconds,
		"retrying a full task queue must still fail inside the API timeout rather than hang")
}
