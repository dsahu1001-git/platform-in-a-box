package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

type appInfo struct {
	Service     string `json:"service"`
	Version     string `json:"version"`
	Environment string `json:"environment"`
	Hostname    string `json:"hostname"`
	Time        string `json:"time"`
}

var requestCount uint64

func main() {
	port := env("PORT", "8080")
	service := env("SERVICE_NAME", "sample-platform-app")
	version := env("APP_VERSION", "dev")
	environment := env("APP_ENV", "training")
	hostname, _ := os.Hostname()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddUint64(&requestCount, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(appInfo{
			Service:     service,
			Version:     version,
			Environment: environment,
			Hostname:    hostname,
			Time:        time.Now().UTC().Format(time.RFC3339),
		})
	})

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddUint64(&requestCount, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("I'm running after app change 2\n"))
	})

	http.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddUint64(&requestCount, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready\n"))
	})

	http.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddUint64(&requestCount, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"service": service,
			"version": version,
		})
	})

	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddUint64(&requestCount, 1)
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintln(w, "# HELP sample_platform_app_requests_total Total HTTP requests handled by the sample app.")
		fmt.Fprintln(w, "# TYPE sample_platform_app_requests_total counter")
		fmt.Fprintf(w, "sample_platform_app_requests_total %d\n", atomic.LoadUint64(&requestCount))
	})

	log.Printf("%s version=%s environment=%s listening on :%s", service, version, environment, port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func env(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
