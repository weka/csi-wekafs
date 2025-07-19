package apiclient

import (
	"encoding/json"
)

// ApiObject generic interface of API object of any type (FileSystem, Quota, etc.)
type ApiObject interface {
	GetType() string                 // returns the type of the object
	GetBasePath(a *ApiClient) string // returns the base path of objects of this type (plural)
	GetApiUrl(a *ApiClient) string   // returns the full URL of the object consisting of base path and object UID
	EQ(other ApiObject) bool         // a way to compare objects and check if they are the same
	getImmutableFields() []string    // provides a list of fields that are used for comparison in EQ()
	String() string                  // returns a string representation of the object
}

// ApiResponse returned by Request method
type ApiResponse struct {
	Data           json.RawMessage `json:"data"` // Data, may be either object, dict or list
	ErrorCodes     []string        `json:"data.exceptionClass,omitempty"`
	Message        string          `json:"message,omitempty"`    // Optional, can have error message
	NextToken      string          `json:"next_token,omitempty"` // For paginated objects
	HttpStatusCode int
}

func (a ApiResponse) SupportsPagination() bool {
	return false
}

func (a ApiResponse) CombinePartialResponse(next ApiObjectResponse) error {
	panic("implement me")
}

// ApiObjectRequest interface that describes a request for an ApiObject CRUD operation
type ApiObjectRequest interface {
	getRequiredFields() []string   // returns a list of fields that are mandatory for the object for creation
	hasRequiredFields() bool       // checks if all mandatory fields are filled in
	getRelatedObject() ApiObject   // returns the type of object that is being requested
	getApiUrl(a *ApiClient) string // returns the full URL of the object consisting of base path and object UID
	String() string                // returns a string representation of the object request
}

type ApiObjectResponse interface {
	SupportsPagination() bool
	CombinePartialResponse(next ApiObjectResponse) error // combines partial response with the current one
}
