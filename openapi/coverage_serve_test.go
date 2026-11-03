package openapi_test

import (
	"bytes"
	"encoding/json"
	"io"
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

// These tests hold the adapted surface to its promises: every route the
// description declares is reachable, everything else under the base path is
// not, the documentation and schema endpoints exist, and a tunnelled request or
// answer that fails is reported as a failure rather than passed off as the
// original server's answer.

// covAPI builds a normalized description from operations, so a test can say
// exactly which routes it wants served.
func covAPI(t *testing.T, operations ...api.Operation) api.API {
	t.Helper()
	described := api.API{
		ID:      "cov-shop",
		Name:    "cov-shop",
		Version: "1.0.0",
		Title:   "Cov shop",
		Format:  openapi.Format,
		Services: []api.Service{{
			Name:       "things",
			Operations: operations,
		}},
	}
	normalized, err := described.Normalize()
	require.NoError(t, err)
	return normalized
}

// covOriginalServer is the API a surface tunnels to. It records what it received
// so a test can prove the tunnel forwards the request it was given.
type covOriginalServer struct {
	*httptest.Server
	requests []*http.Request
	bodies   []string
}

func covStartOriginalServer(t *testing.T) *covOriginalServer {
	t.Helper()
	original := &covOriginalServer{}
	original.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		original.requests = append(original.requests, r.Clone(r.Context()))
		body, _ := io.ReadAll(r.Body)
		original.bodies = append(original.bodies, string(body))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(original.Close)
	return original
}

// covServe starts a surface on a loopback port and stops it when the test ends.
func covServe(t *testing.T, surfaces *openapi.Surfaces, described api.API, server api.Server, options openapi.ServeOptions) *openapi.Served {
	t.Helper()
	served, err := surfaces.Serve(described, server, options)
	require.NoError(t, err)
	t.Cleanup(func() { surfaces.Stop(served.InstanceID) })
	return served
}

// covOrigin strips the base path from a served endpoint, leaving scheme and
// authority, because the reported endpoint paths are absolute.
func covOrigin(endpoint string) string {
	trimmed := strings.TrimPrefix(endpoint, "http://")
	if index := strings.Index(trimmed, "/"); index >= 0 {
		return "http://" + trimmed[:index]
	}
	return "http://" + trimmed
}

// covGet performs a request and returns the status and body.
func covGet(t *testing.T, url string) (int, string) {
	t.Helper()
	response, err := (&http.Client{Timeout: 5 * time.Second}).Get(url)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return response.StatusCode, string(body)
}

func TestCovServedSurfaceAnswersOnlyItsOwnRoutes(t *testing.T) {
	// A path or method the description does not declare is not forwarded: the
	// surface belongs to the adapted API, and answering an undeclared call from the
	// original server would serve an operation the policy never saw.
	original := covStartOriginalServer(t)
	surfaces := openapi.NewSurfaces(openapi.SurfaceOptions{})
	described := covAPI(t,
		api.Operation{Name: "listThings", Method: "get", Path: "/things"},
		api.Operation{Name: "createThing", Method: "post", Path: "/things"},
	)
	served := covServe(t, surfaces, described, api.Server{
		ID: "cov-host", Name: "Cov host", BaseURL: original.URL, Format: openapi.Format,
	}, openapi.ServeOptions{})

	status, _ := covGet(t, served.Endpoint+"/things")
	assert.Equal(t, http.StatusOK, status)

	// A method the path does not declare is a miss, not a tunnel.
	request, err := http.NewRequest(http.MethodPut, served.Endpoint+"/things", nil)
	require.NoError(t, err)
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	assert.Equal(t, http.StatusNotFound, response.StatusCode)

	status, _ = covGet(t, served.Endpoint+"/unknown")
	assert.Equal(t, http.StatusNotFound, status)

	// A request for the surface's own root matches no operation.
	status, _ = covGet(t, served.Endpoint)
	assert.Equal(t, http.StatusNotFound, status)

	// The documentation endpoint answers exactly one path: a subpage of it is not
	// part of the target's documentation.
	status, _ = covGet(t, covOrigin(served.Endpoint)+served.BasePath+"/_docs/extra")
	assert.Equal(t, http.StatusNotFound, status)

	assert.Len(t, original.requests, 1, "only the declared operation reached the original server")
}

func TestCovServeReportsOperationsItCannotServe(t *testing.T) {
	// An operation whose path is not an HTTP path cannot be mounted. It is reported
	// and left out, because a service that pretends to serve it would fail at call
	// time with no explanation.
	original := covStartOriginalServer(t)
	surfaces := openapi.NewSurfaces(openapi.SurfaceOptions{})
	described := covAPI(t,
		api.Operation{Name: "good", Method: "get", Path: "/things"},
		api.Operation{Name: "relative", Method: "get", Path: "things"},
	)
	served := covServe(t, surfaces, described, api.Server{
		ID: "cov-host", Name: "Cov host", BaseURL: original.URL, Format: openapi.Format,
	}, openapi.ServeOptions{})
	assert.Contains(t, served.Warnings, `operation cov-shop/things/relative is not served: path "things" must start with /`)

	// A description with nothing mountable is refused rather than served empty.
	_, err := surfaces.Serve(covAPI(t, api.Operation{Name: "relative", Method: "get", Path: "things"}), api.Server{
		ID: "cov-host", Name: "Cov host", BaseURL: original.URL, Format: openapi.Format,
	}, openapi.ServeOptions{})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), `has no operation this package can serve`)
}

