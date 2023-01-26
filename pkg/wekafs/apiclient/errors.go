package apiclient

import (
	"errors"
	"fmt"
	"github.com/google/uuid"
)

type apiError interface {
	Error() string
	getType() string
}

type ApiError struct {
	Err         error
	Text        string
	StatusCode  int
	RawData     *[]byte
	ApiResponse *ApiResponse
}

func (e ApiError) Error() string {
	return fmt.Sprintf("%s: %s, status code: %d, original error: %e, raw response: %s, json: %s",
		e.getType(), e.Text, e.StatusCode, e.Err, func() string {
			if e.RawData != nil {
				return string(*e.RawData)
			}
			return ""
		}(),
		func() string {
			if e.ApiResponse != nil {
				return string(e.ApiResponse.Data)
			} else {
				return ""
			}
		}(),
	)
}

func (e ApiError) getType() string {
	return "ApiError"
}

type ApiNoEndpointsError ApiError

func (e ApiNoEndpointsError) getType() string {
	return "ApiNoEndpointsError"
}

func (e ApiNoEndpointsError) Error() string {
	return "No endpoints could be found for API client"
}

type ApiNetworkError ApiError

func (e ApiNetworkError) getType() string {
	return "ApiNetworkError"
}

func (e ApiNetworkError) Error() string {
	return "Could not connect: unknown host"
}

type ApiAuthorizationError ApiError

func (e ApiAuthorizationError) getType() string {
	return "ApiAuthorizationError"
}
func (e ApiAuthorizationError) Error() string {
	return fmt.Sprintf("%s: %s, status code: %d, original error: %e, raw response: %s, json: %s",
		e.getType(),
		e.Text,
		e.StatusCode,
		e.Err,
		func() string {
			if e.RawData != nil {
				return string(*e.RawData)
			}
			return ""
		}(),
		e.ApiResponse.Data)
}

type ApiBadRequestError ApiError

func (e ApiBadRequestError) getType() string {
	return "ApiBadRequestError"
}
func (e ApiBadRequestError) Error() string {
	return fmt.Sprintf("%s: %s, status code: %d, original error: %e, raw response: %s, json: %s",
		e.getType(),
		e.Text,
		e.StatusCode,
		e.Err,
		func() string {
			if e.RawData != nil {
				return string(*e.RawData)
			}
			return ""
		}(),
		e.ApiResponse.Data)
}

type ApiConflictError struct {
	ApiError
	ConflictingEntityId *uuid.UUID
}

func (e ApiConflictError) getType() string {
	return "ApiConflictError"
}
func (e ApiConflictError) Error() string {
	if e.ConflictingEntityId != nil {
		return fmt.Sprintf("%v, conflicting entity ID: %s", e.ApiError, e.ConflictingEntityId.String())
	}
	return e.ApiError.Error()
}

type ApiInternalError ApiError

func (e ApiInternalError) Error() string {
	return fmt.Sprintf("%s: %s, status code: %d, original error: %e, raw response: %s, json: %s",
		e.getType(),
		e.Text,
		e.StatusCode,
		e.Err,
		func() string {
			if e.RawData != nil {
				return string(*e.RawData)
			}
			return ""
		}(),
		e.ApiResponse.Data)
}
func (e ApiInternalError) getType() string {
	return "ApiInternalError"
}

type ApiNotFoundError ApiError

func (e ApiNotFoundError) Error() string {
	return fmt.Sprintf("%s: %s, status code: %d, original error: %e, raw response: %s, json: %s",
		e.getType(),
		e.Text,
		e.StatusCode,
		e.Err,
		func() string {
			if e.RawData != nil {
				return string(*e.RawData)
			}
			return ""
		}(), e.ApiResponse.Data)
}

func (e ApiNotFoundError) getType() string {
	return "ApiNotFoundError"
}

type ApiRetriesExceeded struct {
	ApiError
	Retries int
}

func (e ApiRetriesExceeded) getType() string {
	return "ApiRetriesExceeded"
}
func (e ApiRetriesExceeded) Error() string {
	return fmt.Sprintf("%s, retried %d times", e.ApiError.Error(), e.Retries)
}

var ObjectNotFoundError = errors.New("object not found")
var MultipleObjectsFoundError = errors.New("ambiguous filter, multiple objects match")
var RequestMissingParams = errors.New("request cannot be sent since some required params are missing")

// ApiNonrecoverableError is internally generated when non-transient error is found
type ApiNonrecoverableError struct {
	apiError
}

func (e ApiNonrecoverableError) Error() string {
	return e.apiError.Error()
}
func (e ApiNonrecoverableError) getType() string {
	return "ApiNonrecoverableError"
}

type transportError struct {
	Err error
}

func (e transportError) Error() string {
	return e.Err.Error()
}

func (e transportError) getType() string {
	return "transportError"
}
