package openapi_test

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	api "github.com/Manu343726/hammer"
	"github.com/Manu343726/hammer/openapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests hold the invoker to its promises: a call is built from the
// declaration and the caller's arguments, every credential scheme the document
// language documents is applied where the scheme says it travels, a call the
// declaration cannot satisfy is refused before it is sent, and a failure of the
// transport is reported as unavailable rather than as the server's answer.

// covInvokeOperation is one declared operation, as a description would carry it.
func covInvokeOperation(method, path string, parameters ...api.Parameter) api.Operation {
	return api.Operation{
		ID:         "cov-api/things/call",
		APIID:      "cov-api",
		Service:    "things",
		Name:       "call",
		Method:     method,
		Path:       path,
		Parameters: parameters,
	}
}

// covInvokeServer is a registered server pointing at a test server.
func covInvokeServer(baseURL string) api.Server {
	return api.Server{ID: "cov-host", Name: "Cov host", BaseURL: baseURL, Format: openapi.Format, Transport: openapi.TransportHTTP}
}

// covInvoke performs one call and returns both the result and the error, because
// several tests assert the failure rather than the answer.
func covInvoke(invoker *openapi.Invoker, target api.API, server api.Server, operation api.Operation, arguments string) (api.Result, error) {
	return invoker.Invoke(context.Background(), api.Call{
		Server:    server,
		API:       target,
		Operation: operation,
		Arguments: []byte(arguments),
	})
}

// covRecordingServer is an upstream that records the request it received and
// answers with a fixed status and body.
type covRecordingServer struct {
	*httptest.Server
	requests []*http.Request
	queries  []string
	bodies   []string
}

func covStartRecordingServer(t *testing.T, status int, body string, headers map[string]string) *covRecordingServer {
	t.Helper()
	recorder := &covRecordingServer{}
	recorder.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.requests = append(recorder.requests, r.Clone(r.Context()))
		recorder.queries = append(recorder.queries, r.URL.RawQuery)
		received, _ := io.ReadAll(r.Body)
		recorder.bodies = append(recorder.bodies, string(received))
		for name, value := range headers {
			w.Header().Set(name, value)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(recorder.Close)
	return recorder
}

func TestCovInvokeAppliesEveryDocumentedCredentialScheme(t *testing.T) {
	// The scheme says where a credential travels. A scheme applied in the wrong
	// place is a call that reaches the server unauthenticated and fails somewhere
	// less clear, so every documented type and location is asserted at the
	// upstream.
	tests := []struct {
		name       string
		scheme     api.SecurityScheme
		credential string
		assert     func(t *testing.T, request *http.Request, query string)
	}{
		{
			name:       "api key in header",
			scheme:     api.SecurityScheme{Name: "auth", Type: "apiKey", In: "header", ParameterName: "X-API-Key"},
			credential: "secret",
			assert: func(t *testing.T, request *http.Request, _ string) {
				assert.Equal(t, "secret", request.Header.Get("X-API-Key"))
			},
		},
		{
			name:       "api key in query",
			scheme:     api.SecurityScheme{Name: "auth", Type: "apiKey", In: "query", ParameterName: "api_key"},
			credential: "secret",
			assert: func(t *testing.T, _ *http.Request, query string) {
				assert.Equal(t, "api_key=secret", query)
			},
		},
		{
			name:       "api key in cookie",
			scheme:     api.SecurityScheme{Name: "auth", Type: "apiKey", In: "cookie", ParameterName: "session"},
			credential: "secret",
			assert: func(t *testing.T, request *http.Request, _ string) {
				assert.Equal(t, "session=secret", request.Header.Get("Cookie"))
			},
		},
		{
			name:       "http basic",
			scheme:     api.SecurityScheme{Name: "auth", Type: "http", Scheme: "basic"},
			credential: "user:pass",
			assert: func(t *testing.T, request *http.Request, _ string) {
				assert.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte("user:pass")), request.Header.Get("Authorization"))
			},
		},
		{
			name:       "http bearer",
			scheme:     api.SecurityScheme{Name: "auth", Type: "http", Scheme: "bearer"},
			credential: "token",
			assert: func(t *testing.T, request *http.Request, _ string) {
				assert.Equal(t, "bearer token", request.Header.Get("Authorization"))
			},
		},
		{
			name:       "oauth2",
			scheme:     api.SecurityScheme{Name: "auth", Type: "oauth2"},
			credential: "token",
			assert: func(t *testing.T, request *http.Request, _ string) {
				assert.Equal(t, "Bearer token", request.Header.Get("Authorization"))
			},
		},
		{
			name:       "openid connect",
			scheme:     api.SecurityScheme{Name: "auth", Type: "openIdConnect"},
			credential: "token",
			assert: func(t *testing.T, request *http.Request, _ string) {
				assert.Equal(t, "Bearer token", request.Header.Get("Authorization"))
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := covStartRecordingServer(t, http.StatusOK, `{}`, nil)
			target := api.API{SecuritySchemes: []api.SecurityScheme{test.scheme}}
			operation := covInvokeOperation("get", "/things")
			operation.Security = []api.SecurityRequirement{{Scheme: "auth"}}

			// The credential source is a function here, which is the adapter a
			// deployment uses when its credentials come from somewhere this package
			// cannot know about.
			invoker := openapi.NewInvoker(openapi.InvokerOptions{
				Credentials: openapi.CredentialSourceFunc(func(scheme api.SecurityScheme, _ api.SecurityRequirement) (string, bool) {
					return test.credential, scheme.Name == "auth"
				}),
			})
			result, err := covInvoke(invoker, target, covInvokeServer(upstream.URL), operation, "{}")
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, result.Status)

			require.Len(t, upstream.requests, 1)
			test.assert(t, upstream.requests[0], upstream.queries[0])
		})
	}
}

