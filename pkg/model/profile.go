package model

import "time"

/*
Profile is the structured representation of a LinkedIn profile
page returned by this API.

Every section is omitempty: LinkedIn only returns the sections a
member has actually filled in, and visibility also depends on the
authenticated viewer's network distance to that member.
*/
type Profile struct {
	ProfileURL       string `json:"profile_url"`
	PublicIdentifier string `json:"public_identifier"`
	URN              string `json:"urn,omitempty"`

	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	FullName  string `json:"full_name,omitempty"`
	Headline  string `json:"headline,omitempty"`
	About     string `json:"about,omitempty"`

	Location    string `json:"location,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
	Industry    string `json:"industry,omitempty"`

	ProfilePicture  *Image `json:"profile_picture,omitempty"`
	BackgroundImage *Image `json:"background_image,omitempty"`

	IsPremium    bool `json:"is_premium"`
	IsInfluencer bool `json:"is_influencer"`

	Experience     []Position      `json:"experience,omitempty"`
	Education      []Education     `json:"education,omitempty"`
	Skills         []Skill         `json:"skills,omitempty"`
	Certifications []Certification `json:"certifications,omitempty"`
	Languages      []Language      `json:"languages,omitempty"`
	Honors         []Honor         `json:"honors,omitempty"`
	Projects       []Project       `json:"projects,omitempty"`
	Volunteer      []Volunteer     `json:"volunteer,omitempty"`

	/*
		Sections that may be incomplete, because they were not fetched from
		their own endpoint.

		Such a section can still hold data: LinkedIn inlines part of these
		collections in the profile response, but caps them at roughly 20
		entries. A member with 24 skills therefore yields 20 here. Fetch the
		section explicitly (?sections=skills) for the complete list.

		The point is that an empty or short list is never ambiguous between
		"the member listed nothing", "the list is truncated" and "the fetch
		failed".
	*/
	PartialSections []string `json:"partial_sections,omitempty"`

	RetrievedAt time.Time `json:"retrieved_at"`
}

// Image is a single resolved LinkedIn media URL.
type Image struct {
	URL    string `json:"url"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// Date is a partial date; LinkedIn frequently omits month and day.
type Date struct {
	Year  int `json:"year,omitempty"`
	Month int `json:"month,omitempty"`
	Day   int `json:"day,omitempty"`
}

// Position is one role in the experience section.
type Position struct {
	Title          string `json:"title,omitempty"`
	CompanyName    string `json:"company_name,omitempty"`
	CompanyURL     string `json:"company_url,omitempty"`
	CompanyLogo    string `json:"company_logo,omitempty"`
	EmploymentType string `json:"employment_type,omitempty"`
	Location       string `json:"location,omitempty"`
	Description    string `json:"description,omitempty"`
	StartDate      *Date  `json:"start_date,omitempty"`
	EndDate        *Date  `json:"end_date,omitempty"`
	IsCurrent      bool   `json:"is_current"`
}

// Education is one entry in the education section.
type Education struct {
	SchoolName   string `json:"school_name,omitempty"`
	SchoolURL    string `json:"school_url,omitempty"`
	SchoolLogo   string `json:"school_logo,omitempty"`
	DegreeName   string `json:"degree_name,omitempty"`
	FieldOfStudy string `json:"field_of_study,omitempty"`
	Grade        string `json:"grade,omitempty"`
	Activities   string `json:"activities,omitempty"`
	Description  string `json:"description,omitempty"`
	StartDate    *Date  `json:"start_date,omitempty"`
	EndDate      *Date  `json:"end_date,omitempty"`
}

// Skill is one endorsed skill.
type Skill struct {
	Name             string `json:"name"`
	EndorsementCount int    `json:"endorsement_count,omitempty"`
}

// Certification is one licence or certification.
type Certification struct {
	Name        string `json:"name,omitempty"`
	Authority   string `json:"authority,omitempty"`
	LicenseNo   string `json:"license_number,omitempty"`
	URL         string `json:"url,omitempty"`
	IssuedDate  *Date  `json:"issued_date,omitempty"`
	ExpiresDate *Date  `json:"expires_date,omitempty"`
}

// Language is one language and the member's stated proficiency.
type Language struct {
	Name        string `json:"name"`
	Proficiency string `json:"proficiency,omitempty"`
}

// Honor is one award or honour.
type Honor struct {
	Title       string `json:"title,omitempty"`
	Issuer      string `json:"issuer,omitempty"`
	Description string `json:"description,omitempty"`
	IssuedDate  *Date  `json:"issued_date,omitempty"`
}

// Project is one entry in the projects section.
type Project struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
	StartDate   *Date  `json:"start_date,omitempty"`
	EndDate     *Date  `json:"end_date,omitempty"`
}

// Volunteer is one entry in the volunteering section.
type Volunteer struct {
	Role         string `json:"role,omitempty"`
	Organization string `json:"organization,omitempty"`
	Cause        string `json:"cause,omitempty"`
	Description  string `json:"description,omitempty"`
	StartDate    *Date  `json:"start_date,omitempty"`
	EndDate      *Date  `json:"end_date,omitempty"`
}
