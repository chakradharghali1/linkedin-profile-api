package linkedin

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/chakradharghali1/linkedin-profile-api/pkg/model"
)

// Profile and entity are aliases so this package can talk in short names
// while still returning the public model types.
type Profile = model.Profile

type entity = map[string]any

/*
normalizedResponse is Voyager's "normalized+json" envelope. Rather than
nesting objects, LinkedIn flattens every entity into "included" and refers
to them by URN from fields prefixed with "*".
*/
type normalizedResponse struct {
	Data     entity   `json:"data"`
	Included []entity `json:"included"`
}

// graph indexes the included entities so URN pointers can be resolved.
type graph struct {
	byURN map[string]entity
}

func newGraph(included []entity) *graph {
	index := make(map[string]entity, len(included))

	for _, item := range included {
		if urn := str(item, "entityUrn"); urn != "" {
			index[urn] = item
		}
	}

	return &graph{byURN: index}
}

// resolve follows a "*field" URN pointer to the entity it names.
func (g *graph) resolve(from entity, field string) entity {
	urn := str(from, "*"+field)

	if urn == "" {
		return nil
	}

	return g.byURN[urn]
}

/*
collection follows a "*field" pointer to a CollectionResponse and returns
its resolved elements.

LinkedIn returns these collections in two shapes: sometimes the elements are
inlined in "included", and sometimes the collection is an empty stub that has
to be fetched from its own endpoint. This returns nil for the stub case.
*/
func (g *graph) collection(from entity, field string) []entity {
	container := g.resolve(from, field)

	if container == nil {
		return nil
	}

	raw, ok := container["*elements"].([]any)
	if !ok {
		return nil
	}

	elements := make([]entity, 0, len(raw))

	for _, item := range raw {
		urn, ok := item.(string)
		if !ok {
			continue
		}

		if resolved, ok := g.byURN[urn]; ok {
			elements = append(elements, resolved)
		}
	}

	return elements
}

// findByType returns the first included entity of a given Voyager type.
func findByType(included []entity, suffix string) entity {
	for _, item := range included {
		if strings.HasSuffix(str(item, "$type"), suffix) {
			return item
		}
	}

	return nil
}

// filterByType returns every included entity of a given Voyager type.
func filterByType(included []entity, suffix string) []entity {
	var matches []entity

	for _, item := range included {
		if strings.HasSuffix(str(item, "$type"), suffix) {
			matches = append(matches, item)
		}
	}

	return matches
}

// buildProfile maps the entity graph onto the public response schema.
func buildProfile(envelope *normalizedResponse, publicID string) (*Profile, error) {
	g := newGraph(envelope.Included)

	root := findByType(envelope.Included, "identity.profile.Profile")

	if root == nil {
		return nil, errors.New("no profile entity in voyager response")
	}

	profile := &Profile{
		PublicIdentifier: firstNonEmpty(str(root, "publicIdentifier"), publicID),
		URN:              str(root, "entityUrn"),
		FirstName:        str(root, "firstName"),
		LastName:         str(root, "lastName"),
		Headline:         str(root, "headline"),
		About:            str(root, "summary"),
		IsPremium:        boolean(root, "premium"),
		IsInfluencer:     boolean(root, "influencer"),
		RetrievedAt:      time.Now().UTC(),
	}

	profile.ProfileURL = webBase + "/in/" + profile.PublicIdentifier + "/"

	profile.FullName = strings.TrimSpace(
		strings.Join([]string{profile.FirstName, profile.LastName}, " "),
	)

	profile.Location, profile.CountryCode = parseLocation(g, root)

	if industry := g.resolve(root, "industry"); industry != nil {
		profile.Industry = firstNonEmpty(
			str(industry, "name"),
			str(industry, "defaultLocalizedName"),
		)
	}

	profile.ProfilePicture = parseImage(nested(root, "profilePicture"))
	profile.BackgroundImage = parseImage(nested(root, "backgroundPicture"))

	profile.Experience = parseExperience(g, envelope.Included)
	profile.Education = parseEducation(g, envelope.Included)

	/*
		How much of these LinkedIn inlines depends on the viewer. For the
		authenticated member's own profile they arrive populated (though
		capped at roughly 20 entries); for an unconnected member they can be
		empty stubs holding only a pointer. Read whatever is here, and let
		attachSubResources fetch the complete list when asked.
	*/
	profile.Skills = parseSkills(g.collection(root, "profileSkills"))
	profile.Certifications = parseCertifications(g.collection(root, "profileCertifications"))
	profile.Languages = parseLanguages(g.collection(root, "profileLanguages"))
	profile.Honors = parseHonors(g.collection(root, "profileHonors"))
	profile.Projects = parseProjects(g.collection(root, "profileProjects"))
	profile.Volunteer = parseVolunteer(g, g.collection(root, "profileVolunteerExperiences"))

	return profile, nil
}

