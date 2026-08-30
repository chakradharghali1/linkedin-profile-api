package main

import (
	"context"
	// Blank import: the embed package is only needed for the //go:embed
	// directive below, not referenced directly.
	_ "embed"
	"encoding/json"
	"errors"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
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
	cache := linkedin.NewCache(cfg.CacheTTL)

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

/*
apiIndex is what GET / returns.

It is deliberately more than a route list. Someone trying this API will
usually open it in a browser before reading the README, and the behaviour
here is surprising enough to need explaining on the spot: a 429 is normal and
expected, not a broken service.
*/
type apiIndex struct {
	Service     string            `json:"service"`
	Description string            `json:"description"`
	Source      string            `json:"source"`
	Endpoints   []endpointDoc     `json:"endpoints"`
	Example     string            `json:"example"`
	StatusCodes map[string]string `json:"status_codes"`
	Notes       []string          `json:"notes"`
}

type endpointDoc struct {
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Description string            `json:"description"`
	Params      map[string]string `json:"params,omitempty"`
}

//go:embed index.html
var indexHTML string

// Parsed once at startup so a malformed template fails loudly on boot rather
// than on the first request.
var indexTemplate = template.Must(template.New("index").Parse(indexHTML))

// prefersHTML reports whether the caller is a browser rather than an API
// client. Browsers ask for text/html in Accept; curl sends only a wildcard
// and Go clients usually send nothing, so JSON stays the default for
// everything programmatic.
func prefersHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// rootHandler documents the API rather than returning a bare 404. It serves
// HTML to browsers and JSON to everything else.
func rootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))

		return
	}

	index := apiIndex{
		Service: "linkedin-profile-api",
		Description: "Returns a LinkedIn profile as structured JSON by calling " +
			"LinkedIn's internal Voyager API directly. No browser, no HTML scraping.",
		Source: "https://github.com/chakradharghali1/linkedin-profile-api",
		Endpoints: []endpointDoc{
			{
				Method:      "GET",
				Path:        "/api/v1/profile",
				Description: "Structured profile JSON",
				Params: map[string]string{
					"url":      "required. A LinkedIn profile URL, or a bare public identifier",
					"sections": "optional. Comma-separated: skills, certifications, languages. Each costs one extra upstream request",
					"full":     "optional. true = all optional sections (4 upstream requests total)",
				},
			},
			{Method: "GET", Path: "/health", Description: "Liveness probe. Does not contact LinkedIn"},
		},
		Example: "/api/v1/profile?url=https://www.linkedin.com/in/williamhgates",
		StatusCodes: map[string]string{
			"200": "Profile returned. Check the X-Cache header for HIT or MISS",
			"400": "Missing or malformed profile URL, or an unknown section name",
			"404": "No such profile, or not visible to the authenticated session",
			"429": "LinkedIn soft-blocked the request: throttled, or the session cookie expired",
			"502": "Upstream failure",
			"504": "Upstream timed out",
		},
		Notes: []string{
			"A 429 is expected behaviour, not an outage. LinkedIn terminates a session " +
				"after roughly two or three automated requests, so the backing cookie is " +
				"short-lived by design of their anti-bot system, not by choice.",
			"Successful responses are cached, so a profile fetched while the session was " +
				"alive keeps being served afterwards. X-Cache reports HIT or MISS.",
			"The first request after a period of inactivity can take around 50 seconds. " +
				"The service runs on a free tier that suspends idle instances; this is a " +
				"cold start, not a hang.",
			"partial_sections in a response lists sections that were not fetched from " +
				"their own endpoint. They may still contain data, capped at about 20 " +
				"entries, so a short list is never mistaken for a complete one.",
			"See the repository README and docs/decisions.md for the measurements behind " +
				"these behaviours.",
		},
	}

	if prefersHTML(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)

		if err := indexTemplate.Execute(w, index); err != nil {
			log.Printf("render index: %v", err)
		}

		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(index); err != nil {
		log.Printf("write index: %v", err)
	}
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
