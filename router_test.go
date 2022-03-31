package tea

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewRouter(t *testing.T) {
	t.Parallel()
	t.Run("can create instance", func(t *testing.T) {
		r := NewRouter("0")
		assert.NotNil(t, r)
	})
}

func TestHealth(t *testing.T) {
	t.Parallel()

	t.Run("get", func(t *testing.T) {
		r := NewRouter("0")
		req := NewRequestBuilder(t).
			Method("GET").
			Path("/v0/health/status").
			ExpectRoute("/health/status")

		RequestTest(t, r, req)
	})
}

func TestRouter_Route(t *testing.T) {
	t.Parallel()
	t.Run("get", func(t *testing.T) {
		r := NewRouter("0")
		req := NewRequestBuilder(t).
			Method("GET").
			Path("/v0/tests").
			ExpectRoute("/tests").
			ExpectResponse("ok")

		RequestTest(t, r, req)
	})

	t.Run("post", func(t *testing.T) {
		r := NewRouter("0")
		req := NewRequestBuilder(t).
			Method("POST").
			Path("/v0/tests").
			Body("test").
			ExpectRoute("/tests").
			ExpectResponse("ok")

		RequestTest(t, r, req)
	})

	t.Run("put", func(t *testing.T) {
		r := NewRouter("0")
		req := NewRequestBuilder(t).
			Method("PUT").
			Path("/v0/tests/test").
			Body("test").
			ExpectRoute("/tests/test").
			ExpectResponse("ok")

		RequestTest(t, r, req)
	})

	t.Run("patch", func(t *testing.T) {
		r := NewRouter("0")
		req := NewRequestBuilder(t).
			Method("PATCH").
			Path("/v0/tests/test").
			Body("test").
			ExpectRoute("/tests/test").
			ExpectResponse("ok")

		RequestTest(t, r, req)
	})

	t.Run("delete", func(t *testing.T) {
		r := NewRouter("0")
		req := NewRequestBuilder(t).
			Method("DELETE").
			Path("/v0/tests/test").
			ExpectRoute("/tests/test").
			ExpectResponse("ok")

		RequestTest(t, r, req)
	})
}

func TestRouter_Middleware(t *testing.T) {
	t.Parallel()
	t.Run("processes handler", func(t *testing.T) {
		r := NewRouter("0")
		r.Middleware(MiddlewareFunc(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})
		}))
		req := NewRequestBuilder(t).
			Method("GET").
			Path("/v0/tests").
			ExpectRoute("/tests").
			ExpectStatus(http.StatusNoContent)

		RequestTest(t, r, req)
	})
}

func TestNotFoundHandler(t *testing.T) {
	t.Parallel()
	t.Run("sends response", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/tests", nil)
		w := httptest.NewRecorder()

		NotFoundHandler(w, r)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, http.StatusText(http.StatusNotFound), w.Body.String())
	})

	t.Run("routes not found", func(t *testing.T) {
		r := NewRouter("0")
		req := NewRequestBuilder(t).
			Method("GET").
			Path("/v0/tests/test").
			ExpectRoute("/tests").
			ExpectStatus(http.StatusNotFound).
			ExpectResponse(http.StatusText(http.StatusNotFound))

		RequestTest(t, r, req)
	})

	t.Run("health check", func(t *testing.T) {
		r := NewRouter("0")
		req := NewRequestBuilder(t).
			Method("GET").
			Path("/health/status").
			ExpectRoute("/health/status").
			ExpectStatus(http.StatusOK)

		RequestTest(t, r, req)
	})
}

func TestMethodNotAllowedHandler(t *testing.T) {
	t.Parallel()
	t.Run("sends response", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/tests", nil)
		w := httptest.NewRecorder()

		MethodNotAllowedHandler(w, r)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
		assert.Equal(t, http.StatusText(http.StatusMethodNotAllowed), w.Body.String())
	})

	t.Run("routes method not allowed", func(t *testing.T) {
		r := NewRouter("0")
		req := NewRequestBuilder(t).
			Method("GET").
			Path("/v0/tests").
			ExpectRoute("/tests").
			ExpectMethod("POST").
			ExpectStatus(http.StatusMethodNotAllowed).
			ExpectResponse(http.StatusText(http.StatusMethodNotAllowed))

		RequestTest(t, r, req)
	})
}

func RequestTest(t *testing.T, r *Router, b *RequestBuilder) {
	t.Helper()
	want := b.Response()

	var expected []byte
	if want.Body != nil {
		bytes, err := io.ReadAll(want.Body)
		assert.Nil(t, err)
		expected = bytes
		err = want.Body.Close()
		assert.Nil(t, err)
	}

	handlerFunc := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(want.StatusCode)

		if len(expected) > 0 {
			_, err := w.Write(expected)
			assert.Nil(t, err)
		}
	}

	r.Route(strings.ToUpper(b.ExpectedMethod()), b.ExpectedRoute(), handlerFunc)
	s := httptest.NewServer(r)
	defer s.Close()
	req := b.Request(s.URL)
	got, err := s.Client().Do(req)
	assert.Nil(t, err)

	body, err := io.ReadAll(got.Body)
	assert.Nil(t, err)
	err = got.Body.Close()
	assert.Nil(t, err)

	assert.Equal(t, want.StatusCode, got.StatusCode)
	assert.NotNil(t, body)
}

type RequestBuilder struct {
	t      *testing.T
	path   string
	method string
	body   string
	router struct {
		method string
		path   string
	}
	response struct {
		code int
		body string
	}
}

func (b *RequestBuilder) Method(method string) *RequestBuilder {
	b.method = method
	if b.router.method == "" {
		b.router.method = method
	}

	return b
}

func (b *RequestBuilder) Path(path string) *RequestBuilder {
	b.path = path

	return b
}

func (b *RequestBuilder) Body(body string) *RequestBuilder {
	b.body = body
	return b
}

func (b *RequestBuilder) ExpectStatus(code int) *RequestBuilder {
	b.response.code = code
	return b
}

func (b *RequestBuilder) ExpectRoute(path string) *RequestBuilder {
	b.router.path = path
	return b
}

func (b *RequestBuilder) ExpectedRoute() string {
	return b.router.path
}

func (b *RequestBuilder) ExpectMethod(method string) *RequestBuilder {
	b.router.method = method
	return b
}

func (b *RequestBuilder) ExpectedMethod() string {
	return b.router.method
}

func (b *RequestBuilder) ExpectResponse(body string) *RequestBuilder {
	b.response.body = body
	return b
}

func (b *RequestBuilder) Request(base string) *http.Request {
	b.t.Helper()
	var body io.Reader
	if b.body != "" {
		body = io.NopCloser(strings.NewReader(b.body))
	}

	u := fmt.Sprintf("%s/%s?test", base, strings.TrimPrefix(b.path, "/"))
	req, err := http.NewRequest(b.method, u, body)
	assert.Nil(b.t, err)

	return req
}

func (b *RequestBuilder) Response() *http.Response {
	w := http.Response{
		StatusCode: b.response.code,
	}

	if len(b.response.body) > 0 {
		w.Body = io.NopCloser(strings.NewReader(b.response.body))
	}

	return &w
}

func NewRequestBuilder(t *testing.T) *RequestBuilder {
	b := RequestBuilder{t: t}
	b.response.code = http.StatusOK
	return &b
}
