package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"
)

// Remote-only aggregators. Both are low yield for internships specifically —
// typically a handful of intern postings at a time — but they are a single
// cheap request each and occasionally surface small companies that never reach
// LinkedIn.

// ---------------------------------------------------------------------------
// RemoteOK
// ---------------------------------------------------------------------------

type RemoteOKSource struct{}

func (RemoteOKSource) Name() string { return "remoteok" }

type remoteOKEntry struct {
	ID       json.RawMessage `json:"id"`
	Position string          `json:"position"`
	Company  string          `json:"company"`
	Location string          `json:"location"`
	URL      string          `json:"url"`
	Date     string          `json:"date"`
}

func (s RemoteOKSource) Fetch(ctx context.Context) ([]Job, error) {
	body, err := httpGetBody(ctx, "https://remoteok.com/api")
	if err != nil {
		return nil, fmt.Errorf("remoteok: %w", err)
	}

	var entries []remoteOKEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("remoteok: %w", err)
	}

	var jobs []Job
	for _, e := range entries {
		// The first element is a legal/disclaimer object with no position.
		if e.Position == "" || e.URL == "" {
			continue
		}
		if !looksLikeIntern(e.Position) {
			continue
		}
		location := e.Location
		if location == "" {
			location = "Remote"
		}
		jobs = append(jobs, Job{
			Id:       "remoteok:" + strings.Trim(string(e.ID), `"`),
			Title:    e.Position,
			Company:  e.Company,
			Location: location,
			URL:      e.URL,
			PostedAt: shortDate(e.Date),
			Source:   "remoteok",
		})
	}

	fmt.Printf("  %d intern-titled of %d postings\n", len(jobs), len(entries))
	return jobs, nil
}

// ---------------------------------------------------------------------------
// We Work Remotely
// ---------------------------------------------------------------------------

type WeWorkRemotelySource struct{}

func (WeWorkRemotelySource) Name() string { return "weworkremotely" }

type wwrFeed struct {
	Channel struct {
		Items []struct {
			Title  string `xml:"title"`
			Link   string `xml:"link"`
			Region string `xml:"region"`
			PubHdr string `xml:"pubDate"`
		} `xml:"item"`
	} `xml:"channel"`
}

func (s WeWorkRemotelySource) Fetch(ctx context.Context) ([]Job, error) {
	body, err := httpGetBody(ctx, "https://weworkremotely.com/categories/remote-programming-jobs.rss")
	if err != nil {
		return nil, fmt.Errorf("weworkremotely: %w", err)
	}

	var feed wwrFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("weworkremotely: %w", err)
	}

	var jobs []Job
	for _, item := range feed.Channel.Items {
		// Titles arrive as "Company: Role".
		company, title := "", item.Title
		if idx := strings.Index(item.Title, ":"); idx > 0 {
			company = strings.TrimSpace(item.Title[:idx])
			title = strings.TrimSpace(item.Title[idx+1:])
		}
		if !looksLikeIntern(title) {
			continue
		}
		location := item.Region
		if location == "" {
			location = "Remote"
		}
		jobs = append(jobs, Job{
			Id:       "weworkremotely:" + item.Link,
			Title:    title,
			Company:  company,
			Location: location,
			URL:      item.Link,
			Source:   "weworkremotely",
		})
	}

	fmt.Printf("  %d intern-titled of %d postings\n", len(jobs), len(feed.Channel.Items))
	return jobs, nil
}
