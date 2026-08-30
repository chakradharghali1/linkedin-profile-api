package linkedin

import (
	"errors"
	"net/url"
	"strings"
)

// ErrInvalidProfileURL is returned when the input is not a LinkedIn profile
// URL or a bare public identifier.
var ErrInvalidProfileURL = errors.New(
	"input must be a LinkedIn profile URL such as https://www.linkedin.com/in/williamhgates",
)

/*
ParseProfileURL extracts the public identifier from user input.

It accepts the shapes people actually paste:

	https://www.linkedin.com/in/williamhgates/
	https://in.linkedin.com/in/williamhgates?originalSubdomain=in
	linkedin.com/in/williamhgates
	williamhgates
*/
func ParseProfileURL(input string) (string, error) {
	input = strings.TrimSpace(input)

	if input == "" {
		return "", ErrInvalidProfileURL
	}

	// A bare identifier has no host and no path separators.
	if !strings.Contains(input, "/") && !strings.Contains(input, ".") {
		return sanitizeIdentifier(input)
	}

	candidate := input

	// url.Parse only recognises a host when a scheme is present.
	if !strings.Contains(candidate, "://") {
		candidate = "https://" + candidate
	}

	parsed, err := url.Parse(candidate)
	if err != nil {
		return "", ErrInvalidProfileURL
	}

	host := strings.ToLower(parsed.Hostname())

	// Allow country subdomains such as in.linkedin.com, but nothing that
	// merely ends in a lookalike domain.
	if host != "linkedin.com" && !strings.HasSuffix(host, ".linkedin.com") {
		return "", ErrInvalidProfileURL
	}

	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")

	for index, segment := range segments {
		if segment != "in" && segment != "pub" {
			continue
		}

		if index+1 >= len(segments) {
			break
		}

		return sanitizeIdentifier(segments[index+1])
	}

	return "", ErrInvalidProfileURL
}

func sanitizeIdentifier(value string) (string, error) {
	// Identifiers with non-ASCII characters arrive percent-encoded.
	decoded, err := url.PathUnescape(value)
	if err != nil {
		decoded = value
	}

	decoded = strings.TrimSpace(decoded)

	if decoded == "" {
		return "", ErrInvalidProfileURL
	}

	return decoded, nil
}
