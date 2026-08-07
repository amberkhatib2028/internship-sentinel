package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// vanshb03/Summer2027-Internships is a community-maintained list with a
// machine-readable listings.json. It is the highest-signal source here: already
// scoped to the 2027 cycle, already deduped, and served by GitHub rather than
// scraped, so there is no anti-bot behavior to work around.
const githubListingsURL = "https://raw.githubusercontent.com/vanshb03/Summer2027-Internships/dev/.github/scripts/listings.json"

type GitHubListSource struct{}

func (GitHubListSource) Name() string { return "github-list" }

type githubListing struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Company    string   `json:"company_name"`
	URL        string   `json:"url"`
	Locations  []string `json:"locations"`
	Season     string   `json:"season"`
	Active     bool     `json:"active"`
	IsVisible  bool     `json:"is_visible"`
	DatePosted int64    `json:"date_posted"`
}

func (s GitHubListSource) Fetch(ctx context.Context) ([]Job, error) {
	body, err := httpGetBody(ctx, githubListingsURL)
	if err != nil {
		return nil, fmt.Errorf("fetching listings.json: %w", err)
	}

	var listings []githubListing
	if err := json.Unmarshal(body, &listings); err != nil {
		return nil, fmt.Errorf("parsing listings.json: %w", err)
	}

	var jobs []Job
	for _, l := range listings {
		// The file also carries Fall/Winter/Spring cycles and closed roles.
		if !l.Active || !l.IsVisible || !strings.EqualFold(l.Season, "Summer") {
			continue
		}
		if l.URL == "" {
			continue
		}

		posted := ""
		if l.DatePosted > 0 {
			posted = time.Unix(l.DatePosted, 0).UTC().Format("2006-01-02")
		}

		jobs = append(jobs, Job{
			Id:       "github-list:" + l.ID,
			Title:    l.Title,
			Company:  l.Company,
			Location: strings.Join(l.Locations, ", "),
			URL:      l.URL,
			PostedAt: posted,
			Source:   "github-list",
		})
	}

	fmt.Printf("  %d active Summer listings of %d total\n", len(jobs), len(listings))
	return jobs, nil
}
