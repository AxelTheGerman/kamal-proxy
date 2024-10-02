package server

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/basecamp/kamal-proxy/internal/pages"
)

func TestErrorPageMiddleware(t *testing.T) {
	check := func(handler http.HandlerFunc) (int, string, string) {
		middleware, err := WithErrorPageMiddleware(pages.DefaultErrorPages, true, handler)
		require.NoError(t, err)

		req := httptest.NewRequest("GET", "http://example.com", nil)
		resp := httptest.NewRecorder()

		middleware.ServeHTTP(resp, req)

		return resp.Result().StatusCode, resp.Header().Get("Content-Type"), resp.Body.String()
	}

	t.Run("When setting a custom error response", func(t *testing.T) {
		status, contentType, body := check(func(w http.ResponseWriter, r *http.Request) {
			SetErrorResponse(w, r, http.StatusNotFound, nil)
		})

		assert.Equal(t, http.StatusNotFound, status)
		assert.Equal(t, "text/html; charset=utf-8", contentType)
		assert.Regexp(t, "Not Found", body)
	})

	t.Run("When including template arguments in a custom error response", func(t *testing.T) {
		status, contentType, body := check(func(w http.ResponseWriter, r *http.Request) {
			SetErrorResponse(w, r, http.StatusServiceUnavailable, struct{ Message string }{"Gone to lunch"})
		})

		assert.Equal(t, http.StatusServiceUnavailable, status)
		assert.Equal(t, "text/html; charset=utf-8", contentType)
		assert.Regexp(t, "Service Temporarily Unavailable", body)
		assert.Regexp(t, "Gone to lunch", body)
	})

	t.Run("When trying to set an error that we don't have a template for", func(t *testing.T) {
		status, contentType, body := check(func(w http.ResponseWriter, r *http.Request) {
			SetErrorResponse(w, r, http.StatusTeapot, nil)
		})

		assert.Equal(t, http.StatusTeapot, status)
		assert.Equal(t, "text/html; charset=utf-8", contentType)
		assert.Regexp(t, "I'm a teapot", body)
	})

	t.Run("When the backend returns an error normally", func(t *testing.T) {
		status, contentType, body := check(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, http.StatusText(http.StatusTeapot), http.StatusTeapot)
		})

		assert.Equal(t, http.StatusTeapot, status)
		assert.Equal(t, "text/plain; charset=utf-8", contentType)
		assert.Regexp(t, "I'm a teapot", body)
	})
}

func TestErrorPageMiddleware_Nesting(t *testing.T) {
	check := func(handler http.HandlerFunc) (int, string, string) {
		customPages := fstest.MapFS(map[string]*fstest.MapFile{
			"404.html": {Data: []byte("<body>Custom 404</body>")},
		})

		middleware, _ := WithErrorPageMiddleware(customPages, false, handler)
		middleware, _ = WithErrorPageMiddleware(pages.DefaultErrorPages, true, middleware)

		req := httptest.NewRequest("GET", "http://example.com", nil)
		resp := httptest.NewRecorder()

		middleware.ServeHTTP(resp, req)

		return resp.Result().StatusCode, resp.Header().Get("Content-Type"), resp.Body.String()
	}

	t.Run("With an error in the inner FS", func(t *testing.T) {
		status, contentType, body := check(func(w http.ResponseWriter, r *http.Request) {
			SetErrorResponse(w, r, http.StatusNotFound, nil)
		})

		assert.Equal(t, http.StatusNotFound, status)
		assert.Equal(t, "text/html; charset=utf-8", contentType)
		assert.Regexp(t, "Custom 404", body)
	})

	t.Run("With an error not in the inner FS", func(t *testing.T) {
		status, contentType, body := check(func(w http.ResponseWriter, r *http.Request) {
			SetErrorResponse(w, r, http.StatusServiceUnavailable, struct{ Message string }{"Gone to lunch"})
		})

		assert.Equal(t, http.StatusServiceUnavailable, status)
		assert.Equal(t, "text/html; charset=utf-8", contentType)
		assert.Regexp(t, "Service Temporarily Unavailable", body)
		assert.Regexp(t, "Gone to lunch", body)
	})

	t.Run("With an error not in any FS", func(t *testing.T) {
		status, contentType, body := check(func(w http.ResponseWriter, r *http.Request) {
			SetErrorResponse(w, r, http.StatusTeapot, nil)
		})

		assert.Equal(t, http.StatusTeapot, status)
		assert.Equal(t, "text/html; charset=utf-8", contentType)
		assert.Regexp(t, "I'm a teapot", body)
	})
}

func TestErrorPageMiddleware_WithInvalidArguments(t *testing.T) {
	ensureFailed := func(pages fs.FS) {
		handler := func(w http.ResponseWriter, r *http.Request) {}
		_, err := WithErrorPageMiddleware(pages, false, http.HandlerFunc(handler))

		assert.Equal(t, ErrorUnableToLoadErrorPages, err)
	}

	t.Run("With templates that cannot be compiled", func(t *testing.T) {
		pages := fstest.MapFS(map[string]*fstest.MapFile{
			"404.html": {Data: []byte("<body>{{ {{</body>")},
		})
		ensureFailed(pages)
	})

	t.Run("With a filesystem that has no templates", func(t *testing.T) {
		pages := fstest.MapFS(map[string]*fstest.MapFile{})
		ensureFailed(pages)
	})
}
