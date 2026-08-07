package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const (
	linkedInBaseURL = "https://www.linkedin.com/jobs/search/?"

	// Quoted phrases OR'd together. This is the coarse server-side pass; the
	// real gate is rejectionReason() in filters.go.
	linkedInKeywords = `"Software Engineer Intern" OR "Software Engineering Intern" OR ` +
		`"Software Developer Intern" OR "Software Development Intern" OR ` +
		`"SWE Intern" OR "Backend Intern" OR "Frontend Intern" OR ` +
		`"Full Stack Intern" OR "Machine Learning Intern" OR "AI Intern" OR ` +
		`"Data Science Intern" OR "Engineering Intern" OR "Software Engineering Internship"`

	usaGeoID = "103644278"

	// LinkedIn job-type facet. "I" = Internship. Set to "" to widen the search
	// to listings that were mis-tagged as full-time.
	jobType = "I"
)

// linkedInSearch is one query against the guest search page.
//
// Only geoId-based searches are used. A location-string search for the DC/DMV
// metro was measured succeeding roughly a third of the time, and the nationwide
// pass already covers those postings.
type linkedInSearch struct {
	Label  string
	GeoID  string
	Remote bool
}

var linkedInSearches = []linkedInSearch{
	{Label: "remote", GeoID: usaGeoID, Remote: true},
	{Label: "nationwide", GeoID: usaGeoID},
}

type LinkedInSource struct{}

func (LinkedInSource) Name() string { return "linkedin" }

func (s LinkedInSource) Fetch(ctx context.Context) ([]Job, error) {
	var all []Job
	for i, search := range linkedInSearches {
		if i > 0 {
			time.Sleep(time.Duration(3000+rand.Intn(3000)) * time.Millisecond)
		}

		jobs, err := fetchWithRetry(ctx, search)
		if err != nil {
			if errors.Is(err, errEmptyResults) {
				fmt.Printf("  [%s] no results after %d attempts\n", search.Label, fetchAttempts)
			} else {
				fmt.Printf("  [%s] warning: %v\n", search.Label, err)
			}
			continue
		}
		fmt.Printf("  [%s] %d scraped\n", search.Label, len(jobs))
		all = append(all, jobs...)
	}
	return all, nil
}

// errEmptyResults means LinkedIn returned its "no results" page. That page is
// ambiguous — it is served both for a genuinely empty search and when the guest
// endpoint is throttling — so it is retried rather than believed.
var errEmptyResults = errors.New("linkedin returned a no-results page")

// Measured reliability of the guest endpoint: geoId-based searches answer
// essentially every time. Retries remain because throttling still produces the
// occasional false "no results".
const fetchAttempts = 3

func fetchWithRetry(ctx context.Context, search linkedInSearch) ([]Job, error) {
	var err error
	for attempt := 1; attempt <= fetchAttempts; attempt++ {
		var jobs []Job
		jobs, err = fetchLinkedIn(ctx, search)
		if err == nil {
			return jobs, nil
		}
		if attempt < fetchAttempts {
			backoff := time.Duration(4000+rand.Intn(4000)) * time.Millisecond
			fmt.Printf("  [%s] attempt %d/%d: %v — retrying in %v\n",
				search.Label, attempt, fetchAttempts, err, backoff.Round(time.Millisecond))
			time.Sleep(backoff)
		}
	}
	return nil, err
}

func cleanURL(rawURL string) (string, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	q := parsedURL.Query()
	// Remove LinkedIn trackingId if present
	q.Del("trackingId")
	parsedURL.RawQuery = q.Encode()
	return parsedURL.String(), nil
}

func buildLinkedInURL(search linkedInSearch) *url.URL {
	u, _ := url.Parse(linkedInBaseURL)
	params := url.Values{}
	params.Set("keywords", linkedInKeywords)
	params.Set("f_TPR", "r"+strconv.Itoa(lookbackSeconds()))
	if jobType != "" {
		params.Set("f_JT", jobType)
	}
	if search.GeoID != "" {
		params.Set("geoId", search.GeoID)
	}
	if search.Remote {
		params.Set("f_WT", "2")
	}
	u.RawQuery = params.Encode()
	return u
}

func fetchLinkedIn(ctx context.Context, search linkedInSearch) ([]Job, error) {
	u := buildLinkedInURL(search)

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", getRandomUserAgent())
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", getRandomLanguage())
	req.Header.Set("Referer", getRandomReferer())
	req.Header.Set("DNT", "1")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("rate limited by LinkedIn (429)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	body, err := readAllLimited(resp.Body)
	if err != nil {
		return nil, err
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parsing HTML: %w", err)
	}

	// LinkedIn serves this same "no results" page both when a search genuinely
	// has no hits and when it is throttling us, so the caller retries rather
	// than trusting it outright.
	if doc.Find(".no-results").Length() > 0 {
		return nil, errEmptyResults
	}

	var jobs []Job
	doc.Find(".jobs-search__results-list li").Each(func(i int, sel *goquery.Selection) {
		urn, _ := sel.Find(".base-card").Attr("data-entity-urn")
		urlFromHTML, _ := sel.Find(".base-card__full-link").Attr("href")
		linkURL, err := cleanURL(urlFromHTML)
		if err != nil {
			fmt.Printf("  error parsing job link: %v: %v\n", urlFromHTML, err)
			return
		}

		id := strings.TrimPrefix(urn, "urn:li:jobPosting:")
		if id == "" {
			return
		}

		jobs = append(jobs, Job{
			Id:       "linkedin:" + id,
			Title:    strings.TrimSpace(sel.Find(".base-search-card__title").Text()),
			Company:  strings.TrimSpace(sel.Find(".base-search-card__subtitle a").Text()),
			Location: strings.TrimSpace(sel.Find(".job-search-card__location").Text()),
			URL:      linkURL,
			PostedAt: strings.TrimSpace(sel.Find(".job-search-card__listdate").Text()),
			Source:   "linkedin",
		})
	})

	return jobs, nil
}
