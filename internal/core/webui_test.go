package core

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebUIAssetsAreEmbeddedAndSeparateFromAPIAuth(t *testing.T) {
	handler := NewHTTPHandler("secret")
	for _, path := range []string{"/", "/app.js", "/styles.css"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("web UI path %s status = %d", path, response.Code)
		}
		if response.Body.Len() == 0 {
			t.Fatalf("web UI path %s returned an empty body", path)
		}
		if response.Header().Get("Content-Security-Policy") == "" {
			t.Fatalf("web UI path %s is missing Content-Security-Policy", path)
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if !strings.Contains(response.Body.String(), "aiwr") {
		t.Fatalf("root UI does not contain the application name")
	}
	if strings.Contains(response.Body.String(), "id=\"apiKey\"") {
		t.Fatalf("default UI unexpectedly exposes an API key field")
	}

	request = httptest.NewRequest(http.MethodGet, "/health", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized API status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("non-GET UI status = %d", response.Code)
	}
}