func TestCovInvokeRefusesSecurityItCannotSatisfy(t *testing.T) {
	// Each refusal happens before the request is sent: sending an unauthenticated
	// call and failing at the far end would make the cause much harder to find.
	upstream := covStartRecordingServer(t, http.StatusOK, `{}`, nil)
	operation := covInvokeOperation("get", "/things")
	operation.Security = []api.SecurityRequirement{{Scheme: "auth"}}

	// A requirement the document never described cannot be satisfied knowingly.
	_, err := covInvoke(openapi.NewInvoker(openapi.InvokerOptions{}), api.API{}, covInvokeServer(upstream.URL), operation, "{}")
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), "which the document does not describe")

	// A credential source that holds nothing for the scheme is reported the same
	// way, so "no credentials at all" and "no credential for this scheme" are told
	// apart.
	target := api.API{SecuritySchemes: []api.SecurityScheme{
		{Name: "auth", Type: "apiKey", In: "header", ParameterName: "X-API-Key"},
	}}
	operation.Security = []api.SecurityRequirement{{Scheme: "auth"}}
	_, err = covInvoke(openapi.NewInvoker(openapi.InvokerOptions{Credentials: openapi.StaticCredentials{}}), target, covInvokeServer(upstream.URL), operation, "{}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no credential is available for it")

	// A requirement for a scheme the document describes elsewhere is not this
	// operation's scheme, so it is skipped rather than refused.
	target = api.API{SecuritySchemes: []api.SecurityScheme{
		{Name: "other", Type: "http", Scheme: "bearer"},
		{Name: "auth", Type: "apiKey", In: "header", ParameterName: "X-API-Key"},
	}}
	operation.Security = []api.SecurityRequirement{{Scheme: "unknown"}, {Scheme: "auth"}}
	invoker := openapi.NewInvoker(openapi.InvokerOptions{Credentials: openapi.StaticCredentials{"auth": "secret"}})
	_, err = covInvoke(invoker, target, covInvokeServer(upstream.URL), operation, "{}")
	require.NoError(t, err, "a requirement the document does not name is not satisfied, but the named one is")
	require.Len(t, upstream.requests, 1)
	assert.Equal(t, "secret", upstream.requests[0].Header.Get("X-API-Key"))

	// A scheme whose type this package cannot apply is refused rather than sent
	// under a header the server would not understand.
	target.SecuritySchemes = []api.SecurityScheme{{Name: "auth", Type: "mutualTLS"}}
	operation.Security = []api.SecurityRequirement{{Scheme: "auth"}}
	_, err = covInvoke(openapi.NewInvoker(openapi.InvokerOptions{Credentials: openapi.StaticCredentials{"auth": "cert"}}), target, covInvokeServer(upstream.URL), operation, "{}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `of unsupported type "mutualTLS"`)

	// An API key scheme that does not name the parameter it travels under cannot
	// be applied anywhere.
	target.SecuritySchemes = []api.SecurityScheme{{Name: "auth", Type: "apiKey", In: "header"}}
	_, err = covInvoke(openapi.NewInvoker(openapi.InvokerOptions{Credentials: openapi.StaticCredentials{"auth": "secret"}}), target, covInvokeServer(upstream.URL), operation, "{}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not name the parameter it travels under")

	assert.Len(t, upstream.requests, 1, "a refused call is never sent")
}

