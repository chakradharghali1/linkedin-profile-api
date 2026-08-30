package linkedin

import "testing"

func TestParseProfileURL(t *testing.T) {
	valid := map[string]string{
		"https://www.linkedin.com/in/williamhgates":               "williamhgates",
		"https://www.linkedin.com/in/williamhgates/":              "williamhgates",
		"http://www.linkedin.com/in/williamhgates":                "williamhgates",
		"https://linkedin.com/in/williamhgates":                   "williamhgates",
		"www.linkedin.com/in/williamhgates":                       "williamhgates",
		"linkedin.com/in/williamhgates":                           "williamhgates",
		"https://in.linkedin.com/in/someone?originalSubdomain=in": "someone",
		"https://www.linkedin.com/in/williamhgates/details/":      "williamhgates",
		"https://www.linkedin.com/pub/someone":                    "someone",
		"  https://www.linkedin.com/in/williamhgates/  ":          "williamhgates",
		"williamhgates":    "williamhgates",
		"chakradhar-ghali": "chakradhar-ghali",
		// Non-ASCII identifiers arrive percent-encoded.
		"https://www.linkedin.com/in/andr%C3%A9-silva": "andré-silva",
	}

	for input, want := range valid {
		got, err := ParseProfileURL(input)
		if err != nil {
			t.Errorf("ParseProfileURL(%q) returned error: %v", input, err)
			continue
		}

		if got != want {
			t.Errorf("ParseProfileURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseProfileURLRejectsBadInput(t *testing.T) {
	invalid := []string{
		"",
		"   ",
		"https://www.linkedin.com/company/microsoft",
		"https://www.linkedin.com/feed/",
		"https://example.com/in/williamhgates",
		// A lookalike domain must not be accepted just because it ends in
		// the expected string.
		"https://notlinkedin.com/in/williamhgates",
		"https://linkedin.com.evil.example/in/williamhgates",
		"https://www.linkedin.com/in/",
	}

	for _, input := range invalid {
		if got, err := ParseProfileURL(input); err == nil {
			t.Errorf("ParseProfileURL(%q) = %q, want an error", input, got)
		}
	}
}
