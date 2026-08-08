package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"time"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
)

// do Makes a basic API call to the client, returns an *ApiResponse that includes raw data, error message etc.
func (a *ApiClient) do(ctx context.Context, Method string, Path string, Payload *[]byte, Query url.Values) (*ApiResponse, apiError) {
	//construct URL path
	if len(a.Credentials.Endpoints) < 1 {
		return &ApiResponse{}, &ApiNoEndpointsError{
			Err: errors.New("no endpoints could be found for API client"),
		}
	}
	u := a.getUrl(ctx, Path)

	//construct base request and add auth if exists
	var body *bytes.Reader
	if Payload != nil {
		body = bytes.NewReader(*Payload)
	} else {
		body = bytes.NewReader([]byte(""))
	}
	r, err := http.NewRequest(Method, u, body)
	if err != nil {
		return nil, &ApiError{
			Err:         err,
			Text:        "Failed to construct API request",
			StatusCode:  0,
			RawData:     nil,
			ApiResponse: nil,
		}
	}
	r.Header.Set("content-type", "application/json")
	if a.isLoggedIn() {
		r.Header.Set("Authorization", fmt.Sprintf("Bearer %s", a.apiToken))
	}

	//add query params
	if Query != nil && len(Query) > 0 && a.SupportsUrlQueryParams() {
		r.URL.RawQuery = Query.Encode()
	}

	payload := ""
	if Payload != nil {
		payload = string(*Payload)
	}
	logger := log.Ctx(ctx)

	logger.Trace().Str("method", Method).Str("url", r.URL.RequestURI()).Str("payload", maskPayload(payload)).Msg("")

	//perform the request and update endpoint with stats
	endpoint := a.getEndpoint(ctx)
	endpoint.requestCount++
	start := time.Now()
	response, err := a.client.Do(r)

	if err != nil {
		endpoint.transportErrCount++
		return nil, &transportError{err}
	}

	if response == nil {
		endpoint.noRespCount++
		return nil, &transportError{errors.New("received no response")}
	}

	// update endpoint stats for success and total duration
	endpoint.requestDurationTotal += time.Since(start)
	if response.StatusCode != http.StatusOK {
		endpoint.failCount++
	}

	responseBody, err := io.ReadAll(response.Body)
	logger.Trace().Str("response", maskPayload(string(responseBody))).Msg("")
	if err != nil {
		endpoint.parseErrCount++
		return nil, &ApiInternalError{
			Err:         err,
			Text:        fmt.Sprintf("Failed to parse response: %s", err.Error()),
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: nil,
		}
	}

	defer func() {
		_ = response.Body.Close()
	}()

	Response := &ApiResponse{}
	err = json.Unmarshal(responseBody, Response)
	endpoint.parseErrCount++
	Response.HttpStatusCode = response.StatusCode
	if err != nil {
		logger.Error().Err(err).Int("http_status_code", Response.HttpStatusCode).Msg("Could not parse response JSON")
		return nil, &ApiError{
			Err:         err,
			Text:        "Failed to parse HTTP response body",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	}

	switch response.StatusCode {
	case http.StatusOK: //200
		return Response, nil
	case http.StatusCreated: //201
		return Response, nil
	case http.StatusAccepted: //202
		return Response, nil
	case http.StatusNoContent: //203
		return Response, nil
	case http.StatusBadRequest: //400
		endpoint.http400ErrCount++
		return Response, &ApiBadRequestError{
			Err:         nil,
			Text:        "Operation failed",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	case http.StatusUnauthorized: //401
		endpoint.http401ErrCount++
		return Response, &ApiAuthorizationError{
			Err:         nil,
			Text:        "Operation failed",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	case http.StatusForbidden: //403
		endpoint.http403ErrCount++
		return Response, &ApiForbiddenError{
			Err:         err,
			Text:        "Permission denied",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	case http.StatusNotFound: //404
		endpoint.http404ErrCount++
		return Response, &ApiNotFoundError{
			Err:         nil,
			Text:        "Object not found",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	case http.StatusConflict: //409
		endpoint.http409ErrCount++
		return Response, &ApiConflictError{
			ApiError: ApiError{
				Err:         nil,
				Text:        "Object conflict",
				StatusCode:  response.StatusCode,
				RawData:     &responseBody,
				ApiResponse: Response,
			},
			ConflictingEntityId: nil, //TODO: parse and provide entity ID when supplied by API
		}

	case http.StatusInternalServerError: //500
		endpoint.http500ErrCount++
		return Response, &ApiInternalError{
			Err:         nil,
			Text:        Response.Message,
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}

	case http.StatusServiceUnavailable: //503
		endpoint.http503ErrCount++
		return Response, &ApiNotAvailableError{
			Err:         nil,
			Text:        Response.Message,
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}

	default:
		endpoint.generalErrCount++
		return Response, &ApiError{
			Err:         err,
			Text:        "General failure during API command",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	}
}

// request wraps do with retries and some more error handling. It returns the token of the next
// page when the backend indicates the result was truncated, or an empty string when it was not.
func (a *ApiClient) request(ctx context.Context, Method string, Path string, Payload *[]byte, Query url.Values, v interface{}) (string, apiError) {
	op := "ApiClientRequest"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("span_id", span.SpanContext().SpanID().String()).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)
	var nextToken string
	f := func() apiError {
		// Reset per attempt: a retry must not inherit the token of an attempt that later failed.
		nextToken = ""
		rawResponse, reqErr := a.do(ctx, Method, Path, Payload, Query)
		if a.handleTransientErrors(ctx, reqErr) != nil { // transient network errors
			a.rotateEndpoint(ctx)
			logger.Error().Err(reqErr).Msg("")
			return reqErr
		}
		if reqErr != nil {
			return ApiNonTransientError{reqErr}
		}
		s := rawResponse.HttpStatusCode
		var responseCodes []string
		if len(rawResponse.ErrorCodes) > 0 {
			logger.Error().Strs("error_codes", rawResponse.ErrorCodes).Msg("Failed to execute request")
			for _, code := range rawResponse.ErrorCodes {
				if code != "OperationFailedException" {
					responseCodes = append(responseCodes, code)
				}
			}
			return ApiNonTransientError{
				apiError: reqErr,
			}
		}
		err := json.Unmarshal(rawResponse.Data, v)
		if err != nil {
			logger.Error().Err(err).Interface("object_type", reflect.TypeOf(v)).Msg("Failed to marshal JSON request into a valid interface")
		}
		switch s {
		case http.StatusOK:
			nextToken = rawResponse.NextToken
			return nil
		case http.StatusUnauthorized:
			logger.Warn().Msg("Got Authorization failure on request, trying to re-login")
			_ = a.Init(ctx)
			return reqErr
		case
			http.StatusNotFound,            // 404
			http.StatusConflict,            // 409
			http.StatusBadRequest,          // 400
			http.StatusInternalServerError, // 500
			http.StatusForbidden:           // 403
			return ApiNonTransientError{reqErr}
		default:
			logger.Warn().Err(reqErr).Int("http_code", s).Msg("Failed to perform a request, got an unhandled error")
			return ApiNonTransientError{reqErr}
		}
	}
	err := a.retryBackoff(ctx, ApiRetryMaxCount, time.Second*time.Duration(ApiRetryIntervalSeconds), f)
	if err != nil {
		return "", err.(apiError)
	}
	return nextToken, nil
}

// Request makes sure that client is logged in and has a non-expired token.
//
// When Response implements ApiObjectResponse the backend may return it across several pages, and
// every page is fetched and folded into Response before returning.
func (a *ApiClient) Request(ctx context.Context, Method string, Path string, Payload *[]byte, Query url.Values, Response interface{}) error {
	if err := a.Init(ctx); err != nil {
		log.Ctx(ctx).Error().Err(err).Msg("Failed to re-authenticate on repeating request")
		return err
	}

	accumulated, paginated := Response.(ApiObjectResponse)
	if !paginated {
		_, err := a.request(ctx, Method, Path, Payload, Query, Response)
		if err != nil {
			return err
		}
		return nil
	}

	responseType := reflect.TypeOf(Response)
	if responseType.Kind() != reflect.Ptr {
		return errors.New("a paginated response must be passed as a pointer")
	}
	// Work on a copy: Query belongs to the caller, and it is legitimately nil, which Set would
	// panic on.
	query := cloneQuery(Query)
	logger := log.Ctx(ctx)

	for page := 1; ; page++ {
		// Every page is unmarshalled into a fresh object and then folded into the caller's
		// Response. Unmarshalling straight into Response would let each page overwrite the last.
		pageValue := reflect.New(responseType.Elem()).Interface()
		nextToken, err := a.request(ctx, Method, Path, Payload, query, pageValue)
		if err != nil {
			return err
		}
		if err := accumulated.CombinePartialResponse(pageValue.(ApiObjectResponse)); err != nil {
			logger.Error().Err(err).Msg("Failed to combine a partial response")
			return err
		}
		if nextToken == "" {
			if page > 1 {
				logger.Debug().Int("pages", page).Msg("Fetched a paginated response")
			}
			return nil
		}
		if page >= ApiMaxPagesPerRequest {
			return fmt.Errorf("paginated response exceeded %d pages, giving up", ApiMaxPagesPerRequest)
		}
		query.Set("next_token", nextToken)
	}
}

// cloneQuery copies the caller's query parameters so pagination can add its own without mutating
// what the caller passed, and so a nil map is usable.
func cloneQuery(q url.Values) url.Values {
	out := make(url.Values, len(q))
	for k, v := range q {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// Get is shortcut for Request("GET" ...)
func (a *ApiClient) Get(ctx context.Context, Path string, Query url.Values, Response interface{}) error {
	return a.Request(ctx, "GET", Path, nil, Query, Response)
}

// Post is shortcut for Request("POST" ...)
func (a *ApiClient) Post(ctx context.Context, Path string, Payload *[]byte, Query url.Values, Response interface{}) error {
	return a.Request(ctx, "POST", Path, Payload, Query, Response)
}

// Put is shortcut for Request("PUT" ...)
func (a *ApiClient) Put(ctx context.Context, Path string, Payload *[]byte, Query url.Values, Response interface{}) error {
	return a.Request(ctx, "PUT", Path, Payload, Query, Response)
}

// Delete is shortcut for Request("DELETE" ...)
func (a *ApiClient) Delete(ctx context.Context, Path string, Payload *[]byte, Query url.Values, Response interface{}) error {
	return a.Request(ctx, "DELETE", Path, Payload, Query, Response)
}