func TestCovServeReportsADuplicateRouteAndServesTheFirst(t *testing.T) {
	// Two operations on one method and path cannot both be mounted: a request
	// carries no information that would choose between them. The first is served
	// and the rest are reported.
	original := covStartOriginalServer(t)
	surfaces := openapi.NewSurfaces(openapi.SurfaceOptions{})
	described := api.API{
		ID: "duplicated", Name: "duplicated", Format: openapi.Format,
		Services: []api.Service{
			{Name: "first", Operations: []api.Operation{{Name: "getThing", Method: "get", Path: "/things"}}},
			{Name: "second", Operations: []api.Operation{{Name: "fetchThing", Method: "get", Path: "/things"}}},
		},
	}
	normalized, err := described.Normalize()
	require.NoError(t, err)

	served := covServe(t, surfaces, normalized, api.Server{
		ID: "cov-host", Name: "Cov host", BaseURL: original.URL, Format: openapi.Format,
	}, openapi.ServeOptions{})
	assert.Contains(t, served.Warnings, "operation duplicated/second/fetchThing duplicates get /things; only the first is served")
	assert.Equal(t, []string{"/_toolbox/adapted/things"}, served.Paths)
}

func TestCovServeSortsRoutesForAStableListing(t *testing.T) {
	// The reported paths are what a caller is told the surface answers on, so they
	// are sorted: one path with two methods and several paths, which is where the
	// ordering rule chooses between path and method.
	original := covStartOriginalServer(t)
	surfaces := openapi.NewSurfaces(openapi.SurfaceOptions{})
	described := covAPI(t,
		api.Operation{Name: "listThings", Method: "get", Path: "/things"},
		api.Operation{Name: "createThing", Method: "post", Path: "/things"},
		api.Operation{Name: "listOther", Method: "get", Path: "/other"},
	)
	served := covServe(t, surfaces, described, api.Server{
		ID: "cov-host", Name: "Cov host", BaseURL: original.URL, Format: openapi.Format,
	}, openapi.ServeOptions{})
	assert.Equal(t, []string{
		"/_toolbox/adapted/other",
		"/_toolbox/adapted/things",
		"/_toolbox/adapted/things",
	}, served.Paths)
}

func TestCovServeHonoursARequestedBasePath(t *testing.T) {
	// A caller may choose the prefix, and a prefix written without a slash or with
	// a trailing one is normalized rather than refused: the caller's intent is
	// clear, and a moved prefix would invalidate every URL it was told about.
	original := covStartOriginalServer(t)
	surfaces := openapi.NewSurfaces(openapi.SurfaceOptions{})
	served := covServe(t, surfaces, covAPI(t,
		api.Operation{Name: "listThings", Method: "get", Path: "/things"},
	), api.Server{
		ID: "cov-host", Name: "Cov host", BaseURL: original.URL, Format: openapi.Format,
	}, openapi.ServeOptions{BasePath: "api/v1/", ListenAddress: "127.0.0.1:0"})

	assert.Equal(t, "/api/v1", served.BasePath)
	assert.Equal(t, []string{"/api/v1/things"}, served.Paths)
	status, body := covGet(t, served.Endpoint+"/things")
	assert.Equal(t, http.StatusOK, status)
	assert.JSONEq(t, `{"ok":true}`, body)
}

