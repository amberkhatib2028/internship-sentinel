# Internship Sentinel

A Go service on AWS Lambda that polls seven job sources every few minutes and
emails newly posted **Summer 2027 software engineering and ML internships**,
filtering out quant/trading firms, defense employers, and non-software roles.

Built because internship postings get hundreds of applicants within hours, so
the useful signal is not "what is open" but "what opened in the last ten
minutes."

```
sources ──▶ fetch (7, independent) ──▶ filter ──▶ cross-source dedupe ──▶ SNS ──▶ inbox
```

**What is interesting here, if you are skimming:**

- Seven sources behind one `Source` interface, each failing independently
- Content-based cross-source dedupe, because the same role arrives from
  several places with unrelated native ids
- A filter chain that matches on two independent title signals rather than a
  phrase list, so it survives title permutations
- Handling for LinkedIn returning **HTTP 200 with a "no results" page** when it
  throttles, which is indistinguishable from an empty search and silently loses
  postings if you trust it
- One-command idempotent deploy: S3, IAM, Lambda, SNS, EventBridge

Credit: this started from a private, LinkedIn-only job-alert Lambda that
emailed senior product and solutions roles, used here with permission. The
LinkedIn scraping approach and the S3 dedupe idea came from that project. The
other six sources, the `Source` abstraction, the filter chain, the SNS delivery
path and the deploy tooling were written on top of it.

## Sources

Each source implements the `Source` interface in [sources.go](sources.go) and is
fetched independently — one source failing never sinks the run.