func TestCovInvokeBuildsTheRequestFromTheDeclaration(t *testing.T) {
	// The call is assembled from what the operation declares: an absent optional
	// parameter contributes nothing, an object or array parameter is sent as its
	// JSON text, and a cookie parameter travels in the cookie header.
	upstream := covStartRecordingServer(t, http.StatusOK, `{"ok":true}`, nil)
	target := api.API{}
	operation := covInvokeOperation("post", "/things/{thingId}",
		api.Parameter{Name: "thingId", In: api.ParameterInPath, Required: true, Schema: api.StringSchema()},
		api.Parameter{Name: "optional", In: api.ParameterInQuery, Schema: api.StringSchema()},
		api.Parameter{Name: "filter", In: api.ParameterInQuery, Schema: &api.Schema{Type: api.TypeObject}},
		api.Parameter{Name: "session", In: api.ParameterInCookie, Schema: api.StringSchema()},
	)

	result, err := covInvoke(openapi.NewInvoker(openapi.InvokerOptions{}), target, covInvokeServer(upstream.URL+"/api"), operation,
		`{"thingId":"a/b","filter":{"kind":"pet"},"session":"s1"}`)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, result.Status)

	require.Len(t, upstream.requests, 1)
	received := upstream.requests[0]
	assert.Equal(t, "/api/things/a%2Fb", received.URL.EscapedPath(), "a path value is escaped, not treated as structure")
	assert.Equal(t, `{"kind":"pet"}`, received.URL.Query().Get("filter"))
	assert.Equal(t, "session=s1", received.Header.Get("Cookie"))
	assert.Empty(t, received.URL.Query().Get("optional"))
}

