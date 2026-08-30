# Architecture

How the service is put together, and why it is shaped this way.

For the *decisions* behind these choices — and the alternatives rejected —
see [decisions.md](decisions.md).

---

## The constraint that shaped everything

Most API clients are designed around latency or throughput. This one is
designed around a much tighter budget: **LinkedIn terminates a session after
roughly two or three automated requests.**

That is not a rate limit in the usual sense. There is no `429`, no
`Retry-After`, and no recovery window worth waiting for. LinkedIn responds
with a `302` redirect back to the URL just requested, applies it to every
authenticated call including ordinary page loads, and eventually invalidates
the session outright — signing the member out of their browser.

So the scarce resource is not CPU, memory or wall-clock time. It is
*upstream requests*. Nearly every design choice below follows from trying to
spend as few as possible:

| Choice | Requests saved |
|---|---|
| No bootstrap page load | 1 per process |
| Sections opt-in rather than always fetched | 3 per lookup |
| No retry on a soft block | up to 2 per failure |
| In-memory cache | all repeat lookups |

A default lookup costs exactly **one** request.

---

## Request flow

```
GET /api/v1/profile?url=…&sections=skills
        │
        ▼
┌──────────────────────────────────────────────┐
│ handler.ProfileHandler                       │
│  · parse & validate the profile URL          │
│  · parse requested sections (400 on typo)    │
│  · check cache  ── HIT ──▶ return, 0 requests│
└───────────────────────┬──────────────────────┘
                        │ MISS
                        ▼
┌──────────────────────────────────────────────┐
│ linkedin.Client                              │
│  · pace: enforce min gap between calls       │
│  · build Cookie header by hand               │
│  · set Voyager headers (csrf-token, x-li-*)  │
└───────────────────────┬──────────────────────┘
                        │  1 request
                        ▼
        GET /voyager/api/identity/dash/profiles
              ?q=memberIdentity&memberIdentity=…
              &decorationId=FullProfileWithEntities
                        │
                        ▼
┌──────────────────────────────────────────────┐
│ linkedin.buildProfile  (parse.go)            │
│  · index "included" by entityUrn             │
│  · resolve "*"-prefixed URN pointers         │
│  · map onto model.Profile                    │
└───────────────────────┬──────────────────────┘
                        │  optional, 1 request each
                        ▼
        /identity/dash/profileSkills?q=viewee…
        /identity/dash/profileCertifications…
        /identity/dash/profileLanguages…
                        │
                        ▼
                 cache.Set → JSON
```

---

## Packages

| Package | Responsibility |
|---|---|
| `cmd/server` | Wiring, routes, graceful shutdown, request logging |
| `internal/config` | Environment configuration and validation |
| `internal/handler` | HTTP concerns: parameters, status codes, JSON |
| `internal/linkedin` | Voyager transport, response parsing, cache |
| `pkg/model` | The public response schema |

The split that matters is **`handler` knows nothing about Voyager**, and
`linkedin` knows nothing about HTTP serving. The handler translates upstream
errors into status codes through a single `describeError` function; the
client returns sentinel errors (`ErrNotFound`, `ErrThrottled`,
`ErrUnauthorized`) and never touches a `http.ResponseWriter`.

`pkg/model` is separate from `internal/linkedin` so the response schema can
be imported by a consumer without dragging in the client.

---

## Authentication

Three values must agree, or LinkedIn rejects the call:

| Value | Sent as | Notes |
|---|---|---|
| `li_at` | Cookie | The session cookie |
| `JSESSIONID` | Cookie | **Including** the surrounding quotes |
| `csrf-token` | Header | `JSESSIONID` with the quotes **stripped** |

The Cookie header is assembled by hand rather than with `net/http`'s cookie
jar. This is not a stylistic preference — see
[D-003](decisions.md#d-003-build-the-cookie-header-by-hand) for the failure
it prevents.

---

## The response format

Voyager replies in `application/vnd.linkedin.normalized+json+2.1`. This is
not a nested document. It is a **flat entity graph**: every entity is
hoisted into a top-level `included` array, and references between them are
fields whose names begin with `*`, holding a URN.

```jsonc
{
  "data": { … },
  "included": [
    { "entityUrn": "urn:li:fsd_profile:ABC",
      "firstName": "Bill",
      "geoLocation": { "*geo": "urn:li:fsd_geo:104116203" } },

    { "entityUrn": "urn:li:fsd_geo:104116203",
      "defaultLocalizedName": "Seattle, Washington, United States" }
  ]
}
```

Reading `location` therefore means: find the profile entity → read
`geoLocation.*geo` → look that URN up in the index → read
`defaultLocalizedName`. The same pattern resolves company, school and
industry.

`parse.go` builds the index once (`newGraph`) and exposes `resolve` and
`collection` helpers so each field mapping stays a single line.

### Two shapes worth knowing

**Vector images.** Media is stored as a `rootUrl` plus a set of
size-specific path segments. Neither is a usable URL alone; they must be
concatenated. The parser picks the largest rendition.

```
rootUrl:  https://media.licdn.com/dms/image/v2/…/profile-displayphoto-shrink_
segment:  800_800/B56ZRi8g…
result:   https://media.licdn.com/dms/image/v2/…shrink_800_800/B56ZRi8g…
```

**Collection stubs.** `profileSkills`, `profileCertifications` and
`profileLanguages` appear on the profile entity as pointers to collections
that contain *no elements*. The pointer exists; the data does not. Each must
be fetched from its own endpoint, which is why they cost extra requests and
are opt-in.

This is also why `partial_sections` exists in the response: without it, an
empty `skills` array is ambiguous between "this member listed no skills" and
"we did not fetch them".

---

## Failure handling

The client returns sentinel errors, which the handler maps to status codes:

| Condition | Sentinel | HTTP |
|---|---|---|
| Unknown or invisible profile | `ErrNotFound` | `404` |
| Self-redirect, `429`, or `999` | `ErrThrottled` | `429` |
| `401` / `403` | `ErrUnauthorized` | `502` |
| Context deadline | — | `504` |

Sub-resource failures are deliberately **not** fatal. If skills cannot be
fetched, the section name is added to `partial_sections` and the rest of the
profile is still returned. Partial data with an honest manifest beats a
`500`.

---

## Parsing defensively

Every field read goes through a typed accessor (`str`, `integer`, `boolean`,
`nested`) that returns a zero value when the field is absent or the wrong
type. Nothing panics on a missing key, and no field is required.

This is a deliberate response to the schema being undocumented and
unstable — the retired `profileView` endpoint is proof that LinkedIn changes
these shapes without notice. A profile that loses one field to a schema
change still returns the other nine.

The trade-off is that a wrong field name fails *silently*, producing an empty
value rather than an error. `endorsement_count` is a live example: it is
unconfirmed, because the test profile had zero endorsements, so a correct
field name and a wrong one are indistinguishable from the data available.

---

## Testing

The parser is tested against a **real Voyager response** captured from the
live API, with tracking IDs, anti-abuse UUIDs and signed media tokens
stripped.

The tests target the things that actually break when LinkedIn shifts its
schema, rather than restating the mapping:

- URN pointer resolution (location via `Geo`, company via `Company.url`)
- Vector-image URL reconstruction, and picking the largest rendition
- Experience ordering, recent-first
- Empty collection stubs producing no entries
- URL parsing, including rejection of lookalike hosts such as
  `linkedin.com.evil.example`

What unit tests cannot cover is whether the *sub-resource* field names are
right, since those responses were never observed as fixtures. Skills were
confirmed against the live API; certifications and languages were not.
