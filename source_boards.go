package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Company ATS boards. These are the companies' own public job-board APIs, so
// postings show up here before they are syndicated to LinkedIn, and there is no
// scraping involved.
//
// Every slug below was verified to return a non-empty board. A slug that goes
// stale fails silently — it just returns nothing — so re-check with the
// -dry-run output if a company stops appearing. Quant and defense employers are
// intentionally absent: the filters would drop them anyway, so polling them is
// wasted requests.

var greenhouseCompanies = []string{
	"stripe", "databricks", "figma", "airbnb", "instacart", "coinbase",
	"dropbox", "cloudflare", "twilio", "asana", "gitlab", "mongodb",
	"datadog", "elastic", "samsara", "affirm", "brex", "discord",
	"reddit", "pinterest", "lyft", "duolingo", "flexport", "sofi",
	"zocdoc", "robinhood", "verkada", "attentive", "gusto", "mercury",
	"airtable",
}

var ashbyCompanies = []string{
	"ramp", "linear", "openai", "replit", "sierra", "harvey",
	"browserbase", "vanta", "notion", "elevenlabs", "suno", "cohere",
	"langchain",
}

var leverCompanies = []string{
	"spotify", "zoox",
}

// ---------------------------------------------------------------------------
// Greenhouse
// ---------------------------------------------------------------------------

type GreenhouseSource struct{}

func (GreenhouseSource) Name() string { return "greenhouse" }

type greenhouseResponse struct {
	Jobs []struct {
		ID       int64  `json:"id"`
		Title    string `json:"title"`
		AbsURL   string `json:"absolute_url"`
		Updated  string `json:"updated_at"`
		Location struct {
			Name string `json:"name"`
		} `json:"location"`
	} `json:"jobs"`
}

func (s GreenhouseSource) Fetch(ctx context.Context) ([]Job, error) {
	jobs := fetchEach(greenhouseCompanies, func(company string) ([]Job, error) {
		url := fmt.Sprintf("https://boards-api.greenhouse.io/v1/boards/%s/jobs", company)
		body, err := httpGetBody(ctx, url)
		if err != nil {
			return nil, fmt.Errorf("greenhouse %s: %w", company, err)
		}

		var resp greenhouseResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("greenhouse %s: %w", company, err)
		}

		var out []Job
		for _, j := range resp.Jobs {
			if !looksLikeIntern(j.Title) {
				continue
			}
			out = append(out, Job{
				Id:       fmt.Sprintf("greenhouse:%s:%d", company, j.ID),
				Title:    j.Title,
				Company:  displayCompany(company),
				Location: j.Location.Name,
				URL:      j.AbsURL,
				PostedAt: shortDate(j.Updated),
				Source:   "greenhouse",
			})
		}
		return out, nil
	})

	fmt.Printf("  %d intern-titled postings across %d companies\n", len(jobs), len(greenhouseCompanies))
	return jobs, nil
}

// ---------------------------------------------------------------------------
// Ashby
// ---------------------------------------------------------------------------

type AshbySource struct{}

func (AshbySource) Name() string { return "ashby" }

// Ashby's public board type exposes only these fields — it has no posting URL
// and no publish date, and introspection is disabled, so the field list was
// established by probing. Asking for anything else fails validation and nulls
// out the whole jobBoard, which looks exactly like a stale slug.
const ashbyQuery = `query ApiJobBoardWithTeams($organizationHostedJobsPageName: String!) {
  jobBoard: jobBoardWithTeams(organizationHostedJobsPageName: $organizationHostedJobsPageName) {
    jobPostings { id title locationName employmentType }
  }
}`

