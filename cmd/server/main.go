package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/chakradharghali1/linkedin-profile-api/internal/config"
	"github.com/chakradharghali1/linkedin-profile-api/internal/handler"
	"github.com/chakradharghali1/linkedin-profile-api/internal/linkedin"
)

func main() {
	// In production the values come from the platform's environment, so a
	// missing .env file is expected rather than an error.
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found; reading configuration from the environment")
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}

	client, err := linkedin.NewClient(cfg.LinkedInLiAt, cfg.LinkedInJSessionID)
	if err != nil {
		log.Fatalf("linkedin client error: %v", err)
	}

	// Cache lookups so repeated requests for the same profile — a demo, a
	// page refresh — never spend an upstream request.
	cache := linkedin.NewCache(15 * time.Minute)

	profileHandler := handler.NewProfileHandler(client, cache)

	mux := http.NewServeMux()

	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/api/v1/profile", profileHandler.GetProfile)
	mux.HandleFunc("/api/v1/profile/", profileHandler.GetProfile)
	mux.HandleFunc("/", rootHandler)

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           withRequestLogging(mux),
		ReadHeaderTimeout: 10 * time.Second,

		// Generous because a single profile fan-out can take a while when
		// LinkedIn makes us back off.
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("listening on :%s", cfg.Port)

		if err := server.ListenAndServe(); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {

			log.Fatalf("server error: %v", err)
		}
	}()

	// Wait for the platform to ask us to stop, then drain in-flight requests.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// rootHandler documents the API rather than returning a bare 404.
func rootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))

		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{
  "service": "linkedin-profile-api",
  "endpoints": {
    "GET /health": "liveness probe",
    "GET /api/v1/profile?url=<linkedin profile url>": "structured profile JSON"
  },
  "example": "/api/v1/profile?url=https://www.linkedin.com/in/williamhgates"
}`))
}

func withRequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(recorder, r)

		log.Printf("%s %s %d %s",
			r.Method, r.URL.RequestURI(), recorder.status, time.Since(start).Round(time.Millisecond))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