func parseLocation(g *graph, root entity) (string, string) {
	var location string

	if geoLocation := nested(root, "geoLocation"); geoLocation != nil {
		if geo := g.resolve(geoLocation, "geo"); geo != nil {
			location = firstNonEmpty(
				str(geo, "defaultLocalizedName"),
				str(geo, "defaultLocalizedNameWithoutCountryName"),
			)
		}
	}

	if location == "" {
		location = str(root, "locationName")
	}

	countryCode := ""

	if loc := nested(root, "location"); loc != nil {
		countryCode = str(loc, "countryCode")
	}

	return location, strings.ToUpper(countryCode)
}

/*
parseExperience reads the Position entities directly rather than walking
PositionGroups. Groups exist to render several roles at one employer under a
single company header, but every role is also a standalone Position, so
reading positions gives the same set with less traversal.
*/
func parseExperience(g *graph, included []entity) []model.Position {
	raw := filterByType(included, "identity.profile.Position")

	positions := make([]model.Position, 0, len(raw))

	for _, item := range raw {
		position := model.Position{
			Title:          str(item, "title"),
			CompanyName:    str(item, "companyName"),
			EmploymentType: str(item, "employmentType"),
			Location:       firstNonEmpty(str(item, "locationName"), str(item, "geoLocationName")),
			Description:    str(item, "description"),
		}

		if company := g.resolve(item, "company"); company != nil {
			position.CompanyURL = str(company, "url")

			if logo := parseImage(nested(company, "logo")); logo != nil {
				position.CompanyLogo = logo.URL
			}
		}

		position.StartDate, position.EndDate = parseDateRange(nested(item, "dateRange"))

		// LinkedIn marks an ongoing role by omitting the end date.
		position.IsCurrent = position.EndDate == nil

		positions = append(positions, position)
	}

	sortByStartDateDesc(positions)

	return positions
}

func parseEducation(g *graph, included []entity) []model.Education {
	raw := filterByType(included, "identity.profile.Education")

	educations := make([]model.Education, 0, len(raw))

	for _, item := range raw {
		education := model.Education{
			SchoolName:   str(item, "schoolName"),
			DegreeName:   str(item, "degreeName"),
			FieldOfStudy: str(item, "fieldOfStudy"),
			Grade:        str(item, "grade"),
			Activities:   str(item, "activities"),
			Description:  str(item, "description"),
		}

		if school := g.resolve(item, "school"); school != nil {
			education.SchoolURL = str(school, "url")

			if logo := parseImage(nested(school, "logo")); logo != nil {
				education.SchoolLogo = logo.URL
			}
		}

		education.StartDate, education.EndDate = parseDateRange(nested(item, "dateRange"))

		educations = append(educations, education)
	}

	return educations
}

func parseSkills(included []entity) []model.Skill {
	raw := filterByType(included, "identity.profile.Skill")

	skills := make([]model.Skill, 0, len(raw))

	for _, item := range raw {
		name := str(item, "name")

		if name == "" {
			continue
		}

		skills = append(skills, model.Skill{
			Name:             name,
			EndorsementCount: integer(item, "endorsementCount"),
		})
	}

	return skills
}

func parseCertifications(included []entity) []model.Certification {
	raw := filterByType(included, "identity.profile.Certification")

	certifications := make([]model.Certification, 0, len(raw))

	for _, item := range raw {
		certification := model.Certification{
			Name:      str(item, "name"),
			Authority: str(item, "authority"),
			LicenseNo: str(item, "licenseNumber"),
			URL:       str(item, "url"),
		}

		certification.IssuedDate, certification.ExpiresDate =
			parseDateRange(nested(item, "dateRange"))

		certifications = append(certifications, certification)
	}

	return certifications
}

func parseLanguages(included []entity) []model.Language {
	raw := filterByType(included, "identity.profile.Language")

	languages := make([]model.Language, 0, len(raw))

	for _, item := range raw {
		name := str(item, "name")

		if name == "" {
			continue
		}

		languages = append(languages, model.Language{
			Name:        name,
			Proficiency: humanizeEnum(str(item, "proficiency")),
		})
	}

	return languages
}

