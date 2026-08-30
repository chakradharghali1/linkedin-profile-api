package linkedin

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// loadFixture reads a captured Voyager response recorded from the live API.
func loadFixture(t *testing.T, name string) *normalizedResponse {
	t.Helper()

	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var envelope normalizedResponse

	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	return &envelope
}

func TestBuildProfileFromRealResponse(t *testing.T) {
	envelope := loadFixture(t, "profile_real.json")

	profile, err := buildProfile(envelope, "williamhgates")
	if err != nil {
		t.Fatalf("buildProfile: %v", err)
	}

	checks := []struct {
		field string
		got   string
		want  string
	}{
		{"public_identifier", profile.PublicIdentifier, "williamhgates"},
		{"first_name", profile.FirstName, "Bill"},
		{"last_name", profile.LastName, "Gates"},
		{"full_name", profile.FullName, "Bill Gates"},
		{"headline", profile.Headline, "Chair, Gates Foundation and Founder, Breakthrough Energy"},
		{"location", profile.Location, "Seattle, Washington, United States"},
		{"country_code", profile.CountryCode, "US"},
		{"profile_url", profile.ProfileURL, "https://www.linkedin.com/in/williamhgates/"},
	}

	for _, check := range checks {
		if check.got != check.want {
			t.Errorf("%s = %q, want %q", check.field, check.got, check.want)
		}
	}

	if profile.About == "" {
		t.Error("about is empty, expected the summary text")
	}

	if !profile.IsInfluencer {
		t.Error("is_influencer = false, want true")
	}
}

func TestProfilePictureResolvesToLargestRendition(t *testing.T) {
	envelope := loadFixture(t, "profile_real.json")

	profile, err := buildProfile(envelope, "williamhgates")
	if err != nil {
		t.Fatalf("buildProfile: %v", err)
	}

	if profile.ProfilePicture == nil {
		t.Fatal("profile_picture is nil")
	}

	// The URL is only usable if the root and the size-specific path segment
	// were concatenated, which is the part that is easy to get wrong.
	if !strings.HasPrefix(profile.ProfilePicture.URL, "https://media.licdn.com/") {
		t.Errorf("profile picture URL = %q, want a media.licdn.com URL", profile.ProfilePicture.URL)
	}

	if !strings.Contains(profile.ProfilePicture.URL, "profile-displayphoto") {
		t.Errorf("profile picture URL = %q, missing the display photo segment", profile.ProfilePicture.URL)
	}

	// The fixture carries 100/200/400/800 renditions; we must pick 800.
	if profile.ProfilePicture.Width != 800 {
		t.Errorf("profile picture width = %d, want the largest rendition (800)",
			profile.ProfilePicture.Width)
	}
}

/*
The background banner is a separate vector image from the avatar and nests
differently, so it needs its own coverage. Not every member has one, which
makes it easy to leave accidentally untested.
*/
func TestBackgroundImageIsParsed(t *testing.T) {
	envelope := loadFixture(t, "profile_real.json")

	profile, err := buildProfile(envelope, "williamhgates")
	if err != nil {
		t.Fatalf("buildProfile: %v", err)
	}

	if profile.BackgroundImage == nil {
		t.Fatal("background_image is nil, but the fixture has a backgroundPicture")
	}

	if profile.BackgroundImage.Width != 1400 {
		t.Errorf("background image width = %d, want the largest rendition (1400)",
			profile.BackgroundImage.Width)
	}
}

func TestExperienceIsParsedAndOrderedRecentFirst(t *testing.T) {
	envelope := loadFixture(t, "profile_real.json")

	profile, err := buildProfile(envelope, "williamhgates")
	if err != nil {
		t.Fatalf("buildProfile: %v", err)
	}

	if len(profile.Experience) == 0 {
		t.Fatal("experience is empty")
	}

	first := profile.Experience[0]

	if first.Title == "" || first.CompanyName == "" {
		t.Errorf("first position missing title/company: %+v", first)
	}

	// Company details come from a separate Company entity that has to be
	// resolved through its URN pointer.
	if first.CompanyURL == "" {
		t.Errorf("first position has no company_url; URN resolution likely failed: %+v", first)
	}

	for i := 1; i < len(profile.Experience); i++ {
		previous := startKey(profile.Experience[i-1])
		current := startKey(profile.Experience[i])

		if previous < current {
			t.Errorf("experience not sorted recent-first at index %d: %d before %d",
				i, previous, current)
		}
	}
}

func TestEducationIsParsed(t *testing.T) {
	envelope := loadFixture(t, "profile_real.json")

	profile, err := buildProfile(envelope, "williamhgates")
	if err != nil {
		t.Fatalf("buildProfile: %v", err)
	}

	if len(profile.Education) == 0 {
		t.Fatal("education is empty")
	}

	for _, education := range profile.Education {
		if education.SchoolName == "" {
			t.Errorf("education entry missing school_name: %+v", education)
		}
	}
}

// The skills/certifications/languages collections arrive as empty stubs that
// must be fetched separately, so parsing must not invent entries for them.
func TestStubCollectionsYieldNoEntries(t *testing.T) {
	envelope := loadFixture(t, "profile_real.json")

	profile, err := buildProfile(envelope, "williamhgates")
	if err != nil {
		t.Fatalf("buildProfile: %v", err)
	}

	if len(profile.Skills) != 0 {
		t.Errorf("skills = %v, want none from a stub collection", profile.Skills)
	}
}

func TestBuildProfileRejectsResponseWithoutProfile(t *testing.T) {
	envelope := &normalizedResponse{Included: []entity{
		{"$type": "com.linkedin.voyager.dash.organization.Company", "entityUrn": "urn:li:fsd_company:1"},
	}}

	if _, err := buildProfile(envelope, "nobody"); err == nil {
		t.Fatal("expected an error when the response has no profile entity")
	}
}

func TestHumanizeEnum(t *testing.T) {
	cases := map[string]string{
		"NATIVE_OR_BILINGUAL":  "Native or bilingual",
		"PROFESSIONAL_WORKING": "Professional working",
		"ELEMENTARY":           "Elementary",
		"":                     "",
	}

	for input, want := range cases {
		if got := humanizeEnum(input); got != want {
			t.Errorf("humanizeEnum(%q) = %q, want %q", input, got, want)
		}
	}
}
