package pprofserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/temporalio/temporal-proxy/internal/config"
	"github.com/temporalio/temporal-proxy/pkg/logger"
)

// These tests drive the routes the way a caller does: a real HTTP client
// against a real listener, no handler reached directly. The profile one is the
// strictest in the package, because the bytes it parses come from the running
// test binary rather than from a fixture.

func TestClientSeesTheVisualizer(t *testing.T) {
	t.Parallel()

	svr := httptest.NewServer(newHandler())
	t.Cleanup(svr.Close)

	resp, err := svr.Client().Get(svr.URL + route + "ui/")
	require.NoError(t, err)
	body := responseBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, body, `<select name="type">`)
	require.Contains(t, body, `<option value="heap">Heap (in use)</option>`)

	resp, err = svr.Client().Get(svr.URL + route + "ui/flame?type=heap&gc=1")
	require.NoError(t, err)
	body = responseBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, body, "Heap (in use) profile")
	require.Contains(t, body, "<svg")
	require.Contains(t, body, "root")

	resp, err = svr.Client().Get(svr.URL + route + "ui/flame?type=goroutine")
	require.NoError(t, err)
	body = responseBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, body, "Goroutines profile")
	require.Contains(t, body, "<svg")

	resp, err = svr.Client().Get(svr.URL + route + "ui/flame?type=nonsense")
	require.NoError(t, err)
	body = responseBody(t, resp)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Contains(t, body, "unknown profile type")
}

// TestClientRendersARealProfile reads the heap endpoint over HTTP and pushes
// the bytes through the parser and the flame graph, the same trip a browser's
// form submission takes, minus the page wrapping.
func TestClientRendersARealProfile(t *testing.T) {
	t.Parallel()

	svr := httptest.NewServer(newHandler())
	t.Cleanup(svr.Close)

	resp, err := svr.Client().Get(svr.URL + route + "heap")
	require.NoError(t, err)
	raw := responseBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	prof, err := parseProfile([]byte(raw))
	require.NoError(t, err)
	require.NotEmpty(t, prof.types)
	require.NotEmpty(t, prof.samples)

	tree := buildFlame(prof, "inuse_space")
	require.Greater(t, tree.total, int64(0))

	svg := tree.svg()
	require.Contains(t, svg, "<svg")
	require.Contains(t, svg, "root")
}

func TestClientAuthenticatesOverTheFullStack(t *testing.T) {
	t.Parallel()

	routes, err := newFromParams(Params{
		Config: &config.Config{
			HTTP: config.HTTP{
				PProf: config.PProf{
					Enabled: true,
					Auth: &config.AuthConfig{
						StaticToken: &config.StaticTokenConfig{Token: "s3cret"},
					},
				},
			},
		},
		Logger: logger.NewTestLogger(),
	})
	require.NoError(t, err)

	svr := httptest.NewServer(routes[0].Handler)
	t.Cleanup(svr.Close)

	resp, err := svr.Client().Get(svr.URL + route + "ui/")
	require.NoError(t, err)
	_ = responseBody(t, resp)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// The static token scheme reads a Bearer credential from the
	// Authorization header, its default when config names no other.
	req, err := http.NewRequest(http.MethodGet, svr.URL+route+"ui/", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer s3cret")

	resp, err = svr.Client().Do(req)
	require.NoError(t, err)
	_ = responseBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func responseBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return string(body)
}
