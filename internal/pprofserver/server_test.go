package pprofserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/temporalio/temporal-proxy/internal/config"
	"github.com/temporalio/temporal-proxy/pkg/logger"
)

func TestNewFromParamsDisabled(t *testing.T) {
	t.Parallel()

	routes, err := newFromParams(Params{
		Config: &config.Config{},
		Logger: logger.NewTestLogger(),
	})
	require.NoError(t, err)
	require.Empty(t, routes)
}

func TestNewFromParamsEnabled(t *testing.T) {
	t.Parallel()

	routes, err := newFromParams(Params{
		Config: &config.Config{
			HTTP: config.HTTP{PProf: config.PProf{Enabled: true}},
		},
		Logger: logger.NewTestLogger(),
	})
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.Equal(t, route, routes[0].Pattern)
}

func TestNewFromParamsWithAuth(t *testing.T) {
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
	require.Len(t, routes, 1)

	// An unauthenticated caller is rejected before any handler runs.
	rec := httptest.NewRecorder()
	routes[0].Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, route, nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestNewFromParamsBadAuth(t *testing.T) {
	t.Parallel()

	_, err := newFromParams(Params{
		Config: &config.Config{
			HTTP: config.HTTP{
				PProf: config.PProf{
					Enabled: true,
					Auth:    &config.AuthConfig{},
				},
			},
		},
		Logger: logger.NewTestLogger(),
	})
	require.Error(t, err)
}

func TestHandlerRawEndpoints(t *testing.T) {
	t.Parallel()

	h := newHandler()

	// The standard index lists the profile endpoints as relative links.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, route, nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `href="heap?debug=1">heap</a>`)
	require.Contains(t, rec.Body.String(), `<a href="profile?seconds=`)
}

func TestHandlerUIPages(t *testing.T) {
	t.Parallel()

	h := newHandler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, route+"ui/", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `<select name="type">`)
	require.Contains(t, rec.Body.String(), `<option value="heap">Heap (in use)</option>`)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, route+"ui/flame?type=goroutine", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "Goroutines profile")
	require.Contains(t, rec.Body.String(), "<svg")

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, route+"ui/flame?type=nonsense", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, route+"ui/elsewhere", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandlerUIFlameHeapWithGC(t *testing.T) {
	t.Parallel()

	h := newHandler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, route+"ui/flame?type=heap&gc=1", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "Heap (in use) profile")
	require.Contains(t, rec.Body.String(), "<svg")
}

func TestCaptureWriter(t *testing.T) {
	t.Parallel()

	cw := &captureWriter{}
	cw.Header().Set("Content-Type", "application/octet-stream")
	n, err := cw.Write([]byte("hello"))
	require.NoError(t, err)
	require.Equal(t, 5, n)
	require.Equal(t, http.StatusOK, cw.code)
	require.Equal(t, "hello", cw.buf.String())
}
