package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port               string
	LinkedInLiAt       string
	LinkedInJSessionID string

	/*
		How long a fetched profile stays cached.

		This is deliberately long. A LinkedIn session survives only a few
		automated requests, so a cached profile often outlives the session
		that fetched it — and serving it is the difference between a working
		response and a 429.
	*/
	CacheTTL time.Duration
}

func Load() (*Config, error) {
	port := strings.TrimSpace(
		os.Getenv("PORT"),
	)

	if port == "" {
		port = "8080"
	}

	if _, err := strconv.Atoi(port); err != nil {
		return nil, fmt.Errorf(
			"invalid PORT: %w",
			err,
		)
	}

	liAt := strings.TrimSpace(
		os.Getenv("LINKEDIN_LI_AT"),
	)

	if liAt == "" {
		return nil, fmt.Errorf(
			"LINKEDIN_LI_AT is required",
		)
	}

	jsessionID := strings.TrimSpace(
		os.Getenv("LINKEDIN_JSESSIONID"),
	)

	if jsessionID == "" {
		return nil, fmt.Errorf(
			"LINKEDIN_JSESSIONID is required",
		)
	}

	cacheTTL := 6 * time.Hour

	if raw := strings.TrimSpace(os.Getenv("CACHE_TTL")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid CACHE_TTL %q: %w", raw, err)
		}

		if parsed <= 0 {
			return nil, fmt.Errorf("CACHE_TTL must be positive, got %q", raw)
		}

		cacheTTL = parsed
	}

	return &Config{
		Port:               port,
		LinkedInLiAt:       liAt,
		LinkedInJSessionID: jsessionID,
		CacheTTL:           cacheTTL,
	}, nil
}
