// Package verifyclient implements the one-shot acceptance run used by the
// "verify" service: it waits for the API to become ready, posts one
// verification request, prints the adjudication and maps it to a process exit
// code, so a borderline branch ends with exactly one machine-readable result.
package verifyclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Exit codes returned by Run.
const (
	ExitPass        = 0 // verdict: pass
	ExitFail        = 1 // verdict: fail
	ExitReview      = 2 // verdict: review required
	ExitInvalid     = 3 // API rejected the payload (HTTP 422)
	ExitUpstream    = 4 // any other unexpected HTTP response
	ExitUnreachable = 5 // the API could not be reached or did not become ready
)

// Options controls a one-shot run.
type Options struct {
	BaseURL      string
	Payload      io.Reader
	Out          io.Writer
	HTTPClient   *http.Client
	ReadyTimeout time.Duration
	PollInterval time.Duration
}

// Run executes one verification against BaseURL and returns the process exit
// code. The pretty-printed adjudication (or error body) is written to Out.
func Run(ctx context.Context, opts Options) int {
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if opts.ReadyTimeout == 0 {
		opts.ReadyTimeout = 30 * time.Second
	}
	if opts.PollInterval == 0 {
		opts.PollInterval = 500 * time.Millisecond
	}
	if opts.Out == nil {
		opts.Out = io.Discard
	}

	body, err := io.ReadAll(opts.Payload)
	if err != nil {
		fmt.Fprintf(opts.Out, "cannot read request payload: %v\n", err)
		return ExitInvalid
	}

	if !waitReady(ctx, opts) {
		fmt.Fprintf(opts.Out, "verification API at %s did not become ready within %s\n", opts.BaseURL, opts.ReadyTimeout)
		return ExitUnreachable
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, opts.BaseURL+"/api/v1/verify", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(opts.Out, "cannot build request: %v\n", err)
		return ExitUnreachable
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		fmt.Fprintf(opts.Out, "verification request failed: %v\n", err)
		return ExitUnreachable
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(opts.Out, "cannot read API response: %v\n", err)
		return ExitUpstream
	}
	writeIndented(opts.Out, respBody)

	switch {
	case resp.StatusCode == http.StatusOK:
		var parsed struct {
			Verdict string `json:"verdict"`
		}
		if err := json.Unmarshal(respBody, &parsed); err != nil {
			fmt.Fprintf(opts.Out, "cannot parse adjudication: %v\n", err)
			return ExitUpstream
		}
		switch parsed.Verdict {
		case "pass":
			return ExitPass
		case "review":
			return ExitReview
		case "fail":
			return ExitFail
		default:
			return ExitUpstream
		}
	case resp.StatusCode == http.StatusUnprocessableEntity:
		return ExitInvalid
	default:
		return ExitUpstream
	}
}

func waitReady(ctx context.Context, opts Options) bool {
	deadline := time.Now().Add(opts.ReadyTimeout)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, opts.BaseURL+"/healthz", nil)
		if err != nil {
			return false
		}
		if resp, err := opts.HTTPClient.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(opts.PollInterval):
		}
	}
}

func writeIndented(w io.Writer, raw []byte) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		_, _ = w.Write(raw)
		return
	}
	_, _ = w.Write(buf.Bytes())
	_, _ = w.Write([]byte("\n"))
}
