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
	// Spread requests across the management nodes when the caller asked for it. This has to happen
	// once, here, rather than inside getEndpoint: a single request resolves the endpoint twice - to
	// build the URL and again to record stats - and rotating on each of those would attribute the
	// request to a different endpoint than the one it was actually sent to.
	if a.rotateEndpointOnEachRequest {
		a.apiEndpoints.Rotate()
	}
	u, uErr := a.getUrl(ctx, Path)
	if uErr != nil {
		return &ApiResponse{}, uErr
	}

	// status is overwritten as soon as the outcome is known; the initial value only survives if the
	// function returns through a path that sets none.
	status := "error"
	startTime := time.Now()
	defer func() {
		labels := []string{
			a.driverName,
			a.ClusterGuid.String(),
			a.getEndpoint(ctx).IpAddress,
			Method,
			generalizeUrlPathForMetrics(Path),
			status,
		}
		apiMetrics.requestCounters.WithLabelValues(labels...).Inc()
		apiMetrics.requestDurations.WithLabelValues(labels...).Observe(time.Since(startTime).Seconds())
	}()

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
	if token := a.authToken(); token != "" {
		r.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
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
	endpoint, epErr := a.requireEndpoint(ctx)
	if epErr != nil {
		return &ApiResponse{}, epErr
	}
	endpoint.requestCount.Add(1)
	start := time.Now()
	response, err := a.client.Do(r)

	if err != nil {
		endpoint.transportErrCount.Add(1)
		status = "transport_error"
		return nil, &transportError{err}
	}

	if response == nil {
		endpoint.noRespCount.Add(1)
		status = "no_response_from_server"
		return nil, &transportError{errors.New("received no response")}
	}

	// update endpoint stats for success and total duration
	endpoint.requestDurationTotal.Add(int64(time.Since(start)))
	if response.StatusCode != http.StatusOK {
		endpoint.failCount.Add(1)
	}

	responseBody, err := io.ReadAll(response.Body)
	logger.Trace().Str("response", maskPayload(string(responseBody))).Msg("")
	if err != nil {
		endpoint.parseErrCount.Add(1)
		status = "response_parse_error"
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
	Response.HttpStatusCode = response.StatusCode
	if err != nil {
		endpoint.parseErrCount.Add(1)
		status = "response_parse_error"
		logger.Error().Err(err).Int("http_status_code", Response.HttpStatusCode).Msg("Could not parse response JSON")
		return nil, &ApiError{
			Err:         err,
			Text:        "Failed to parse HTTP response body",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	}

	// From here the HTTP status is known, so it becomes the metric's status. The switch below
	// turns each code into a typed error, but they all share one label shape.
	status = fmt.Sprintf("http_%d", response.StatusCode)

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
		endpoint.http400ErrCount.Add(1)
		return Response, &ApiBadRequestError{
			Err:         nil,
			Text:        "Operation failed",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	case http.StatusUnauthorized: //401
		endpoint.http401ErrCount.Add(1)
		return Response, &ApiAuthorizationError{
			Err:         nil,
			Text:        "Operation failed",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	case http.StatusForbidden: //403
		endpoint.http403ErrCount.Add(1)
		return Response, &ApiForbiddenError{
			Err:         err,
			Text:        "Permission denied",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	case http.StatusNotFound: //404
		endpoint.http404ErrCount.Add(1)
		return Response, &ApiNotFoundError{
			Err:         nil,
			Text:        "Object not found",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	case http.StatusConflict: //409
		endpoint.http409ErrCount.Add(1)
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
		endpoint.http500ErrCount.Add(1)
		return Response, &ApiInternalError{
			Err:         nil,
			Text:        Response.Message,
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}

	case http.StatusServiceUnavailable: //503
		endpoint.http503ErrCount.Add(1)
		return Response, &ApiNotAvailableError{
			Err:         nil,
			Text:        Response.Message,
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}

	default:
		endpoint.generalErrCount.Add(1)
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
		// An absent data key is not a decode failure. Every DELETE passes &ApiResponse{} and gets
		// back a body carrying no data at all, and a paginated fetch may legitimately return an
		// empty page. Unmarshalling nil would fail with "unexpected end of JSON input", so
		// distinguish "nothing to decode" from "present but malformed" before deciding.
		var unmarshalErr error
		if len(rawResponse.Data) > 0 {
			unmarshalErr = json.Unmarshal(rawResponse.Data, v)
			if unmarshalErr != nil {
				logger.Error().Err(unmarshalErr).Interface("object_type", reflect.TypeOf(v)).Msg("Failed to marshal JSON request into a valid interface")
			}
		}
		switch s {
		case http.StatusOK:
			if unmarshalErr != nil {
				// Data was present but did not decode. Folding it in as an empty page would
				// silently truncate a paginated result. Only the success path relies on
				// unmarshalErr; non-200 paths return their own reqErr below.
				return ApiNonTransientError{apiError: ApiError{
					Err:         unmarshalErr,
					Text:        "Failed to unmarshal JSON response body",
					StatusCode:  s,
					ApiResponse: rawResponse,
				}}
			}
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

	// Only a GET can safely be repeated per page: a POST/PUT/DELETE whose response type happens
	// to implement ApiObjectResponse would otherwise re-send the same mutating payload once per
	// page.
	accumulated, paginated := Response.(ApiObjectResponse)
	paginated = paginated && Method == http.MethodGet
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
	responseValue := reflect.ValueOf(Response)
	if responseValue.IsNil() {
		// accumulated wraps a nil *T here: the interface value itself is non-nil (it carries a
		// type), so the type assertion above succeeds, but CombinePartialResponse would
		// dereference a nil receiver and panic. Fail cleanly instead.
		return fmt.Errorf("a paginated response must not be a nil %s pointer", responseType.Elem())
	}
	logger := log.Ctx(ctx)

	if !a.SupportsUrlQueryParams() {
		// The cluster can't accept next_token at all, so pagination cannot happen: every
		// request would come back as page 1 again (see the non-advancing-token check below,
		// which exists for exactly this shape of backend misbehaviour). Do a single request and
		// return it, matching the pre-pagination behaviour, rather than looping or failing a
		// call that used to succeed.
		logger.Warn().Str("path", Path).Msg("Cluster does not support URL query parameters; fetching a single page, result may be truncated")
		_, err := a.request(ctx, Method, Path, Payload, Query, Response)
		return err
	}

	// Work on a copy: Query belongs to the caller, and it is legitimately nil, which Set would
	// panic on.
	query := cloneQuery(Query)

	// Zero the caller's accumulator before fetching the first page. CombinePartialResponse
	// appends, so a caller that passes in a non-empty accumulator (or reuses one across calls)
	// would otherwise get duplicates alongside the freshly fetched pages. This restores the
	// replace-semantics of the pre-pagination code path, which unmarshalled straight into
	// Response.
	responseValue.Elem().Set(reflect.Zero(responseType.Elem()))

	// currentToken is the next_token that produced the page just received - used to detect a
	// backend that echoes the same token back forever instead of advancing it.
	currentToken := ""

	for page := 1; ; page++ {
		if err := ctx.Err(); err != nil {
			return err
		}
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
		if nextToken == currentToken {
			return fmt.Errorf("backend returned pagination token %q unchanged after it was already used for %s; refusing to loop indefinitely since the backend is not advancing the token", nextToken, Path)
		}
		if page >= ApiMaxPagesPerRequest {
			return fmt.Errorf("paginated response exceeded %d pages, giving up", ApiMaxPagesPerRequest)
		}
		query.Set("next_token", nextToken)
		currentToken = nextToken
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