func parseHonors(included []entity) []model.Honor {
	raw := filterByType(included, "identity.profile.Honor")

	honors := make([]model.Honor, 0, len(raw))

	for _, item := range raw {
		honor := model.Honor{
			Title:       str(item, "title"),
			Issuer:      str(item, "issuer"),
			Description: str(item, "description"),
		}

		honor.IssuedDate = parseDate(nested(item, "issuedOn"))

		honors = append(honors, honor)
	}

	return honors
}

func parseProjects(included []entity) []model.Project {
	raw := filterByType(included, "identity.profile.Project")

	projects := make([]model.Project, 0, len(raw))

	for _, item := range raw {
		project := model.Project{
			Title:       str(item, "title"),
			Description: str(item, "description"),
			URL:         str(item, "url"),
		}

		project.StartDate, project.EndDate = parseDateRange(nested(item, "dateRange"))

		projects = append(projects, project)
	}

	return projects
}

func parseVolunteer(g *graph, included []entity) []model.Volunteer {
	raw := filterByType(included, "identity.profile.VolunteerExperience")

	volunteers := make([]model.Volunteer, 0, len(raw))

	for _, item := range raw {
		volunteer := model.Volunteer{
			Role:         str(item, "role"),
			Organization: str(item, "companyName"),
			Cause:        humanizeEnum(str(item, "cause")),
			Description:  str(item, "description"),
		}

		volunteer.StartDate, volunteer.EndDate = parseDateRange(nested(item, "dateRange"))

		volunteers = append(volunteers, volunteer)
	}

	return volunteers
}

/*
parseImage rebuilds a usable media URL from LinkedIn's vector image format,
which stores a root URL and a set of size-specific path segments that have to
be concatenated. It returns the largest available rendition.
*/
func parseImage(container entity) *model.Image {
	if container == nil {
		return nil
	}

	vector := nested(container, "vectorImage")

	// Profile and background pictures wrap the vector one level deeper.
	if vector == nil {
		if reference := nested(container, "displayImageReference"); reference != nil {
			vector = nested(reference, "vectorImage")
		}
	}

	if vector == nil {
		return nil
	}

	rootURL := str(vector, "rootUrl")

	artifacts, ok := vector["artifacts"].([]any)
	if !ok || rootURL == "" {
		return nil
	}

	var best *model.Image

	for _, raw := range artifacts {
		artifact, ok := raw.(entity)
		if !ok {
			continue
		}

		segment := str(artifact, "fileIdentifyingUrlPathSegment")

		if segment == "" {
			continue
		}

		width := integer(artifact, "width")

		if best != nil && width <= best.Width {
			continue
		}

		best = &model.Image{
			URL:    rootURL + segment,
			Width:  width,
			Height: integer(artifact, "height"),
		}
	}

	return best
}

func parseDateRange(container entity) (*model.Date, *model.Date) {
	if container == nil {
		return nil, nil
	}

	return parseDate(nested(container, "start")), parseDate(nested(container, "end"))
}

func parseDate(container entity) *model.Date {
	if container == nil {
		return nil
	}

	date := &model.Date{
		Year:  integer(container, "year"),
		Month: integer(container, "month"),
		Day:   integer(container, "day"),
	}

	if date.Year == 0 && date.Month == 0 && date.Day == 0 {
		return nil
	}

	return date
}

// sortByStartDateDesc puts the most recent roles first, matching the order
// LinkedIn renders them on the profile page.
func sortByStartDateDesc(positions []model.Position) {
	sort.SliceStable(positions, func(i, j int) bool {
		return startKey(positions[i]) > startKey(positions[j])
	})
}

func startKey(position model.Position) int {
	if position.StartDate == nil {
		return 0
	}

	return position.StartDate.Year*100 + position.StartDate.Month
}

// humanizeEnum turns Voyager's SCREAMING_SNAKE enums into readable text,
// for example NATIVE_OR_BILINGUAL into "Native or bilingual".
func humanizeEnum(value string) string {
	if value == "" {
		return ""
	}

	lower := strings.ToLower(strings.ReplaceAll(value, "_", " "))

	return strings.ToUpper(lower[:1]) + lower[1:]
}

func str(source entity, key string) string {
	if source == nil {
		return ""
	}

	value, _ := source[key].(string)

	return strings.TrimSpace(value)
}

func integer(source entity, key string) int {
	if source == nil {
		return 0
	}

	// encoding/json decodes every number into float64.
	value, _ := source[key].(float64)

	return int(value)
}

func boolean(source entity, key string) bool {
	if source == nil {
		return false
	}

	value, _ := source[key].(bool)

	return value
}

func nested(source entity, key string) entity {
	if source == nil {
		return nil
	}

	value, _ := source[key].(entity)

	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}

	return ""
}
