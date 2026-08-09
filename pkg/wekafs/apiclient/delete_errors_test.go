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
			got := classifyDeleteError(tc.err, "PermissionDoesNotExistException")
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
	if got := classifyDeleteError(absent, "PermissionDoesNotExistException"); !errors.Is(got, ObjectNotFoundError) {
		t.Errorf("PermissionDoesNotExistException gave %v, want ObjectNotFoundError", got)
	}

	other := &ApiBadRequestError{
		ApiResponse: &ApiResponse{ErrorCodes: []string{"SomethingElseEntirely"}},
		StatusCode:  http.StatusBadRequest,
	}
	if got := classifyDeleteError(other, "PermissionDoesNotExistException"); got == nil || errors.Is(got, ObjectNotFoundError) {
		t.Errorf("unrelated BadRequest gave %v, want the original error", got)
	}
}

// DeleteFileSystem is the consequential one. Its caller in volume.go already forgives
// ObjectNotFoundError, ApiNotFoundError and ApiBadRequestError as "already deleted" and fails on
// anything else - it simply never saw anything else, because the handler turned every unrecognised
// error into a successful deletion. A 500 or an authorization failure therefore made DeleteVolume
// report success while the filesystem was still there.
func TestDeleteFileSystemErrorClassification(t *testing.T) {
	const absent = "FilesystemDoesNotExistException"

	for name, tc := range map[string]struct {
		err          error
		wantNotFound bool
		wantNil      bool
	}{
		"deleted successfully": {nil, false, true},
		"already gone (404)":   {&ApiNotFoundError{}, true, false},
		"already gone by code": {&ApiBadRequestError{ApiResponse: &ApiResponse{ErrorCodes: []string{absent}}}, true, false},
		// The failures that used to be reported as successful deletions.
		"server error (500)":    {&ApiInternalError{}, false, false},
		"authorization failure": {&ApiAuthorizationError{}, false, false},
		"cluster unreachable":   {&ApiNetworkError{}, false, false},
		"retries exhausted":     {&ApiRetriesExceeded{Retries: 5}, false, false},
		"unrelated bad request": {&ApiBadRequestError{ApiResponse: &ApiResponse{ErrorCodes: []string{"SomethingElse"}}}, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			got := classifyDeleteError(tc.err, absent)
			switch {
			case tc.wantNil && got != nil:
				t.Errorf("got %v, want nil", got)
			case tc.wantNotFound && !errors.Is(got, ObjectNotFoundError):
				t.Errorf("got %v, want ObjectNotFoundError", got)
			case !tc.wantNil && !tc.wantNotFound && got == nil:
				t.Error("returned nil - a filesystem that was not deleted would be reported as deleted")
			}
		})
	}
}

// The absent-object codes are per object type, so a code that means "already gone" for one must not
// be honoured for another.
func TestDeleteErrorCodesAreScopedToTheObject(t *testing.T) {
	fsAbsent := &ApiBadRequestError{ApiResponse: &ApiResponse{ErrorCodes: []string{"FilesystemDoesNotExistException"}}}

	if got := classifyDeleteError(fsAbsent, "FilesystemDoesNotExistException"); !errors.Is(got, ObjectNotFoundError) {
		t.Errorf("filesystem handler gave %v, want ObjectNotFoundError", got)
	}
	if got := classifyDeleteError(fsAbsent, "PermissionDoesNotExistException"); errors.Is(got, ObjectNotFoundError) {
		t.Error("permission handler forgave a filesystem-absent code - the codes must be scoped per object")
	}
}
