# Decision records

The choices that shaped this service, why they were made, and what was
rejected. Several were driven by measurements against the live API rather
than by preference — those are marked with the evidence.

For how the pieces fit together, see [architecture.md](architecture.md).

| | Decision | Driver |
|---|---|---|
| [D-001](#d-001-build-on-identitydashprofiles-not-profileview) | Build on `identity/dash/profiles` | `profileView` returns `410` |
| [D-002](#d-002-no-browser-anywhere-in-the-request-path) | No browser anywhere | Requirement |
| [D-003](#d-003-build-the-cookie-header-by-hand) | Cookie header by hand | Silent auth failure |
| [D-004](#d-004-drop-the-bootstrap-page-load) | Drop the bootstrap page load | Measured as unnecessary |
| [D-005](#d-005-do-not-retry-a-soft-block) | No retry on a soft block | Retrying makes it worse |
| [D-006](#d-006-make-extra-sections-opt-in) | Sections opt-in | 1 request vs 4 |
| [D-007](#d-007-cache-in-process) | In-process cache | Cheapest request is none |
| [D-008](#d-008-report-what-was-not-fetched) | Report unfetched sections | Avoid silent ambiguity |
| [D-009](#d-009-parse-defensively-never-require-a-field) | Parse defensively | Undocumented schema |
| [D-010](#d-010-keep-a-real-captured-response-as-the-test-fixture) | Real response as fixture | Synthetic proves less |

---

## D-001: Build on `identity/dash/profiles`, not `profileView`

**Decision.** Use
`/voyager/api/identity/dash/profiles?q=memberIdentity&memberIdentity={id}`
with the `FullProfileWithEntities` decoration.

**Why.** The endpoint most online write-ups recommend,
`/voyager/api/identity/profiles/{id}/profileView`, is retired. Probed
directly before any code was written:

| Endpoint | Result |
|---|---|
| `identity/profiles/{id}/profileView` | **410 Gone** |
| `me` | 200 |
| `identity/dash/profiles?q=memberIdentity…` | **200**, ~41 KB |

**Consequence.** The `dash` projection inlines positions and educations but
returns skills, certifications and languages as empty stubs — which is the
root cause of [D-006](#d-006-make-extra-sections-opt-in).

**Note.** Verifying the endpoints first cost three requests and saved
building on something that cannot work. Given how scarce requests turned out
to be, reconnaissance was the highest-value use of them.

---

## D-002: No browser anywhere in the request path

**Decision.** Pure HTTP against LinkedIn's JSON endpoints. No headless
Chrome, no automation driver, no HTML parsing.

**Why.** Required by the brief. It is also faster and far lighter: the
service is a static Go binary with a single dependency (`godotenv`) and no
system libraries.

**Rejected.** An earlier iteration of this repository scraped the rendered
HTML with `goquery`, matching on hardcoded strings from one specific
profile. That approach cannot generalise — LinkedIn renders profiles client
side, so the HTML shell contains almost none of the data — and it was
removed entirely.

**Verification.** `go.mod` lists exactly one dependency; no browser,
automation or HTML-parsing library appears in the module graph.

---

## D-003: Build the Cookie header by hand

**Decision.** Construct `Cookie:` manually. Do not use
`net/http/cookiejar`.

**Why.** A cookie jar keys entries by `(domain, path, name)` per RFC 6265.
A **host-only** `JSESSIONID` set by `www.linkedin.com` and a **domain**
cookie for `.linkedin.com` are therefore two distinct entries, and the jar
sends *both*.

LinkedIn then receives two `JSESSIONID` values. The `csrf-token` header can
only match one, so authentication fails — and it fails **silently**, as a
`302` redirect indistinguishable from rate limiting. There is no error
message pointing at the cause.

**Evidence.** This was observed directly: the same credentials succeeded via
`curl` and failed through the client, and the jar was the only difference.

**Consequence.** Cookie handling is explicit and boring:
`li_at=…; JSESSIONID=…`, exactly once each.

---

## D-004: Drop the bootstrap page load

**Decision.** Do not fetch the LinkedIn homepage to collect `bcookie` /
`lidc` before calling Voyager.

**Why.** It was assumed necessary — a browser accumulates those cookies
before any XHR, so reproducing that seemed prudent. Measurement showed
otherwise: requests carrying only `li_at` and `JSESSIONID` succeed, and the
very first successful probe sent nothing else.

Against a budget of two or three requests per session, an unnecessary
request is expensive.

**Consequence.** One less request per process, and no risk of the homepage's
anonymous `JSESSIONID` colliding with the authenticated one.

---

## D-005: Do not retry a soft block

**Decision.** On `ErrThrottled`, fail immediately with `429`. No backoff, no
retry.

**Why.** Retrying assumes a transient, per-request failure. That assumption
is wrong here. A soft block applies to the **whole session**, persists for
minutes, and is not cleared by waiting a few seconds. Retrying spends more
of an already-tiny budget and appears to prolong the block.

**Evidence.** The original client retried up to three times with exponential
backoff. Combined with the bootstrap load, a single lookup fired four
requests in about seven seconds — enough to burn a freshly issued cookie
almost immediately. Removing retries and the bootstrap took the same lookup
from 7.4 s to 0.44 s, and it then succeeded.

**Instead.** A minimum gap is enforced between consecutive upstream calls, so
multi-section lookups are paced rather than bursted.

---

## D-006: Make extra sections opt-in

**Decision.** The base lookup is one request. Skills, certifications and
languages are requested individually via `?sections=…`, or all at once with
`?full=true`.

**Why.** Those three are not included in the profile response — they come
back as empty collection stubs and each needs its own call. Fetching all of
them always would make every lookup cost four requests, which exceeds what a
session tolerates.

Selecting individually means the cost is proportional to what is actually
needed. Verifying skills against the live API cost two requests rather than
four, and stayed within budget.

**Rejected.** A single `?full=true` boolean. All-or-nothing forced callers
who wanted one section to pay for three.

**Also.** An unknown section name returns `400` rather than being ignored, so
a typo like `sections=skils` surfaces immediately instead of silently
returning a profile without the requested data.

---

## D-007: Cache in process

**Decision.** In-memory cache keyed by public identifier plus the requested
sections, 15-minute TTL.

**Why.** The cheapest request is the one never sent. A demo, a page refresh
or a retry costs nothing, and results keep being served after the session
has been blocked — which materially improves the odds that a deployed
instance responds successfully.

**Rejected.** Redis. It would survive restarts and be shared across
replicas, but it adds an external dependency and a second thing to deploy,
for a service that fits in one container.

**Known limitation.** Per-process and lost on restart.

---

## D-008: Report what was not fetched

**Decision.** Every section that was skipped or failed is listed in
`partial_sections`.

**Why.** Without it, `"skills": []` is ambiguous: it could mean the member
listed no skills, that the section was not requested, or that the request
failed. Those are very different facts and a caller cannot distinguish them.

This matters more than usual here, because skipping sections is the
*default* behaviour rather than an error path.

---

## D-009: Parse defensively, never require a field

**Decision.** Every field read goes through an accessor returning a zero
value when absent or of the wrong type. No field is mandatory; no missing
key panics.

**Why.** The schema is undocumented, internal, and demonstrably unstable —
`profileView` returning `410` is that instability in action. A profile that
loses one field to an upstream change should still return the rest.

**Trade-off, stated plainly.** Defensive parsing converts errors into empty
values, so a *wrong field name* fails silently rather than loudly.
`endorsement_count` is a live instance: it is unconfirmed, because every
skill on the test profile had zero endorsements, making a correct field name
and an incorrect one indistinguishable. This is documented rather than
papered over.

---

## D-010: Keep a real captured response as the test fixture

**Decision.** Test the parser against a genuine Voyager response, sanitised
of tracking IDs, anti-abuse UUIDs and signed media tokens.

**Why.** The parser's job is to handle *LinkedIn's* format. A synthetic
fixture written from the same understanding that produced the parser would
test that understanding against itself, and would agree with any
misconception baked into both.

The real response caught details that would not have been invented:
`geoLocation` nesting a `*geo` pointer rather than a name, background images
nesting differently from avatars, and collection stubs that carry a pointer
but no elements.

**Trade-off.** It is scraped public-figure data in a public repository. It
was sanitised, and the alternative — a fixture that agrees with its own
author — was judged to be worth less.