func TestCovInvokeRejectsArgumentsTheDeclarationCannotUse(t *testing.T) {
	// Each of these is a caller mistake that would otherwise produce a request that
	// looks successful and does not do what was asked.
	upstream := covStartRecordingServer(t, http.StatusOK, `{}`, nil)
	target := api.API{}

	tests := []struct {
		name      string
		operation api.Operation
		arguments string
		contains  string
	}{
		{
			name:      "arguments are not a JSON object",
			operation: covInvokeOperation("get", "/things"),
			arguments: `[1, 2]`,
			contains:  "arguments must be a JSON object",
		},
		{
			name: "an empty path parameter",
			operation: covInvokeOperation("get", "/things/{thingId}",
				api.Parameter{Name: "thingId", In: api.ParameterInPath, Required: true, Schema: api.StringSchema()}),
			arguments: `{"thingId":""}`,
			contains:  `path parameter "thingId" cannot be empty`,
		},
		{
			name: "a parameter that travels in the body",
			operation: covInvokeOperation("post", "/things",
				api.Parameter{Name: "payload", In: api.ParameterInBody, Schema: api.StringSchema()}),
			arguments: `{"payload":"x"}`,
			contains:  `body parameter "payload" must be supplied as the body argument`,
		},
		{
			name: "a parameter in a location this transport has no place for",
			operation: covInvokeOperation("post", "/things",
				api.Parameter{Name: "form", In: "form", Schema: api.StringSchema()}),
			arguments: `{"form":"x"}`,
			contains:  `uses unsupported location "form"`,
		},
		{
			name: "a scalar parameter supplied as a value",
			operation: covInvokeOperation("get", "/things",
				api.Parameter{Name: "name", In: api.ParameterInQuery, Schema: api.StringSchema()}),
			arguments: `{"name":{"nested":true}}`,
			contains:  `parameter "name" must be a scalar value`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := covInvoke(openapi.NewInvoker(openapi.InvokerOptions{}), target, covInvokeServer(upstream.URL), test.operation, test.arguments)
			require.Error(t, err)
			assert.Equal(t, api.KindInvalid, api.KindOf(err))
			assert.Contains(t, err.Error(), test.contains)
		})
	}
	assert.Empty(t, upstream.requests, "a refused call is never sent")
}

func TestCovInvokeSendsAWholeBodyOrItsFields(t *testing.T) {
	// The request value may be supplied whole, as "body", or field by field when
	// the caller has the operation's request schema in front of it. Either way the
	// upstream receives one JSON object.
	upstream := covStartRecordingServer(t, http.StatusOK, `{}`, nil)
	operation := covInvokeOperation("post", "/things")
	operation.Request = api.ObjectSchema(
		api.Property{Name: "name", Schema: api.StringSchema(), Required: true},
		api.Property{Name: "age", Schema: &api.Schema{Type: api.TypeInteger}},
	)
	server := covInvokeServer(upstream.URL)
	invoker := openapi.NewInvoker(openapi.InvokerOptions{})

	_, err := covInvoke(invoker, api.API{}, server, operation, `{"body":{"name":"Rex"}}`)
	require.NoError(t, err)
	_, err = covInvoke(invoker, api.API{}, server, operation, `{"name":"Rex","age":3}`)
	require.NoError(t, err)

	require.Len(t, upstream.bodies, 2)
	assert.JSONEq(t, `{"name":"Rex"}`, upstream.bodies[0])
	assert.JSONEq(t, `{"name":"Rex","age":3}`, upstream.bodies[1])
	assert.Equal(t, "application/json", upstream.requests[0].Header.Get("Content-Type"))
	assert.Equal(t, "application/json", upstream.requests[0].Header.Get("Accept"))
}

func TestCovInvokeHandlesAnOperationWithNoRequestShape(t *testing.T) {
	// An operation that takes no request value has no fields to accept, so a call
	// with no arguments is simply sent. This is the common shape of a read.
	upstream := covStartRecordingServer(t, http.StatusOK, `{}`, nil)
	operation := covInvokeOperation("get", "")
	operation.Request = nil

	result, err := covInvoke(openapi.NewInvoker(openapi.InvokerOptions{}), api.API{}, covInvokeServer(upstream.URL), operation, "")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, result.Status)
	require.Len(t, upstream.requests, 1)
	assert.Equal(t, "/", upstream.requests[0].URL.Path, "an operation with no path still addresses the server root")
}

