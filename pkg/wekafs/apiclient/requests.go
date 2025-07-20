package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"time"
)

// do Makes a basic API call to the client, returns an *ApiResponse that includes raw data, error message etc.
func (a *ApiClient) do(ctx context.Context, Method string, Path string, Payload *[]byte, Query url.Values) (*ApiResponse, apiError) {
	ctx = context.WithValue(ctx, "startTime", time.Now())
	//construct URL path
	if len(a.Credentials.Endpoints) < 1 {
		return &ApiResponse{}, &ApiNoEndpointsError{
			Err: errors.New("no endpoints could be found for API client"),
		}
	}
	u := a.getUrl(ctx, Path)
	status := "ERROR"
	defer func() {
		path := generalizeUrlPathForMetrics(Path)
		guid := a.ClusterGuid.String()
		ip := a.getEndpoint(ctx).IpAddress
		dn := a.driverName
		a.metrics.requestCounters.WithLabelValues(dn, guid, ip, Method, path, status).Inc()
		a.metrics.requestDurations.WithLabelValues(dn, guid, ip, Method, path, status).Observe(time.Since(ctx.Value("startTime").(time.Time)).Seconds())
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
	response, err := a.client.Do(r)

	if err != nil {
		status = "transport_error"
		return nil, &transportError{err}
	}

	if response == nil {
		status = "no_response_from_server"
		return nil, &transportError{errors.New("received no response")}
	}

	responseBody, err := io.ReadAll(response.Body)
	logger.Trace().Str("response", maskPayload(string(responseBody))).Str("duration", time.Since(ctx.Value("startTime").(time.Time)).String()).Msg("")
	if err != nil {
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
	status = "response_parse_error"
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
	status = fmt.Sprintf("http_%d", response.StatusCode)
	switch response.StatusCode {
	case http.StatusOK: //200
		return Response, nil
	case http.StatusCreated: //201
		status = "success"
		return Response, nil
	case http.StatusAccepted: //202
		status = "success"
		return Response, nil
	case http.StatusNoContent: //203
		status = "success"
		return Response, nil
	case http.StatusBadRequest: //400
		return Response, &ApiBadRequestError{
			Err:         nil,
			Text:        "Operation failed",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	case http.StatusUnauthorized: //401
		return Response, &ApiAuthorizationError{
			Err:         nil,
			Text:        "Operation failed",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	case http.StatusNotFound: //404
		return Response, &ApiNotFoundError{
			Err:         nil,
			Text:        "Object not found",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	case http.StatusConflict: //409
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
		return Response, &ApiInternalError{
			Err:         nil,
			Text:        Response.Message,
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}

	case http.StatusServiceUnavailable: //503
		return Response, &ApiNotAvailableError{
			Err:         nil,
			Text:        Response.Message,
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}

	default:
		status = "general_api_error"
		return Response, &ApiError{
			Err:         err,
			Text:        "General failure during API command",
			StatusCode:  response.StatusCode,
			RawData:     &responseBody,
			ApiResponse: Response,
		}
	}
}

// request wraps do with retries and some more error handling
func (a *ApiClient) request(ctx context.Context, Method string, Path string, Payload *[]byte, Query url.Values, v ApiObjectResponse) apiError {
	op := "ApiClientRequest"
	ctx, span := otel.Tracer(TracerName).Start(ctx, op)
	defer span.End()
	ctx = log.With().Str("trace_id", span.SpanContext().TraceID().String()).Str("span_id", span.SpanContext().SpanID().String()).Str("op", op).Logger().WithContext(ctx)
	logger := log.Ctx(ctx)
	f := func() apiError {
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
			if rawResponse.NextToken != "" {
				_, ok := v.(ApiObjectResponse)
				if ok {
					if v.SupportsPagination() {
						if rawResponse.NextToken != "" {
							return ApiResponseNextPage{NextPageToken: rawResponse.NextToken}
						}
					}
				}
			}
			return nil
		case http.StatusUnauthorized:
			logger.Warn().Msg("Got Authorization failure on request, trying to re-login")
			_ = a.Init(ctx)
			return reqErr
		case http.StatusNotFound, http.StatusConflict, http.StatusBadRequest, http.StatusInternalServerError:
			return ApiNonTransientError{reqErr}
		default:
			logger.Warn().Err(reqErr).Int("http_code", s).Msg("Failed to perform a request, got an unhandled error")
			return ApiNonTransientError{reqErr}
		}
	}
	err := a.retryBackoff(ctx, ApiRetryMaxCount, time.Second*time.Duration(ApiRetryIntervalSeconds), f)
	if err != nil {
		return err.(apiError)
	}
	return nil
}

// Request makes sure that client is logged in and has a non-expired token
func (a *ApiClient) Request(ctx context.Context, Method string, Path string, Payload *[]byte, Query url.Values, Response ApiObjectResponse) error {
	ctx, span := otel.Tracer(TracerName).Start(ctx, "ApiClientRequest")
	defer span.End()
	logger := log.Ctx(ctx)

	if err := a.Init(ctx); err != nil {
		logger.Error().Err(err).Msg("Failed to re-authenticate on repeating request")
		return err
	}
	if a.RotateEndpointOnEachRequest {
		a.rotateEndpoint(ctx)
	}

	rt := reflect.TypeOf(Response)
	newObj := reflect.New(rt.Elem()).Interface().(ApiObjectResponse)

	nextPageNeeded := true
	pagesFetched := 0
	for nextPageNeeded {
		pagesFetched++
		err := a.request(ctx, Method, Path, Payload, Query, Response)
		if err != nil {
			switch e := err.(type) {
			case ApiResponseNextPage:
				if Response.SupportsPagination() {
					err2 := newObj.CombinePartialResponse(Response)
					if err2 != nil {
						log.Ctx(ctx).Error().Err(err2).Msg("Failed to combine partial response")
						return err2
					}
					Response = newObj
				} else {
					break
				}

				if e.NextPageToken != "" {
					Query.Set("next_token", e.NextPageToken)
					nextPageNeeded = true

				} else {
					nextPageNeeded = false
				}
			default:
				return err
			}
		} else {
			nextPageNeeded = false
		}
		if pagesFetched > 1 {
			logger.Trace().Int("pages_fetched", pagesFetched).Msg("Fetched more than one page response")
		}

	}
	return nil
}

// Get is shortcut for Request("GET" ...)
func (a *ApiClient) Get(ctx context.Context, Path string, Query url.Values, Response ApiObjectResponse) error {
	return a.Request(ctx, "GET", Path, nil, Query, Response)
}

// Post is shortcut for Request("POST" ...)
func (a *ApiClient) Post(ctx context.Context, Path string, Payload *[]byte, Query url.Values, Response ApiObjectResponse) error {
	return a.Request(ctx, "POST", Path, Payload, Query, Response)
}

// Put is shortcut for Request("PUT" ...)
func (a *ApiClient) Put(ctx context.Context, Path string, Payload *[]byte, Query url.Values, Response ApiObjectResponse) error {
	return a.Request(ctx, "PUT", Path, Payload, Query, Response)
}

// Delete is shortcut for Request("DELETE" ...)
func (a *ApiClient) Delete(ctx context.Context, Path string, Payload *[]byte, Query url.Values, Response ApiObjectResponse) error {
	return a.Request(ctx, "DELETE", Path, Payload, Query, Response)
}
