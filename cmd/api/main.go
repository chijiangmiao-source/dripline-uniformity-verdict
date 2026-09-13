// Command api runs the drip-irrigation distribution-uniformity HTTP service.
// The listen port defaults to 8080 and is overridden by the API_PORT
// environment variable (used by Docker Compose for both services).
package main

import (
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/example/drip-du/internal/httpapi"
)

func main() {
	port := 8080
	if v := os.Getenv("API_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			log.Fatalf("invalid API_PORT %q: must be an integer between 1 and 65535", v)
		}
		port = p
	}

	router := httpapi.NewRouter()
	addr := ":" + strconv.Itoa(port)
	log.Printf("drip-du verification API listening on %s", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}
