package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/chakradharghali1/linkedin-profile-api/internal/linkedin"
)

type ProfileHandler struct {
	client *linkedin.Client
	cache  *linkedin.Cache
}

func NewProfileHandler(client *linkedin.Client, cache *linkedin.Cache) *ProfileHandler {
	return &ProfileHandler{client: client, cache: cache}
}

type errorResponse struct {
	Error   string `json:"error"`
	Details string `json:"details,omitempty"`
}

/*
GetProfile serves GET /api/v1/profile?url=<linkedin profile url>.

The identifier may also be supplied as a path segment
(/api/v1/profile/williamhgates) because that is convenient to curl.
*/
func (h *ProfileHandler) GetProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{
			Error: "method not allowed",
		})

		return
	}

	input := firstNonEmpty(
		r.URL.Query().Get("url"),
		r.URL.Query().Get("profile_url"),
		strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/profile"), "/"),
	)

	if input == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{
			Error:   "missing profile url",
			Details: "pass ?url=https://www.linkedin.com/in/<public-id>",
		})

		return
	}

	publicID, err := linkedin.ParseProfileURL(input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{
			Error:   "invalid profile url",
			Details: err.Error(),
		})

		return
	}

	/*
		Each optional section is one more upstream request, and LinkedIn
		tolerates very few per session, so they are opt-in and individually
		selectable rather than all-or-nothing.
	*/
	sections, err := parseSections(r.URL.Query())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{
			Error:   "invalid sections",
			Details: err.Error(),
		})

		return
	}

	cacheKey := publicID + "|" + sectionsKey(sections)

	// Serving from cache avoids touching LinkedIn at all.
	if cached, ok := h.cache.Get(cacheKey); ok {
		w.Header().Set("X-Cache", "HIT")

		writeJSON(w, http.StatusOK, cached)

		return
	}

	w.Header().Set("X-Cache", "MISS")

	// Bound the upstream work so a hung Voyager call cannot pin a connection.
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	profile, err := h.client.GetProfile(ctx, publicID, linkedin.Options{
		Sections: sections,
	})

	if err != nil {
		status, body := describeError(err)

		log.Printf("profile lookup failed for %q: %v", publicID, err)

		writeJSON(w, status, body)

		return
	}

	h.cache.Set(cacheKey, profile)

	writeJSON(w, http.StatusOK, profile)
}

/*
parseSections reads the optional sections to fetch.

	?full=true                     every section
	?sections=skills,languages     only those named

Unknown names are rejected rather than ignored, so a typo surfaces as a 400
instead of silently returning a profile without the section asked for.
*/
func parseSections(query url.Values) ([]linkedin.Section, error) {
	if isTruthy(query.Get("full")) {
		return linkedin.AllSections, nil
	}

	raw := strings.TrimSpace(query.Get("sections"))

	if raw == "" {
		return nil, nil
	}

	var sections []linkedin.Section

	for _, name := range strings.Split(raw, ",") {
		name = strings.ToLower(strings.TrimSpace(name))

		if name == "" {
			continue
		}

		valid := false

		for _, candidate := range linkedin.AllSections {
			if linkedin.Section(name) == candidate {
				valid = true
				break
			}
		}

		if !valid {
			return nil, fmt.Errorf(
				"unknown section %q; valid sections are skills, certifications, languages",
				name,
			)
		}

		sections = append(sections, linkedin.Section(name))
	}

	return sections, nil
}

// sectionsKey builds a stable cache key component, so the same request always
// maps to the same entry regardless of the order sections were listed in.
func sectionsKey(sections []linkedin.Section) string {
	names := make([]string, 0, len(sections))

	for _, section := range sections {
		names = append(names, string(section))
	}

	sort.Strings(names)

	return strings.Join(names, ",")
}

func isTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// describeError maps upstream failures onto meaningful HTTP statuses so a
// caller can tell a bad request apart from a throttle or an expired session.
func describeError(err error) (int, errorResponse) {
	switch {
	case errors.Is(err, linkedin.ErrNotFound):
		return http.StatusNotFound, errorResponse{
			Error:   "profile not found",
			Details: "LinkedIn has no profile for this identifier, or it is not visible to the authenticated session",
		}

	case errors.Is(err, linkedin.ErrThrottled):
		return http.StatusTooManyRequests, errorResponse{
			Error:   "rate limited by linkedin",
			Details: "LinkedIn is soft-blocking this session; retry after a short pause",
		}

	case errors.Is(err, linkedin.ErrUnauthorized):
		return http.StatusBadGateway, errorResponse{
			Error:   "linkedin session invalid",
			Details: "the configured li_at cookie is expired or was rejected",
		}

	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, errorResponse{
			Error: "timed out talking to linkedin",
		}

	default:
		return http.StatusBadGateway, errorResponse{
			Error:   "failed to fetch profile",
			Details: err.Error(),
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(value); err != nil {
		log.Printf("write response: %v", err)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}

	return ""
}