type ashbyResponse struct {
	Data struct {
		JobBoard *struct {
			JobPostings []struct {
				ID             string `json:"id"`
				Title          string `json:"title"`
				Location       string `json:"locationName"`
				EmploymentType string `json:"employmentType"`
			} `json:"jobPostings"`
		} `json:"jobBoard"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (s AshbySource) Fetch(ctx context.Context) ([]Job, error) {
	jobs := fetchEach(ashbyCompanies, func(company string) ([]Job, error) {
		payload, err := json.Marshal(map[string]any{
			"operationName": "ApiJobBoardWithTeams",
			"variables":     map[string]string{"organizationHostedJobsPageName": company},
			"query":         ashbyQuery,
		})
		if err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, "POST",
			"https://jobs.ashbyhq.com/api/non-user-graphql?op=ApiJobBoardWithTeams",
			bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", getRandomUserAgent())

		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("ashby %s: %w", company, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("ashby %s: status %d", company, resp.StatusCode)
		}

		raw, err := readAllLimited(resp.Body)
		if err != nil {
			return nil, err
		}

		var parsed ashbyResponse
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, fmt.Errorf("ashby %s: %w", company, err)
		}
		if len(parsed.Errors) > 0 {
			return nil, fmt.Errorf("ashby %s: %s", company, parsed.Errors[0].Message)
		}
		if parsed.Data.JobBoard == nil {
			return nil, fmt.Errorf("ashby %s: no job board (stale slug?)", company)
		}

		var out []Job
		for _, p := range parsed.Data.JobBoard.JobPostings {
			if !looksLikeIntern(p.Title) {
				continue
			}
			out = append(out, Job{
				Id:       fmt.Sprintf("ashby:%s:%s", company, p.ID),
				Title:    p.Title,
				Company:  displayCompany(company),
				Location: p.Location,
				URL:      fmt.Sprintf("https://jobs.ashbyhq.com/%s/%s", company, p.ID),
				Source:   "ashby",
			})
		}
		return out, nil
	})

	fmt.Printf("  %d intern-titled postings across %d companies\n", len(jobs), len(ashbyCompanies))
	return jobs, nil
}

// ---------------------------------------------------------------------------
// Lever
// ---------------------------------------------------------------------------

type LeverSource struct{}

func (LeverSource) Name() string { return "lever" }

type leverPosting struct {
	ID         string `json:"id"`
	Text       string `json:"text"`
	HostedURL  string `json:"hostedUrl"`
	CreatedAt  int64  `json:"createdAt"`
	Categories struct {
		Location string `json:"location"`
	} `json:"categories"`
}

func (s LeverSource) Fetch(ctx context.Context) ([]Job, error) {
	jobs := fetchEach(leverCompanies, func(company string) ([]Job, error) {
		url := fmt.Sprintf("https://api.lever.co/v0/postings/%s?mode=json", company)
		body, err := httpGetBody(ctx, url)
		if err != nil {
			return nil, fmt.Errorf("lever %s: %w", company, err)
		}

		var postings []leverPosting
		if err := json.Unmarshal(body, &postings); err != nil {
			return nil, fmt.Errorf("lever %s: %w", company, err)
		}

		var out []Job
		for _, p := range postings {
			if !looksLikeIntern(p.Text) {
				continue
			}
			posted := ""
			if p.CreatedAt > 0 {
				posted = time.UnixMilli(p.CreatedAt).UTC().Format("2006-01-02")
			}
			out = append(out, Job{
				Id:       fmt.Sprintf("lever:%s:%s", company, p.ID),
				Title:    p.Text,
				Company:  displayCompany(company),
				Location: p.Categories.Location,
				URL:      p.HostedURL,
				PostedAt: posted,
				Source:   "lever",
			})
		}
		return out, nil
	})

	fmt.Printf("  %d intern-titled postings across %d companies\n", len(jobs), len(leverCompanies))
	return jobs, nil
}

// displayCompany turns a board slug into something presentable in an email.
// Filtering is unaffected either way, since normalize() lowercases first.
func displayCompany(slug string) string {
	if name, ok := companyDisplayNames[slug]; ok {
		return name
	}
	words := strings.FieldsFunc(slug, func(r rune) bool { return r == '-' || r == '_' })
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// Slugs whose capitalization the generic rule gets wrong.
var companyDisplayNames = map[string]string{
	"openai":         "OpenAI",
	"gitlab":         "GitLab",
	"mongodb":        "MongoDB",
	"sofi":           "SoFi",
	"zocdoc":         "Zocdoc",
	"elevenlabs":     "ElevenLabs",
	"langchain":      "LangChain",
	"browserbase":    "Browserbase",
	"airtable":       "Airtable",
	"weworkremotely": "We Work Remotely",
}

// shortDate trims an ISO-8601 timestamp to its date part.
func shortDate(ts string) string {
	if len(ts) >= 10 && strings.Count(ts[:10], "-") == 2 {
		return ts[:10]
	}
	return ""
}
