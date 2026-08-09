package apiclient

import (
	"errors"
	"net/http"
	"testing"
)

// DeleteNfsPermission used to fall out of its type switch and return nil for anything it did not
// recognise, so a 500, an authorization failure, or retries exhausted against an unreachable cluster
// all reported the permission as deleted while it was still there.
//
// The two recognised cases still mean "already gone", which is the outcome the caller wants.
func TestDeleteNfsPermissionErrorClassification(t *testing.T) {
	for name, tc := range map[string]struct {
		err          error
		wantNotFound bool // maps to ObjectNotFoundError
		wantNil      bool
	}{
		"deleted successfully":  {nil, false, true},
		"already gone (404)":    {&ApiNotFoundError{}, true, false},
		"server error (500)":    {&ApiInternalError{}, false, false},
		"authorization failure": {&ApiAuthorizationError{}, false, false},
		"retries exhausted":     {&ApiRetriesExceeded{Retries: 5}, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			got := classifyNfsPermissionDeleteError(tc.err)
			switch {
			case tc.wantNil && got != nil:
				t.Errorf("got %v, want nil", got)
			case tc.wantNotFound && !errors.Is(got, ObjectNotFoundError):
				t.Errorf("got %v, want ObjectNotFoundError", got)
			case !tc.wantNil && !tc.wantNotFound && got == nil:
				t.Error("returned nil - a real failure was reported as a successful deletion")
			}
		})
	}
}

// A BadRequest naming the permission as absent is the cluster's other way of saying "already gone";
// any other BadRequest is a genuine failure and must not be swallowed.
func TestDeleteNfsPermissionBadRequestCodes(t *testing.T) {
	absent := &ApiBadRequestError{
		ApiResponse: &ApiResponse{ErrorCodes: []string{"PermissionDoesNotExistException"}},
		StatusCode:  http.StatusBadRequest,
	}
	if got := classifyNfsPermissionDeleteError(absent); !errors.Is(got, ObjectNotFoundError) {
		t.Errorf("PermissionDoesNotExistException gave %v, want ObjectNotFoundError", got)
	}

	other := &ApiBadRequestError{
		ApiResponse: &ApiResponse{ErrorCodes: []string{"SomethingElseEntirely"}},
		StatusCode:  http.StatusBadRequest,
	}
	if got := classifyNfsPermissionDeleteError(other); got == nil || errors.Is(got, ObjectNotFoundError) {
		t.Errorf("unrelated BadRequest gave %v, want the original error", got)
	}
}
