// Command verify is the one-shot acceptance service: it posts exactly one
// verification request to the API, prints the adjudication and exits with a
// code that mirrors the verdict (0 pass, 1 fail, 2 review, 3 invalid payload,
// 4 unexpected upstream error, 5 API unreachable).
//
// Configuration via environment:
//
//	API_BASE_URL    base URL of the API (default http://api:8080)
//	VERIFY_PAYLOAD  path to the JSON request file (default /data/payload.json)
//
// If the payload file is absent, the request is read from standard input.
package main

import (
	"context"
	"log"
	"os"

	"github.com/example/drip-du/internal/verifyclient"
)

func main() {
	baseURL := os.Getenv("API_BASE_URL")
	if baseURL == "" {
		baseURL = "http://api:8080"
	}

	payloadPath := os.Getenv("VERIFY_PAYLOAD")
	if payloadPath == "" {
		payloadPath = "/data/payload.json"
	}

	var payload *os.File
	if f, err := os.Open(payloadPath); err == nil {
		payload = f
		defer f.Close()
	} else {
		// Fall back to standard input so the service can be driven by pipes.
		payload = os.Stdin
	}

	code := verifyclient.Run(context.Background(), verifyclient.Options{
		BaseURL: baseURL,
		Payload: payload,
		Out:     os.Stdout,
	})
	if code != verifyclient.ExitPass && code != verifyclient.ExitReview && code != verifyclient.ExitFail {
		log.Printf("verify service exit code %d", code)
	}
	os.Exit(code)
}
