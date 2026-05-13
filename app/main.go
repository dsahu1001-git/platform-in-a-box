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
	ReleaseRing string `json:"releaseRing"`
	ReportName  string `json:"reportName"`
	Hostname    string `json:"hostname"`
	Time        string `json:"time"`
}

var requestCount uint64

func main() {
	port := env("PORT", "8080")
	service := env("SERVICE_NAME", "sample-platform-app")
	version := env("APP_VERSION", "dev")
	environment := env("APP_ENV", "training")
	releaseRing := env("RELEASE_RING", "dev")
	reportName := env("REPORT_NAME", "training-demo")
	hostname, _ := os.Hostname()

	handle := func(path string, next http.HandlerFunc) {
		http.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			atomic.AddUint64(&requestCount, 1)
			started := time.Now()
			recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next(recorder, r)
			log.Printf(
				"method=%s path=%s status=%d duration_ms=%d service=%s version=%s report=%s ring=%s",
				r.Method,
				r.URL.Path,
				recorder.status,
				time.Since(started).Milliseconds(),
				service,
				version,
				reportName,
				releaseRing,
			)
		})
	}

	handle("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(appInfo{
			Service:     service,
			Version:     version,
			Environment: environment,
			ReleaseRing: releaseRing,
			ReportName:  reportName,
			Hostname:    hostname,
			Time:        time.Now().UTC().Format(time.RFC3339),
		})
	})

	handle("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("I'm running after app change 2\n"))
	})

	handle("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready after\n"))
	})

	handle("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"service":     service,
			"version":     version,
			"reportName":  reportName,
			"releaseRing": releaseRing,
			"environment": environment,
		})
	})

	handle("/metrics", func(w http.ResponseWriter, r *http.Request) {
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

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (recorder *statusRecorder) WriteHeader(status int) {
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}
