# LinkedIn Profile API

A small HTTP service that accepts a LinkedIn profile URL and returns the
profile as structured JSON.

It talks to LinkedIn's internal **Voyager API** directly over HTTPS. There is
no browser, no headless Chrome, and no HTML scraping anywhere in the request
path.

```bash
curl "https://<your-deployment>/api/v1/profile?url=https://www.linkedin.com/in/williamhgates"
```

---

> **Deeper documentation:** [docs/architecture.md](docs/architecture.md) for
> how the pieces fit together, and [docs/decisions.md](docs/decisions.md) for
> the reasoning behind each design choice and the alternatives rejected.

## Contents

- [How it works](#how-it-works)
- [API](#api)
- [Response schema](#response-schema)
- [Setup](#setup)
- [Getting your LinkedIn cookies](#getting-your-linkedin-cookies)
- [Deployment](#deployment)
- [Tests](#tests)
- [Known limitations](#known-limitations)
- [Legal note](#legal-note)

---

## How it works

### Finding the endpoint

LinkedIn's web app is a single-page application. The HTML it serves is a
shell; the profile data is fetched afterwards by XHR from an internal JSON
API under `/voyager/api`, authenticated with the browser's session cookies.
Calling those endpoints directly is what this service does.

I probed the candidate endpoints before writing any client code:

| Endpoint | Result |
|---|---|
| `/voyager/api/identity/profiles/{id}/profileView` | **410 Gone** — the widely-cited endpoint is retired |
| `/voyager/api/me` | 200 — useful to validate a session |
| `/voyager/api/identity/dash/profiles?q=memberIdentity&memberIdentity={id}` | **200** — the one that works |

The `profileView` endpoint appears in most older write-ups on this topic. It
no longer exists, so the service is built on the `identity/dash/profiles`
resource with the `FullProfileWithEntities` decoration, which inlines
positions and educations into a single response.

### Authentication

Three things have to line up, or LinkedIn rejects the call:

1. `li_at` — the session cookie, sent as a cookie.
2. `JSESSIONID` — sent as a cookie, quotes included.
3. `csrf-token` — an HTTP header whose value is the `JSESSIONID` **with the
   quotes stripped**. If it disagrees with the cookie, the request fails.

Those two cookies are all that is needed. An earlier version also loaded the
homepage first to pick up the cookies a browser accumulates (`bcookie`,
`lidc`), but requests carrying only `li_at` and `JSESSIONID` succeed, so that
page load was removed — it bought nothing and consumed one of the very few
requests a session gets.

The Cookie header is built by hand rather than with `net/http`'s cookie jar.
A jar keys entries by `(domain, path, name)`, so the host-only `JSESSIONID`
that linkedin.com sets on a page load is a *different* entry from the
authenticated one and both get sent. LinkedIn then sees two `JSESSIONID`
values, the `csrf-token` header matches neither, and every call is soft
blocked — with no error message explaining why
([`client.go`](internal/linkedin/client.go)).

### Parsing the response

Voyager replies in `application/vnd.linkedin.normalized+json+2.1`, which is
not a nested document. It is a flat entity graph:

```jsonc
{
  "data": { ... },
  "included": [
    { "entityUrn": "urn:li:fsd_profile:ABC", "firstName": "Bill",
      "geoLocation": { "*geo": "urn:li:fsd_geo:104116203" } },
    { "entityUrn": "urn:li:fsd_geo:104116203",
      "defaultLocalizedName": "Seattle, Washington, United States" }
  ]
}
```

Every field prefixed with `*` is a pointer to another entity by URN. So the
parser indexes `included` by `entityUrn` and resolves pointers through that
index — location, company, school and industry all come from following these
references ([`parse.go`](internal/linkedin/parse.go)).

Two details worth calling out:

- **Images.** Media is stored as a "vector image": a `rootUrl` plus a set of
  size-specific path segments. A usable URL is the concatenation of the two.
  The parser picks the largest rendition available.
- **Partial sections.** Skills, certifications and languages come back as
  *empty collection stubs* — the pointers exist, the elements do not. Each
  needs its own request (`/identity/dash/profileSkills?q=viewee&...`). They
  are therefore opt-in behind `?full=true`, and fetched sequentially when
  requested, because firing them in parallel is a reliable way to trip the
  rate limiter. Whether skipped or failed, the section name is reported in
  `partial_sections` instead of failing the whole response, so a caller can
  always distinguish "this member listed no skills" from "we did not read
  them".

### Handling the rate limiter

When LinkedIn decides it does not like the traffic, it does not return an
error. It returns **`302` redirecting to the exact URL you just asked for**.
Followed naively, that is an infinite redirect loop — which is what a default
HTTP client does until it gives up.

The client disables redirect-following and treats a self-redirect (along with
`429` and LinkedIn's non-standard `999`) as throttling, surfacing a clean
`429` to the caller.

It deliberately does **not** retry. A soft block is not a transient
per-request failure: it applies to the entire session and persists for
minutes, so retrying cannot recover the call and appears to prolong the
block. Instead the client paces itself, enforcing a minimum gap between
upstream calls, because issuing the several requests a full profile needs
back-to-back is the fastest way to trip the limiter. See
[Known limitations](#known-limitations) for the measurements behind this.

---

## API

### `GET /api/v1/profile`

| Parameter | Required | Default | Description |
|---|---|---|---|
| `url` | yes | — | A LinkedIn profile URL, or a bare public identifier |
| `sections` | no | none | Comma-separated extras: `skills`, `certifications`, `languages` |
| `full` | no | `false` | Shorthand for all three sections |

**Sections are opt-in because upstream requests are the scarce resource.** The
base lookup is a single Voyager call. Skills, certifications and languages are
*not* included in it — LinkedIn returns empty stubs pointing at separate
endpoints — so each one costs an extra request, and `full=true` turns one
request into four. LinkedIn blocks a session after only a handful.

Request just what you need:

```bash
# 1 request  — name, headline, location, about, experience, education, images
curl ".../api/v1/profile?url=<url>"

# 2 requests — the above plus skills
curl ".../api/v1/profile?url=<url>&sections=skills"

# 4 requests — everything (most likely to be blocked)
curl ".../api/v1/profile?url=<url>&full=true"
```

Any section not fetched is listed in `partial_sections`, so an empty `skills`
array is never ambiguous between "none listed" and "not requested". An
unknown section name is a `400` rather than a silent omission.

Successful responses are cached in memory for 15 minutes and report
`X-Cache: HIT` or `MISS`. A repeated lookup costs no upstream request at all.

Accepted input shapes:

```
https://www.linkedin.com/in/williamhgates/
https://in.linkedin.com/in/someone?originalSubdomain=in
linkedin.com/in/williamhgates
williamhgates
```

A path form is also supported for convenience:
`GET /api/v1/profile/williamhgates`

### Other endpoints

| Endpoint | Purpose |
|---|---|
| `GET /health` | Liveness probe |
| `GET /` | Self-describing index |

### Status codes

| Code | Meaning |
|---|---|
| `200` | Profile returned |
| `400` | Missing or malformed profile URL |
| `404` | No such profile, or not visible to the authenticated session |
| `429` | LinkedIn soft-blocked the request: throttled **or** the cookie expired |
| `502` | Upstream failure |
| `504` | Upstream timed out |

---

## Response schema

Empty sections are omitted rather than returned as `null`.

```json
{
  "profile_url": "https://www.linkedin.com/in/williamhgates/",
  "public_identifier": "williamhgates",
  "urn": "urn:li:fsd_profile:ACoAAA8BYqEB...",
  "first_name": "Bill",
  "last_name": "Gates",
  "full_name": "Bill Gates",
  "headline": "Chair, Gates Foundation and Founder, Breakthrough Energy",
  "about": "...",
  "location": "Seattle, Washington, United States",
  "country_code": "US",
  "industry": "Non-profit Organizations",
  "profile_picture": {
    "url": "https://media.licdn.com/dms/image/.../800_800/...",
    "width": 800,
    "height": 800
  },
  "background_image": { "url": "...", "width": 1584, "height": 396 },
  "is_premium": true,
  "is_influencer": true,
  "experience": [
    {
      "title": "Co-chair",
      "company_name": "Gates Foundation",
      "company_url": "https://www.linkedin.com/company/gates-foundation/",
      "company_logo": "https://media.licdn.com/...",
      "location": "Seattle, Washington",
      "description": "...",
      "start_date": { "year": 2000 },
      "is_current": true
    }
  ],
  "education": [
    {
      "school_name": "Harvard University",
      "degree_name": "...",
      "field_of_study": "...",
      "start_date": { "year": 1973 },
      "end_date": { "year": 1975 }
    }
  ],
  "skills": [{ "name": "Public Speaking", "endorsement_count": 42 }],
  "certifications": [{ "name": "...", "authority": "..." }],
  "languages": [{ "name": "English", "proficiency": "Native or bilingual" }],
  "partial_sections": [],
  "retrieved_at": "2025-08-30T12:00:00Z"
}
```

`partial_sections` lists any section that could not be retrieved on this
request. It is omitted when everything was fetched successfully.

---

## Setup

Requires Go 1.24+.

```bash
git clone https://github.com/chakradharghali1/linkedin-profile-api.git
cd linkedin-profile-api

cp .env.example .env
# fill in LINKEDIN_LI_AT and LINKEDIN_JSESSIONID — see the next section

go mod download
go run ./cmd/server
```

```bash
curl "http://localhost:8080/api/v1/profile?url=https://www.linkedin.com/in/williamhgates"
```

With Docker:

```bash
docker build -t linkedin-profile-api .
docker run -p 8080:8080 --env-file .env linkedin-profile-api
```

### Configuration

| Variable | Required | Default | Description |
|---|---|---|---|
| `LINKEDIN_LI_AT` | yes | — | `li_at` session cookie |
| `LINKEDIN_JSESSIONID` | yes | — | `JSESSIONID` cookie, **including quotes** |
| `PORT` | no | `8080` | Listen port |

---

## Getting your LinkedIn cookies

1. Log in to LinkedIn in Chrome.
2. Open DevTools → **Application** → **Cookies** → `https://www.linkedin.com`.
3. Copy the value of `li_at` into `LINKEDIN_LI_AT`.
4. Copy the value of `JSESSIONID` into `LINKEDIN_JSESSIONID`, **keeping the
   surrounding quotes** — it looks like `"ajax:1234567890123456789"`.

These are live session credentials. They belong in `.env` (which is
git-ignored) or in your host's secret store — never in the repository.

Logging out of LinkedIn in that browser invalidates the cookie.

---

## Deployment

The repo includes a [`render.yaml`](render.yaml) blueprint. On Render:
**New → Blueprint**, point it at the repo, and set `LINKEDIN_LI_AT` and
`LINKEDIN_JSESSIONID` in the dashboard when prompted. They are marked
`sync: false`, so they are never committed. Render terminates TLS, so the
service is served over HTTPS automatically.

Any Docker host works the same way. The image is a static binary on Alpine,
running as an unprivileged user.

> **Read [Known limitations](#known-limitations) before deploying.** LinkedIn
> blocks datacenter IP ranges aggressively, and this affects cloud
> deployments more than local runs.

---

## Tests

```bash
go test ./...
```

The parser is tested against a **real Voyager response** captured from the
live API and checked into
[`internal/linkedin/testdata`](internal/linkedin/testdata) (tracking IDs,
anti-abuse metadata and signed media tokens stripped). The tests assert the
things that actually break when LinkedIn shifts its schema: URN pointer
resolution for location and company, vector-image URL reconstruction,
recent-first ordering of experience, and that empty collection stubs do not
silently produce empty sections.

URL parsing is tested separately, including rejection of lookalike hosts such
as `linkedin.com.evil.example`.

### Verified against the live API

Beyond the unit tests, the following were confirmed end-to-end against
LinkedIn with a real session:

| Field | Verified |
|---|---|
| name, headline, location, country, industry, about | ✅ live |
| experience | ✅ live — roles, company URLs, dates, `is_current`, ordering |
| education | ✅ live — school, degree, field of study |
| profile picture | ✅ live |
| background image | ✅ real captured response (fixture) |
| skills | ✅ live — all 24 returned via `?sections=skills` |
| certifications, languages | ⚠️ parsers written but not exercised live |

One caveat on skills: the `name` field is confirmed, but every skill on the
test profile had zero endorsements, so `endorsement_count` returned nothing.
That is consistent with either a correct field name and no endorsements, or a
wrong field name — the two are indistinguishable from this data, so treat
`endorsement_count` as unconfirmed.

---

## Known limitations

**This is the honest list, including the one that matters most.**

1. **Anti-bot detection is the real constraint, and it acts on the session.**
   This is the finding that shapes everything else, so it is worth stating
   precisely.

   Voyager answers normally at first. After a small number of requests it
   begins returning a `302` redirect to the *exact URL just requested* — for
   every authenticated call, including ordinary page loads like `/feed/`. It
   is a soft block, not an error, and a naive client follows it into an
   infinite redirect loop.

   What the block is tied to:

   - **Not the IP.** A freshly issued `li_at` starts working immediately from
     the same machine that was blocked seconds earlier.
   - **The session.** The new cookie then survives only a handful of
     non-browser requests before it is blocked in turn.
   - **Escalating to invalidation.** Continued automated access does not just
     block the session — LinkedIn *terminates* it, which signs the member out
     in their browser too and requires a fresh login. Observed repeatedly:
     one session was ended after four requests, another after two.

   The practical budget is roughly **2–3 automated requests per session**,
   which is what drove the design below: one request per lookup by default,
   individually selectable sections, no retries, and a cache in front.

   A fresh session working instantly while the previous one stays blocked
   rules out IP-based rate limiting and points at request fingerprinting:
   Go's TLS/HTTP2 signature does not look like Chrome's, regardless of how
   faithfully the headers are reproduced.

   The service detects the condition and returns `429`, but it cannot make
   LinkedIn answer. Closing this properly means presenting a browser TLS
   fingerprint (via uTLS) rather than sending more or better headers.

2. **Datacenter IPs are blocked much harder than residential ones.** A
   deployment on a cloud provider is substantially more likely to be
   throttled than the same code run from a home connection. Serious use would
   need residential or mobile proxy egress, which is out of scope here. This
   is the main caveat on the hosted deployment.

3. **Cookies expire, and automated use shortens their life sharply.** `li_at`
   is a real session cookie. Left alone it lasts weeks, but as described
   above, automated access can cause LinkedIn to terminate it within minutes.
   There is no refresh mechanism; the service returns `502` with a clear
   message when the session is rejected, and the cookie must be replaced by
   logging in again.

   Practically, this means the account used for the backend should be one you
   are willing to have signed out and potentially flagged. Repeated automated
   access can subject an account to verification challenges or temporary
   restriction.

4. **Results depend on who is authenticated.** Voyager returns what *that
   member* is allowed to see. Network distance, the target's privacy
   settings, and whether they block the viewer all change the response. Two
   different sessions can legitimately return different data for the same
   profile.

5. **Sections cost extra requests.** Skills, certifications and languages
   are separate calls, so `?full=true` turns one request into four and is
   correspondingly more likely to be blocked. They are opt-in, fetched
   sequentially, and degrade to `partial_sections` rather than failing the
   response.

   Given how few requests a session gets, the design goal was to make the
   default lookup cost exactly one upstream call. That is why there is no
   bootstrap page load, no retry, and an in-memory cache in front.

6. **The schema is undocumented and unstable.** Decoration IDs and entity
   shapes are internal to LinkedIn and change without notice — `profileView`
   returning `410` is exactly that happening. Parsing is defensive throughout
   and omits fields it cannot find rather than failing, but a large enough
   change upstream will require updating the parser.

7. **The cache is in-memory only.** It is per-process and lost on restart, so
   it does not help across replicas or deploys. Redis would fix that, at the
   cost of an external dependency.

8. **Contact details are not returned.** Email and phone sit behind a
   separate endpoint and are usually only visible for first-degree
   connections. I left them out deliberately.

### What I would do next

Present a Chrome TLS/HTTP2 fingerprint via
[uTLS](https://github.com/refraction-networking/utls), which is the actual
fix for the blocking described above and still requires no browser; move the
cache to Redis so it survives restarts and is shared across replicas; rotate
egress IPs; and add API-key auth, which the service currently has no notion
of.

---

## Legal note

This uses LinkedIn's private, undocumented API with a member's own session
cookies, which is contrary to LinkedIn's Terms of Service. It was built as a
hiring exercise, and it is intended for use with your own account and your
own data. Deploying it publicly means serving personal data scraped from
LinkedIn, which carries obligations under GDPR and similar regimes. Do not
use it for bulk collection.