func TestCovInvokeReportsATransportFailureAsUnavailable(t *testing.T) {
	// "The server did not answer" is not the same as "the server refused"; the
	// former is unavailable, and a caller retries it while a refusal it does not.
	operation := covInvokeOperation("get", "/things")

	// A closed port is a server that is not there.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := "http://" + listener.Addr().String()
	require.NoError(t, listener.Close())

	_, err = covInvoke(openapi.NewInvoker(openapi.InvokerOptions{RequestTimeout: time.Second}), api.API{}, covInvokeServer(address), operation, "{}")
	require.Error(t, err)
	assert.Equal(t, api.KindUnavailable, api.KindOf(err))

	// A server too slow to answer within the bound is unavailable too, and the
	// bound exists so a call cannot hang forever.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer slow.Close()
	_, err = covInvoke(openapi.NewInvoker(openapi.InvokerOptions{RequestTimeout: 20 * time.Millisecond}), api.API{}, covInvokeServer(slow.URL), operation, "{}")
	require.Error(t, err)
	assert.Equal(t, api.KindUnavailable, api.KindOf(err))

	// An unusable method cannot become a request at all, and that is the caller's
	// mistake rather than a transport failure.
	badMethod := covInvokeOperation("get things", "/things")
	_, err = covInvoke(openapi.NewInvoker(openapi.InvokerOptions{}), api.API{}, covInvokeServer(slow.URL), badMethod, "{}")
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
}

// covFailingReader is a response body that fails part-way through, which is what
// a connection dropped mid-answer looks like to the reader.
type covFailingReader struct{}

func (covFailingReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }
func (covFailingReader) Close() error             { return nil }

// covFailingTransport serves one canned response whose body cannot be read.
type covFailingTransport struct{}

func (covFailingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       covFailingReader{},
	}, nil
}

// covVolatileHeaderTransport serves one answer whose headers are all excluded
// from what the standard model keeps.
type covVolatileHeaderTransport struct{}

func (covVolatileHeaderTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Set-Cookie": []string{"session=secret"}},
		Body:       io.NopCloser(strings.NewReader(`{}`)),
	}, nil
}

func TestCovInvokeReportsAResponseThatCannotBeRead(t *testing.T) {
	// A body that fails mid-read is a failed call, not an empty answer: returning
	// what was read so far would present a truncated value as the server's.
	invoker := openapi.NewInvoker(openapi.InvokerOptions{HTTPClient: &http.Client{Transport: covFailingTransport{}}})
	_, err := covInvoke(invoker, api.API{}, covInvokeServer("http://unused.test"), covInvokeOperation("get", "/things"), "{}")
	require.Error(t, err)
	assert.Equal(t, api.KindUnavailable, api.KindOf(err))
	assert.Contains(t, err.Error(), "read the response")
}

func TestCovInvokeBoundsTheResponseItReads(t *testing.T) {
	// An unbounded body would let any registered API exhaust the host. The limit is
	// explicit, and what is returned is what the limit allowed.
	const maxResponseBytes = 4 << 20
	oversized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", maxResponseBytes+1024)))
	}))
	defer oversized.Close()

	result, err := covInvoke(openapi.NewInvoker(openapi.InvokerOptions{}), api.API{}, covInvokeServer(oversized.URL), covInvokeOperation("get", "/things"), "{}")
	require.NoError(t, err)
	// The truncated body is not JSON, so it is reported as a JSON string rather
	// than as invalid JSON. Its length shows the read stopped at the limit.
	assert.Len(t, string(result.Body), maxResponseBytes+2)
	assert.Equal(t, byte('"'), result.Body[0])
}

