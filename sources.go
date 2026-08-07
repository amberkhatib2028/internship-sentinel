package main

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Source is one place we look for listings. Each implementation is responsible
// only for returning raw postings; filtering is shared and lives in filters.go.
type Source interface {
	Name() string
	Fetch(ctx context.Context) ([]Job, error)
}

// Job is a posting from any source.
type Job struct {
	Id       string // globally unique, "<source>:<native id>"
	Title    string
	Company  string
	Location string
	URL      string
	PostedAt string
	Source   string
}

// dedupeKey collapses the same role seen through several sources. A Stripe
// internship reached via LinkedIn and via Stripe's Greenhouse board must not be
// emailed twice, and the two carry unrelated native ids, so identity has to be
// content-based.
//
// Consequence: two genuinely distinct postings with the same company and title
// (commonly the same role listed per-location) collapse into one. That is the
// right trade here — you apply once either way.
func (j Job) dedupeKey() string {
	return normalize(j.Company) + "|" + normalize(j.Title)
}

var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 13_3_1) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/118.0.5993.88 Safari/537.36",
	"Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:114.0) Gecko/20100101 Firefox/114.0",
	"Mozilla/5.0 (Windows NT 6.1; Win64; x64; rv:109.0) Gecko/20100101 Firefox/109.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 11_7_10) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.4 Safari/605.1.15",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:118.0) Gecko/20100101 Firefox/118.0",
}

var referers = []string{
	"https://www.google.com/",
	"https://www.bing.com/",
	"https://news.ycombinator.com/",
}

var languages = []string{
	"en-US,en;q=0.9",
	"en-GB,en;q=0.8",
	"en-AU,en;q=0.9",
	"en-CA,en;q=0.9",
}

func getRandomReferer() string   { return referers[rand.Intn(len(referers))] }
func getRandomUserAgent() string { return userAgents[rand.Intn(len(userAgents))] }
func getRandomLanguage() string  { return languages[rand.Intn(len(languages))] }

var httpClient = &http.Client{Timeout: 20 * time.Second}

// httpGetBody issues a GET and returns the body, failing on any non-200.
func httpGetBody(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", getRandomUserAgent())
	req.Header.Set("Accept", "application/json,text/html,application/xhtml+xml,*/*;q=0.8")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 32<<20))
}

// boardConcurrency bounds simultaneous per-company board requests. The board
// APIs are public and tolerant, but a few dozen companies fanned out at once is
// both rude and a good way to get throttled.
const boardConcurrency = 6

// fetchEach runs fn over items with a bounded worker pool, discarding failures
// after logging them. One company's board being down must not sink the source.
func fetchEach[T any](items []T, fn func(T) ([]Job, error)) []Job {
	var (
		mu   sync.Mutex
		jobs []Job
		wg   sync.WaitGroup
	)
	sem := make(chan struct{}, boardConcurrency)

	for _, item := range items {
		wg.Add(1)
		go func(item T) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			got, err := fn(item)
			if err != nil {
				mu.Lock()
				fmt.Printf("  warning: %v (%v)\n", err, item)
				mu.Unlock()
				return
			}
			mu.Lock()
			jobs = append(jobs, got...)
			mu.Unlock()
		}(item)
	}
	wg.Wait()
	return jobs
}

// looksLikeIntern is a cheap pre-filter for sources that return a company's
// entire job board. It only avoids carrying thousands of irrelevant postings
// through the pipeline; rejectionReason remains the real gate.
func looksLikeIntern(title string) bool {
	return internRe.MatchString(strings.ToLower(title))
}

// enabledSources resolves which sources to run, preferring the event's value
// over the SOURCES env var. Board and aggregator polling is far heavier than
// the LinkedIn pass, so a deployment can run LinkedIn on a tight schedule and
// everything else on a slower one.
func enabledSources(all []Source, override string) []Source {
	want := strings.TrimSpace(override)
	if want == "" {
		want = envOr("SOURCES", "all")
	}
	if want == "all" {
		return all
	}
	set := map[string]bool{}
	for _, name := range strings.Split(want, ",") {
		set[strings.ToLower(strings.TrimSpace(name))] = true
	}

	var out []Source
	for _, s := range all {
		if set[strings.ToLower(s.Name())] {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		fmt.Printf("warning: SOURCES=%q matched nothing; running all sources\n", want)
		return all
	}
	return out
}

func allSources() []Source {
	return []Source{
		LinkedInSource{},
		GitHubListSource{},
		GreenhouseSource{},
		AshbySource{},
		LeverSource{},
		RemoteOKSource{},
		WeWorkRemotelySource{},
	}
}
