package linkedin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	voyagerBase = "https://www.linkedin.com/voyager/api"
	webBase     = "https://www.linkedin.com"

	// The decoration id tells Voyager which projection of the profile to
	// return. FullProfileWithEntities inlines positions and educations.
	fullProfileDecoration = "com.linkedin.voyager.dash.deco.identity.profile.FullProfileWithEntities-102"

	defaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

	// Minimum spacing between upstream Voyager calls.
	minRequestGap = 2 * time.Second
)

// ErrNotFound is returned when LinkedIn has no profile for the identifier.
var ErrNotFound = errors.New("profile not found")

// ErrThrottled is returned when LinkedIn refuses to serve the request,
// which it signals with a redirect loop or an explicit rate-limit status.
var ErrThrottled = errors.New("linkedin throttled the request")

// ErrUnauthorized is returned when the session cookies are invalid or expired.
var ErrUnauthorized = errors.New("linkedin session is invalid or expired")

// Client is a reverse-engineered client for LinkedIn's internal Voyager API.
type Client struct {
	httpClient *http.Client
	liAt       string
	jsessionID string
	csrfToken  string
	userAgent  string

	// Serialises upstream calls so they are spaced out rather than bursted.
	paceMu      sync.Mutex
	lastRequest time.Time
}

// NewClient builds a Voyager client from a member's session cookies.
func NewClient(liAt string, jsessionID string) (*Client, error) {
	liAt = strings.TrimSpace(liAt)
	jsessionID = strings.TrimSpace(jsessionID)

	if liAt == "" {
		return nil, errors.New("LINKEDIN_LI_AT is required")
	}

	if jsessionID == "" {
		return nil, errors.New("LINKEDIN_JSESSIONID is required")
	}

	return &Client{
		httpClient: &http.Client{
			/*
				Deliberately no cookie jar. A jar keys cookies by
				(domain, path, name), so the host-only JSESSIONID that
				linkedin.com sets on a page load is a *different* entry from
				the authenticated one, and both get sent. LinkedIn then sees
				two JSESSIONID values, the csrf-token header matches neither,
				and every call is soft-blocked. Building the Cookie header by
				hand guarantees exactly one of each.
			*/
			Timeout: 30 * time.Second,

			/*
				Voyager answers a throttled request with a 302 back to the
				exact same URL, which the default policy would chase until it
				gives up. Stopping here lets us surface it as ErrThrottled.
			*/
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		liAt:       liAt,
		jsessionID: jsessionID,
		// The CSRF token is the JSESSIONID value without its quotes.
		csrfToken: strings.Trim(jsessionID, `"`),
		userAgent: defaultUserAgent,
	}, nil
}

/*
cookieHeader builds the Cookie header explicitly so the authenticated session
cookies appear exactly once each.

There is intentionally no "bootstrap" page load to collect bcookie/lidc.
Measured against the live API, requests carrying only these two cookies
succeed, so the extra page load bought nothing and simply consumed one of the
very small number of requests a session gets before being blocked.
*/
func (c *Client) cookieHeader() string {
	return "li_at=" + c.liAt + "; JSESSIONID=" + c.jsessionID
}

/*
get issues a Voyager request, pacing calls so we never burst.

There is deliberately no retry on throttling. Measured against the live API,
a soft block is not a transient per-request failure: it applies to the whole
session and persists for minutes. Retrying inside the block does not recover
it and appears to prolong it, so the honest response is to fail fast and let
the caller back off.
*/
func (c *Client) get(ctx context.Context, path string, referer string) ([]byte, error) {
	if err := c.pace(ctx); err != nil {
		return nil, err
	}

	return c.doOnce(ctx, path, referer)
}

// pace enforces a minimum gap between consecutive upstream requests. A full
// profile needs several calls, and issuing them back to back is what trips
// LinkedIn's limiter fastest.
func (c *Client) pace(ctx context.Context) error {
	c.paceMu.Lock()
	defer c.paceMu.Unlock()

	if !c.lastRequest.IsZero() {
		if wait := minRequestGap - time.Since(c.lastRequest); wait > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}
	}

	c.lastRequest = time.Now()

	return nil
}

func (c *Client) doOnce(ctx context.Context, path string, referer string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, voyagerBase+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create voyager request: %w", err)
	}

	c.setVoyagerHeaders(req, referer)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("voyager request failed: %w", err)
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read voyager response: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		return body, nil

	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return nil, ErrNotFound

	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, ErrUnauthorized

	/*
		A 302 here is not a real redirect. LinkedIn points the request back at
		the URL it already asked for as a soft block. 999 is LinkedIn's
		long-standing non-standard rate-limit status.
	*/
	case resp.StatusCode == http.StatusFound ||
		resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode == 999:
		return nil, ErrThrottled

	default:
		return nil, fmt.Errorf(
			"voyager returned status %d: %s",
			resp.StatusCode,
			truncate(strings.TrimSpace(string(body)), 200),
		)
	}
}