| Source | What it is | Typical yield |
| --- | --- | --- |
| `linkedin` | Guest job-search HTML, two geoId passes (remote + nationwide) | High, noisy |
| `github-list` | [vanshb03/Summer2027-Internships](https://github.com/vanshb03/Summer2027-Internships) `listings.json` | ~100 active Summer roles |
| `greenhouse` | Public board API, 31 companies | Moderate |
| `ashby` | Public GraphQL board, 13 companies | Low–moderate |
| `lever` | Public postings API, 2 companies | Low |
| `remoteok` | Public JSON feed | Very low |
| `weworkremotely` | Programming RSS feed | Very low |

`github-list` is the highest-signal source: already scoped to the 2027 cycle,
already deduped, served by GitHub rather than scraped.

Company ATS boards (Greenhouse/Ashby/Lever) are the companies' *own* public
APIs, so postings appear there before they reach LinkedIn. Quant and defense
employers are deliberately absent from those company lists — the filters would
drop them anyway, so polling them is wasted requests.

**Deliberately excluded:** Indeed, Glassdoor and ZipRecruiter (aggressive
anti-bot plus ToS that explicitly bans scraping), and Handshake (requires
university SSO; automating a logged-in session violates their terms).

### Adding a company

Append the slug to `greenhouseCompanies`, `ashbyCompanies`, or `leverCompanies`
in [source_boards.go](source_boards.go), then confirm with
`SOURCES=greenhouse go run . -dry-run`. **A stale or wrong slug fails silently** —
it simply returns nothing — so always verify the company count in the output.

## Cross-source deduplication

A Stripe internship reaches us through LinkedIn *and* Stripe's Greenhouse board,
carrying unrelated native ids. Identity is therefore content-based:

```
dedupeKey = normalize(company) + "|" + normalize(title)
```

Consequence: two genuinely distinct postings sharing a company and title
(commonly the same role listed per-location) collapse into one. That is the
right trade — you apply once either way.

## How filtering works

The real gate is `rejectionReason()` in [filters.go](filters.go), applied to
every posting from every source, in this order:

1. **Must be an internship** — matches `\bintern(ship)?s?\b`, `co-op`, or
   `summer analyst`; never "internal" / "international".
2. **Must be software or ML** — an engineering signal (`software`, `swe`,
   `developer`, `backend`, `machine learning`, `AI`, `data science`, `SRE`, …).
   Matching two independent signals rather than a fixed phrase list survives
   LinkedIn's title permutations: "Intern - Software Engineer", "2027 Software
   Engineering Summer Analyst", "Intern, Backend".
3. **Not another engineering discipline** — electrical/mechanical/civil/
   aerospace/hardware/systems etc., *unless* a strong software or ML signal
   overrides it. So "Imaging Software Engineer Intern" survives but "Electrical
   Engineering Intern" does not. `robotics` and `perception` are deliberately
   not overrides, so "Robotics Hardware Intern" is still caught as hardware.
4. **Right term** — rejects 2023–2026, Fall/Winter/Spring 2027, 2029+, and
   seasons named without a year ("Fall Intern"). Titles with no term stated are
   **kept** — most listings don't name one, so rejecting them drops nearly
   everything.
5. **Right level** — rejects senior/staff/principal/lead/manager/architect,
   PhD/MBA/masters/graduate, and cohort-restricted programs (veterans-only).
6. **Not quant** — title keywords plus a ~65-firm blocklist, including
   physical-commodities trading houses.
7. **Not defense** — title keywords (`clearance`, `TS/SCI`, `ITAR`, `DoD`) plus
   primes, services/IT contractors, FFRDCs and national labs, and defense-tech.
8. **Not a spam poster** — staffing agencies and aggregators that repost under
   their own name (Jobright, SpeedyApply, Dice, Jobot).

Validated against the 275-listing Summer 2027 corpus: of 61 active recent Summer
listings, 55 were correctly rejected (Jane Street, Citadel, Optiver, HRT, Akuna,
Jump, Palantir, Anduril, Northrop) and 6 kept.

### Known limitation

Filtering sees only title, company and location — not the job description.
Graduation-year eligibility ("must graduate Dec 2027–Jun 2028") and citizenship
requirements live in the description, so some ineligible listings still come
through. Fetching each description would mean one extra request per job and a
much higher chance of being rate-limited.

## Configuration

Recipient resolution: event `email` → `SENTINEL_TO_EMAIL` → `SENTINEL_TO_EMAIL`.

| Env var | Default | Purpose |
| --- | --- | --- |
| `SOURCES` | `all` | Comma-separated source names to run |
| `SNS_TOPIC_ARN` | set by deploy | SNS topic to publish to; when set, SNS is used instead of SES |
| `SENTINEL_TO_EMAIL` | none (required) | Recipient when the event has no `email` |
| `SES_FROM` | recipient | Verified SES sender (SES path only) |
| `S3_BUCKET` | `internship-sentinel-jobs` | Dedupe state bucket |
| `S3_KEY` | `sent_jobs_swe_intern_2027.json` | Dedupe state key |
| `LOOKBACK_SECONDS` | `1800` | LinkedIn search window only |

`S3_KEY` defaults to a file distinct from the upstream project's
`sent_jobs.json`, so running both against a single bucket cannot
cross-contaminate dedupe state.

## Scheduling

Only LinkedIn is time-windowed; every other source returns a full current
snapshot where the dedupe key set alone decides what is new. Polling ~50 board
endpoints every 10 minutes is wasteful, so use **two EventBridge rules against
the same Lambda**:

| Rule | Payload | Purpose |
| --- | --- | --- |
| `rate(10 minutes)` | `{"email":"...","SOURCES":"linkedin"}` via env | Catch LinkedIn's moving window |
| `rate(6 hours)` | `SOURCES=all` | Sweep boards and curated lists |

Set the Lambda timeout to **300 seconds** — a full sweep makes ~50 requests.

The first run against an empty key set emails the entire current backlog
(~35 listings) and logs that it is doing so. Subsequent runs send only new ones.

## Delivery: SNS, not SES

Alerts are published to an SNS topic, and SNS emails its subscribers.

SES was the original path and does not work here. SES can only send as a
verified identity and DKIM-signs as `amazonses.com`. When the `From` address is
at a domain publishing **DMARC `p=reject`** — many university domains do — the
signature does not align with the `From` domain, and the receiving server hard
-bounces every message:

```
550 5.7.509 Access denied, sending domain example.edu does not pass
DMARC verification and has a DMARC policy of reject
```

No SES setting fixes this; the `From` domain itself has to change. SNS sends
from AWS's own signed domain, so nothing is spoofed and DMARC passes. The cost
is formatting — **SNS delivers plain text only**, so `buildHTMLBody` is unused
on this path. URLs stay clickable in practically every client.

To go back to SES you would need a domain you control, verified in SES with
DKIM. Clear `SNS_TOPIC_ARN` and set `SES_FROM` to an address at that domain.

## Filing the emails into a folder

Every subject line begins with a fixed `[SWE-Intern]` prefix. In Gmail, search
`subject:[SWE-Intern]` → **Create filter** → *Skip the Inbox* + *Apply the
label*. In Outlook, a rule on *subject contains `[SWE-Intern]`*.

Keep the prefix stable — changing `subjectPrefix` breaks existing filters.

## What this does not do

It **does not apply to anything**. It finds listings and emails them; there is no
application logic here, and adding it would be a bad trade — automated
submissions breach LinkedIn's terms and are easy to detect, and a generic
auto-application is weaker than none for competitive SWE internships. Treat the
email as a queue to work through by hand.

## Local dry run

Runs every source, touches no AWS, prints each rejection with its reason:

```bash
go run . -dry-run
```

Scope it to one source while tuning:

```bash
SOURCES=greenhouse go run . -dry-run
```

## LinkedIn guest-endpoint reliability

Measured over repeated live requests:

| Query style | Success rate |
| --- | --- |
| `geoId=` (remote, nationwide) | ~100% |
| `location=` string (DC/DMV) | ~33% |

When throttled, LinkedIn returns **HTTP 200 with its ordinary "no results"
page** — indistinguishable from a genuinely empty search, so a naive scraper
silently reports zero jobs. Mitigations:

- **Retry** — `.no-results` is treated as a retryable error, not an empty
  result (`fetchAttempts`, randomized backoff).
- **Overlapping window** — `LOOKBACK_SECONDS` (30 min) is ~3x the 10-minute
  schedule, so each posting is scanned by three consecutive runs. Don't
  "simplify" by matching lookback to schedule rate — that silently drops
  listings, because `f_TPR` is a moving window.

A DC/DMV location-string pass was **removed** for this reason: it succeeded only
a third of the time, and the nationwide pass already covers those postings.
Substituting a geoId is not an option — the guest typeahead API returns nothing
and the published DC-area geoIds (`104994202`, `90000097`) return zero results.

## Build & deploy

```bash
GOOS=linux GOARCH=amd64 go build -tags lambda.norpc -o bootstrap . && zip sentinel.zip bootstrap
```

Upload `sentinel.zip` to the Lambda (`provided.al2023` runtime, handler
`bootstrap`). The execution role needs `s3:GetObject` + `s3:PutObject` on the
state key and `ses:SendEmail`. Sender and recipient must both be verified in SES
unless the account is out of the sandbox.

## Note on state growth

The dedupe key set is append-only with no expiry. Over a full recruiting season
it grows unbounded but stays small (tens of KB); clear the key between seasons.
