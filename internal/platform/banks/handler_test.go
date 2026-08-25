package banks

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestHandlerPanicsOnNilDirectory(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected NewHandler(nil) to panic")
		}
	}()
	_ = NewHandler(nil)
}

func TestListBanks(t *testing.T) {
	directory, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	handler := NewHandler(directory)

	r := chi.NewRouter()
	r.Route("/api/v1/banks", func(sub chi.Router) {
		handler.RegisterRoutes(sub)
	})

	type bankListResponse struct {
		Banks []Bank `json:"banks"`
	}
	type envelope struct {
		Success bool             `json:"success"`
		Data    bankListResponse `json:"data"`
	}
	decode := func(t *testing.T, body io.Reader) bankListResponse {
		t.Helper()
		var env envelope
		if err := json.NewDecoder(body).Decode(&env); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if !env.Success {
			t.Fatalf("expected success=true envelope")
		}
		return env.Data
	}

	t.Run("returns all banks by default with cache header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/banks", nil)
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "public, max-age=86400" {
			t.Fatalf("unexpected Cache-Control header: %q", got)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
			t.Fatalf("unexpected Content-Type header: %q", got)
		}

		resp := decode(t, rec.Body)
		if len(resp.Banks) != len(directory.All()) {
			t.Fatalf("expected %d banks, got %d", len(directory.All()), len(resp.Banks))
		}
	})

	t.Run("returns only supported banks when supported=true", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/banks?supported=true", nil)
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}

		resp := decode(t, rec.Body)
		if len(resp.Banks) == 0 || len(resp.Banks) >= len(directory.All()) {
			t.Fatalf("unexpected filtered count: %d", len(resp.Banks))
		}
		for _, b := range resp.Banks {
			if !b.Supported {
				t.Fatalf("expected bank %s to be supported", b.Code)
			}
		}
	})

	t.Run("returns only unsupported banks when supported=false", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/banks?supported=false", nil)
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}

		resp := decode(t, rec.Body)
		for _, b := range resp.Banks {
			if b.Supported {
				t.Fatalf("expected bank %s to be unsupported", b.Code)
			}
		}
	})

	t.Run("ignores invalid supported query param and returns all banks", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/banks?supported=invalid", nil)
		rec := httptest.NewRecorder()

		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rec.Code)
		}

		resp := decode(t, rec.Body)
		if len(resp.Banks) != len(directory.All()) {
			t.Fatalf("expected %d banks, got %d", len(directory.All()), len(resp.Banks))
		}
	})
}
