package verifyclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const passPayload = `{"measurements":[{"id":"a","flow_lph":10},{"id":"b","flow_lph":10},{"id":"c","flow_lph":10},{"id":"d","flow_lph":10}]}`

func newServer(t *testing.T, status int, verdict string, ready bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if ready {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	mux.HandleFunc("/api/v1/verify", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"verdict":"` + verdict + `"}`))
	})
	return httptest.NewServer(mux)
}

func TestRunExitCodes(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		verdict string
		ready   bool
		want    int
	}{
		{"pass", http.StatusOK, "pass", true, ExitPass},
		{"review", http.StatusOK, "review", true, ExitReview},
		{"fail", http.StatusOK, "fail", true, ExitFail},
		{"invalid", http.StatusUnprocessableEntity, "", true, ExitInvalid},
		{"upstream error", http.StatusInternalServerError, "", true, ExitUpstream},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, tc.status, tc.verdict, tc.ready)
			defer srv.Close()

			var out strings.Builder
			code := Run(context.Background(), Options{
				BaseURL:      srv.URL,
				Payload:      strings.NewReader(passPayload),
				Out:          &out,
				ReadyTimeout: 2 * time.Second,
			})
			assert.Equal(t, tc.want, code)
			assert.Contains(t, out.String(), `"verdict"`)
		})
	}
}

func TestRunWaitsForReadiness(t *testing.T) {
	var ready atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if ready.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	mux.HandleFunc("/api/v1/verify", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"verdict":"pass"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	go func() {
		time.Sleep(300 * time.Millisecond)
		ready.Store(true)
	}()

	var out strings.Builder
	code := Run(context.Background(), Options{
		BaseURL:      srv.URL,
		Payload:      strings.NewReader(passPayload),
		Out:          &out,
		ReadyTimeout: 3 * time.Second,
		PollInterval: 50 * time.Millisecond,
	})
	assert.Equal(t, ExitPass, code)
}

func TestRunUnreachableServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // closed before the run: readiness must time out

	var out strings.Builder
	code := Run(context.Background(), Options{
		BaseURL:      srv.URL,
		Payload:      strings.NewReader(passPayload),
		Out:          &out,
		ReadyTimeout: 300 * time.Millisecond,
		PollInterval: 50 * time.Millisecond,
	})
	require.Equal(t, ExitUnreachable, code)
	assert.Contains(t, out.String(), "did not become ready")
}