func TestCovInvokeNormalizesTheAnswer(t *testing.T) {
	// The answer is always a JSON value: a body that is JSON passes through with
	// unknown fields intact, anything else is reported as a string, and no body is
	// null. A tool result that is not valid JSON would break every caller.
	operation := covInvokeOperation("get", "/things")
	invoker := openapi.NewInvoker(openapi.InvokerOptions{})

	jsonServer := covStartRecordingServer(t, http.StatusOK, `{"ok":true,"extra":[1,2]}`, map[string]string{"Content-Type": "application/json"})
	result, err := covInvoke(invoker, api.API{}, covInvokeServer(jsonServer.URL), operation, "{}")
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true,"extra":[1,2]}`, string(result.Body), "unknown fields survive")
	assert.Equal(t, "application/json", result.ContentType)

	textServer := covStartRecordingServer(t, http.StatusOK, "plain text", map[string]string{"Content-Type": "text/plain"})
	result, err = covInvoke(invoker, api.API{}, covInvokeServer(textServer.URL), operation, "{}")
	require.NoError(t, err)
	assert.JSONEq(t, `"plain text"`, string(result.Body))

	emptyServer := covStartRecordingServer(t, http.StatusNoContent, "", nil)
	result, err = covInvoke(invoker, api.API{}, covInvokeServer(emptyServer.URL), operation, "{}")
	require.NoError(t, err)
	assert.JSONEq(t, `null`, string(result.Body), "an empty body is null rather than an empty value")
}

func TestCovInvokeKeepsOnlySignificantAnswerHeaders(t *testing.T) {
	// Echoing every answer header back would leak set-cookie and authentication
	// headers into a tool result, so those are dropped — and when nothing is left,
	// the map is absent rather than empty.
	operation := covInvokeOperation("get", "/things")
	invoker := openapi.NewInvoker(openapi.InvokerOptions{})

	leaky := covStartRecordingServer(t, http.StatusOK, `{}`, map[string]string{
		"Set-Cookie":       "session=secret",
		"WWW-Authenticate": `Basic realm="x"`,
	})
	result, err := covInvoke(invoker, api.API{}, covInvokeServer(leaky.URL), operation, "{}")
	require.NoError(t, err)
	assert.NotContains(t, result.Headers, "Set-Cookie", "a credential-bearing header is not echoed back")
	assert.NotContains(t, result.Headers, "WWW-Authenticate")

	// When the answer carries nothing but volatile headers, the map is absent
	// rather than empty, so a caller can tell "nothing worth keeping" from "no
	// headers at all".
	volatile := openapi.NewInvoker(openapi.InvokerOptions{HTTPClient: &http.Client{Transport: covVolatileHeaderTransport{}}})
	result, err = covInvoke(volatile, api.API{}, covInvokeServer("http://unused.test"), operation, "{}")
	require.NoError(t, err)
	assert.Nil(t, result.Headers)

	useful := covStartRecordingServer(t, http.StatusOK, `{}`, map[string]string{"X-Request-Id": "abc"})
	result, err = covInvoke(invoker, api.API{}, covInvokeServer(useful.URL), operation, "{}")
	require.NoError(t, err)
	require.NotNil(t, result.Headers)
	assert.Equal(t, []string{"abc"}, result.Headers["X-Request-Id"])
}

func TestCovInvokeReportsTheStatusOfAnErrorAnswer(t *testing.T) {
	// An answer with an error status is still an answer: the invoker reports the
	// status and the body, and classification belongs to the caller that knows what
	// the API's statuses mean. It must not be flattened into a local failure, which
	// would hide the body the server sent to explain itself.
	operation := covInvokeOperation("get", "/things")
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError} {
		upstream := covStartRecordingServer(t, status, `{"error":"nope"}`, map[string]string{"Content-Type": "application/json"})
		result, err := covInvoke(openapi.NewInvoker(openapi.InvokerOptions{}), api.API{}, covInvokeServer(upstream.URL), operation, "{}")
		require.NoError(t, err, "status %d is an answer, not a failure of the call", status)
		assert.Equal(t, status, result.Status)
		assert.JSONEq(t, `{"error":"nope"}`, string(result.Body))
	}
}

func TestCovStaticCredentialsAnswerBySchemeName(t *testing.T) {
	// The static source is what a development deployment uses; it answers only for
	// the schemes it holds a value for, and holding one is what makes a call
	// possible.
	credentials := openapi.StaticCredentials{"auth": "secret"}
	value, ok := credentials.Credential(api.SecurityScheme{Name: "auth"}, api.SecurityRequirement{Scheme: "auth"})
	assert.True(t, ok)
	assert.Equal(t, "secret", value)

	_, ok = credentials.Credential(api.SecurityScheme{Name: "other"}, api.SecurityRequirement{Scheme: "other"})
	assert.False(t, ok)
}