func (c *Client) setVoyagerHeaders(req *http.Request, referer string) {
	// Ask for the normalized envelope, which splits entities into "included".
	req.Header.Set("accept", "application/vnd.linkedin.normalized+json+2.1")
	req.Header.Set("csrf-token", c.csrfToken)
	req.Header.Set("x-restli-protocol-version", "2.0.0")
	req.Header.Set("x-li-lang", "en_US")

	// The web client always sends its build metadata; requests without it
	// look automated.
	req.Header.Set("x-li-track",
		`{"clientVersion":"1.13.35","mpVersion":"1.13.35","osName":"web",`+
			`"timezoneOffset":5.5,"timezone":"Asia/Calcutta",`+
			`"deviceFormFactor":"DESKTOP","mpName":"voyager-web",`+
			`"displayDensity":2,"displayWidth":2560,"displayHeight":1440}`)

	req.Header.Set("user-agent", c.userAgent)
	req.Header.Set("accept-language", "en-US,en;q=0.9")
	req.Header.Set("sec-ch-ua", `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"macOS"`)
	req.Header.Set("sec-fetch-dest", "empty")
	req.Header.Set("sec-fetch-mode", "cors")
	req.Header.Set("sec-fetch-site", "same-origin")
	req.Header.Set("cookie", c.cookieHeader())

	if referer != "" {
		req.Header.Set("referer", referer)
	}
}

// Section names a profile area that lives behind its own endpoint.
type Section string

const (
	SectionSkills         Section = "skills"
	SectionCertifications Section = "certifications"
	SectionLanguages      Section = "languages"
)

// AllSections is every optional section, in the order they are fetched.
var AllSections = []Section{SectionSkills, SectionCertifications, SectionLanguages}

/*
Options controls how much of the profile is fetched.

The base profile is a single request. Each additional section costs one more,
and LinkedIn blocks a session after very few, so sections are requested
individually rather than all-or-nothing. Asking for just the sections you
need keeps the request cost — and the blocking risk — proportional.
*/
type Options struct {
	Sections []Section
}

func (o Options) wants(section Section) bool {
	for _, candidate := range o.Sections {
		if candidate == section {
			return true
		}
	}

	return false
}

// GetProfile fetches and assembles a profile for a public identifier.
func (c *Client) GetProfile(ctx context.Context, publicID string, opts Options) (*Profile, error) {
	publicID = strings.TrimSpace(publicID)

	if publicID == "" {
		return nil, errors.New("public identifier is required")
	}

	referer := webBase + "/in/" + publicID + "/"

	path := fmt.Sprintf(
		"/identity/dash/profiles?q=memberIdentity&memberIdentity=%s&decorationId=%s",
		url.QueryEscape(publicID),
		url.QueryEscape(fullProfileDecoration),
	)

	body, err := c.get(ctx, path, referer)
	if err != nil {
		return nil, err
	}

	var envelope normalizedResponse

	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode voyager response: %w", err)
	}

	profile, err := buildProfile(&envelope, publicID)
	if err != nil {
		return nil, err
	}

	c.attachSubResources(ctx, profile, referer, opts)

	return profile, nil
}

/*
attachSubResources fills in the sections LinkedIn does not inline in the
profile projection. Each one is a best-effort extra call: a failure records
the section name in PartialSections rather than failing the whole request.
*/
func (c *Client) attachSubResources(
	ctx context.Context,
	profile *Profile,
	referer string,
	opts Options,
) {
	encodedURN := url.QueryEscape(profile.URN)

	type subResource struct {
		name  Section
		path  string
		apply func(entities []entity)
	}

	resources := []subResource{
		{
			name: SectionSkills,
			path: "/identity/dash/profileSkills?q=viewee&profileUrn=" + encodedURN + "&count=100",
			apply: func(entities []entity) {
				profile.Skills = parseSkills(entities)
			},
		},
		{
			name: SectionCertifications,
			path: "/identity/dash/profileCertifications?q=viewee&profileUrn=" + encodedURN + "&count=100",
			apply: func(entities []entity) {
				profile.Certifications = parseCertifications(entities)
			},
		},
		{
			name: SectionLanguages,
			path: "/identity/dash/profileLanguages?q=viewee&profileUrn=" + encodedURN + "&count=100",
			apply: func(entities []entity) {
				profile.Languages = parseLanguages(entities)
			},
		},
	}

	/*
		These run sequentially on purpose. Firing them in parallel is the
		fastest way to trip LinkedIn's rate limiter, which then blocks the
		session for everything.
	*/
	for _, resource := range resources {
		/*
			Anything not requested, or unreachable because the profile had no
			URN, is reported as not fetched. An empty skills list must never
			be mistaken for "this member has no skills".
		*/
		if !opts.wants(resource.name) || profile.URN == "" {
			profile.PartialSections = append(profile.PartialSections, string(resource.name))
			continue
		}

		body, err := c.get(ctx, resource.path, referer)
		if err != nil {
			profile.PartialSections = append(profile.PartialSections, string(resource.name))
			continue
		}

		var envelope normalizedResponse

		if err := json.Unmarshal(body, &envelope); err != nil {
			profile.PartialSections = append(profile.PartialSections, string(resource.name))
			continue
		}

		resource.apply(envelope.Included)
	}
}

func truncate(value string, limit int) string {
	runes := []rune(value)

	if len(runes) <= limit {
		return value
	}

	return string(runes[:limit]) + "..."
}
