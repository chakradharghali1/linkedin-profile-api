package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port               string
	LinkedInLiAt       string
	LinkedInJSessionID string
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

	return &Config{
		Port:               port,
		LinkedInLiAt:       liAt,
		LinkedInJSessionID: jsessionID,
	}, nil
}
