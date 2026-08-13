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
	Data json.RawMessage `json:"data"` // Data, may be either object, dict or list
	// ErrorCodes is derived from Data by parseErrorCodes rather than unmarshalled directly. It
	// carried the tag `data.exceptionClass`, which encoding/json treats as a literal key name and
	// never traverses, so it stayed empty on every response - silently disabling every
	// exceptionClass check in this package.
	ErrorCodes     []string `json:"-"`
	Message        string   `json:"message,omitempty"`    // Optional, can have error message
	NextToken      string   `json:"next_token,omitempty"` // For paginated objects
	HttpStatusCode int
}

// parseErrorCodes lifts the exception classes the backend nests inside data onto the response, so
// callers can branch on the specific failure rather than only on the HTTP status.
//
// A failure body looks like:
//
//	{"message":"Operation cannot start because there are already 32 tasks running",
//	 "data":{"tasks_num":32,"exceptionClass":["CannotStartOperationTooManyTasks",...]}}
//
// Data is kept as RawMessage because it is otherwise decoded into whatever type the caller asked
// for, so the classes have to be read out separately. A body that does not carry them - every
// success, and any error shaped differently - simply leaves ErrorCodes empty.
func (r *ApiResponse) parseErrorCodes() {
	if len(r.Data) == 0 {
		return
	}
	var payload struct {
		ExceptionClass []string `json:"exceptionClass"`
	}
	if err := json.Unmarshal(r.Data, &payload); err != nil {
		// Data is frequently a list or a scalar rather than an object, which is not an error here:
		// it just means there are no exception classes to lift.
		return
	}
	r.ErrorCodes = payload.ExceptionClass
}

// HasErrorCode reports whether the backend returned the named exception class.
func (r *ApiResponse) HasErrorCode(code string) bool {
	if r == nil {
		return false
	}
	for _, c := range r.ErrorCodes {
		if c == code {
			return true
		}
	}
	return false
}

// ApiObjectResponse is implemented by response types the backend may return across several pages.
//
// Implementing it is what opts a type into pagination, so there is no separate boolean to keep in
// step with the method. A response type that does not implement it is fetched in a single request,
// exactly as before.
type ApiObjectResponse interface {
	// CombinePartialResponse folds one fetched page into the accumulated response. Implementations
	// append, since the accumulated value is the caller's own object.
	CombinePartialResponse(page ApiObjectResponse) error
}

// ApiObjectRequest interface that describes a request for an ApiObject CRUD operation
type ApiObjectRequest interface {
	getRequiredFields() []string   // returns a list of fields that are mandatory for the object for creation
	hasRequiredFields() bool       // checks if all mandatory fields are filled in
	getRelatedObject() ApiObject   // returns the type of object that is being requested
	getApiUrl(a *ApiClient) string // returns the full URL of the object consisting of base path and object UID
	String() string                // returns a string representation of the object request
}