func TestCovServeRefusesABasePathThatWouldCollide(t *testing.T) {
	// A prefix that would shadow an adapted operation is refused rather than
	// silently moved, because a moved prefix would invalidate the URLs the caller
	// was told about. The same holds for a prefix that would shadow the original
	// server's own path.
	original := covStartOriginalServer(t)
	surfaces := openapi.NewSurfaces(openapi.SurfaceOptions{})
	described := covAPI(t, api.Operation{Name: "listThings", Method: "get", Path: "/things"})
	server := api.Server{ID: "cov-host", Name: "Cov host", BaseURL: original.URL, Format: openapi.Format}

	_, err := surfaces.Serve(described, server, openapi.ServeOptions{BasePath: "/things"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be used")

	// "/" normalizes to the empty prefix, which every path is under.
	_, err = surfaces.Serve(described, server, openapi.ServeOptions{BasePath: "/"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be used")

	_, err = surfaces.Serve(described, api.Server{
		ID: "cov-host", Name: "Cov host", BaseURL: original.URL + "/shop", Format: openapi.Format,
	}, openapi.ServeOptions{BasePath: "/shop"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "would shadow the original server's path")
}

func TestCovServeRefusesAnAPIWhosePathsOccupyEveryBasePath(t *testing.T) {
	// The default prefixes are tried in order. An API that serves under all of them
	// cannot be mounted anywhere without shadowing itself, and the refusal says so
	// rather than picking a path that overwrites an operation.
	original := covStartOriginalServer(t)
	surfaces := openapi.NewSurfaces(openapi.SurfaceOptions{})
	described := covAPI(t,
		api.Operation{Name: "one", Method: "get", Path: "/_toolbox/adapted/x"},
		api.Operation{Name: "two", Method: "get", Path: "/adapted/x"},
		api.Operation{Name: "three", Method: "get", Path: "/_adapted/x"},
		api.Operation{Name: "four", Method: "get", Path: "/api/x"},
	)
	_, err := surfaces.Serve(described, api.Server{
		ID: "cov-host", Name: "Cov host", BaseURL: original.URL, Format: openapi.Format,
	}, openapi.ServeOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no base path avoids the paths this API serves")
}

func TestCovServeRefusesAnUnusableOriginalServerOrAddress(t *testing.T) {
	// An adapted surface forwards somewhere; a location that is not a URL, or a
	// listen address that cannot be bound, is a deployment mistake and is refused
	// with the classification a caller can act on.
	original := covStartOriginalServer(t)
	surfaces := openapi.NewSurfaces(openapi.SurfaceOptions{})
	described := covAPI(t, api.Operation{Name: "listThings", Method: "get", Path: "/things"})

	_, err := surfaces.Serve(described, api.Server{ID: "h", Name: "H", BaseURL: "not a url", Format: openapi.Format}, openapi.ServeOptions{})
	require.Error(t, err)
	assert.Equal(t, api.KindInvalid, api.KindOf(err))
	assert.Contains(t, err.Error(), "invalid base url")

	_, err = surfaces.Serve(described, api.Server{ID: "h", Name: "H", BaseURL: "://broken", Format: openapi.Format}, openapi.ServeOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid base url")

	_, err = surfaces.Serve(described, api.Server{
		ID: "h", Name: "H", BaseURL: original.URL, Format: openapi.Format,
	}, openapi.ServeOptions{ListenAddress: "127.0.0.1:999999"})
	require.Error(t, err)
	assert.Equal(t, api.KindUnavailable, api.KindOf(err))
}

func TestCovSurfacesListAndStopTheirInstances(t *testing.T) {
	// A caller stops what it started by identifier, and the registry is what tells
	// it what is running. An unknown or empty identifier is answered honestly
	// rather than refused.
	original := covStartOriginalServer(t)
	surfaces := openapi.NewSurfaces(openapi.SurfaceOptions{})
	assert.Empty(t, surfaces.Instances())
	assert.False(t, surfaces.Stop(""))

	described := covAPI(t, api.Operation{Name: "listThings", Method: "get", Path: "/things"})
	served := covServe(t, surfaces, described, api.Server{
		ID: "cov-host", Name: "Cov host", BaseURL: original.URL, Format: openapi.Format,
	}, openapi.ServeOptions{})

	assert.Equal(t, []string{served.InstanceID}, surfaces.Instances())
	assert.True(t, surfaces.Stop(served.InstanceID))
	assert.Empty(t, surfaces.Instances())
	assert.False(t, surfaces.Stop(served.InstanceID))
}

func TestCovTunnelRefusesAnOversizedBody(t *testing.T) {
	// A tunnelled body is bounded: an adapted surface that read an unbounded body
	// into memory would let any caller exhaust the host. The refusal is a 4xx,
	// because the caller can send less.
	original := covStartOriginalServer(t)
	surfaces := openapi.NewSurfaces(openapi.SurfaceOptions{})
	served := covServe(t, surfaces, covAPI(t,
		api.Operation{Name: "upload", Method: "post", Path: "/upload"},
	), api.Server{
		ID: "cov-host", Name: "Cov host", BaseURL: original.URL, Format: openapi.Format,
	}, openapi.ServeOptions{})

	oversized := bytes.Repeat([]byte("a"), (8<<20)+10)
	request, err := http.NewRequest(http.MethodPost, served.Endpoint+"/upload", bytes.NewReader(oversized))
	require.NoError(t, err)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	assert.Equal(t, http.StatusRequestEntityTooLarge, response.StatusCode)
	assert.Empty(t, original.requests, "an oversized body is not forwarded")
}

func TestCovTunnelReportsAnUnreachableOriginalServer(t *testing.T) {
	// A tunnel that cannot reach the original server is a 502: the request was
	// well formed, and the failure belongs to the hop the caller cannot see. It
	// must not be presented as the original server's own answer.
	original := covStartOriginalServer(t)
	surfaces := openapi.NewSurfaces(openapi.SurfaceOptions{})
	served := covServe(t, surfaces, covAPI(t,
		api.Operation{Name: "listThings", Method: "get", Path: "/things"},
	), api.Server{
		ID: "cov-host", Name: "Cov host", BaseURL: original.URL, Format: openapi.Format,
	}, openapi.ServeOptions{})

	original.Close()

	status, body := covGet(t, served.Endpoint+"/things")
	assert.Equal(t, http.StatusBadGateway, status)
	assert.Contains(t, body, "the original server did not answer")
}

func TestCovServedRootOperation(t *testing.T) {
	// A description may declare the root path, and the surface's own root is then a
	// real route. It is the one path where the split-and-match code has to treat an
	// empty path as the single empty segment.
	original := covStartOriginalServer(t)
	surfaces := openapi.NewSurfaces(openapi.SurfaceOptions{})
	served := covServe(t, surfaces, covAPI(t,
		api.Operation{Name: "root", Method: "get", Path: "/"},
	), api.Server{
		ID: "cov-host", Name: "Cov host", BaseURL: original.URL, Format: openapi.Format,
	}, openapi.ServeOptions{})

	status, body := covGet(t, served.Endpoint+"/")
	assert.Equal(t, http.StatusOK, status)
	assert.JSONEq(t, `{"ok":true}`, body)
	require.Len(t, original.requests, 1)
	assert.Equal(t, "/", original.requests[0].URL.Path)
}

func TestCovTunnelForwardsTheBodyAndHeaders(t *testing.T) {
	// The tunnel is a proxy, not a reimplementation: the method, body, and headers
	// the caller sent are what the original server receives.
	original := covStartOriginalServer(t)
	surfaces := openapi.NewSurfaces(openapi.SurfaceOptions{})
	served := covServe(t, surfaces, covAPI(t,
		api.Operation{Name: "send", Method: "post", Path: "/things/{thingId}"},
	), api.Server{
		ID: "cov-host", Name: "Cov host", BaseURL: original.URL + "/base", Format: openapi.Format,
	}, openapi.ServeOptions{})

	request, err := http.NewRequest(http.MethodPost, served.Endpoint+"/things/9?verbose=true", strings.NewReader(`{"name":"thing"}`))
	require.NoError(t, err)
	request.Header.Set("X-Trace", "trace-1")
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	assert.Equal(t, http.StatusOK, response.StatusCode)

	require.Len(t, original.requests, 1)
	received := original.requests[0]
	assert.Equal(t, http.MethodPost, received.Method)
	assert.Equal(t, "/base/things/9", received.URL.Path, "the original base path and the captured parameter are put back")
	assert.Equal(t, "true", received.URL.Query().Get("verbose"))
	assert.Equal(t, "trace-1", received.Header.Get("X-Trace"))
	require.Len(t, original.bodies, 1)
	assert.JSONEq(t, `{"name":"thing"}`, original.bodies[0])
}

func TestCovSchemaEndpointsAreDiscoverableAndDownloadable(t *testing.T) {
	// The schema is what a client generator reads, so it is downloadable in both
	// serializations, as a file when asked, and the directory lists what exists.
	original := covStartOriginalServer(t)
	surfaces := openapi.NewSurfaces(openapi.SurfaceOptions{})
	served := covServe(t, surfaces, covAPI(t,
		api.Operation{Name: "listThings", Method: "get", Path: "/things"},
	), api.Server{
		ID: "cov-host", Name: "Cov host", BaseURL: original.URL, Format: openapi.Format,
	}, openapi.ServeOptions{})
	base := covOrigin(served.Endpoint) + served.BasePath

	for _, name := range []string{"openapi.json", "openapi.yaml"} {
		for _, query := range []string{"", "?download=yes", "?download=1", "?download=true"} {
			status, body := covGet(t, base+"/_schema/"+name+query)
			require.Equal(t, http.StatusOK, status, name+query)
			assert.NotEmpty(t, body)
		}
	}
	status, listing := covGet(t, base+"/_schema/")
	assert.Equal(t, http.StatusOK, status)
	var files map[string]any
	require.NoError(t, json.Unmarshal([]byte(listing), &files))
	assert.Contains(t, listing, "openapi.json")
	assert.Contains(t, listing, "openapi.yaml")
}
