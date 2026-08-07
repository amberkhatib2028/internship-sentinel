// GOOS=linux GOARCH=amd64 go build -tags lambda.norpc -o bootstrap .
// zip sentinel.zip bootstrap
//
// Local dry run (no AWS calls, prints what it would send):
//   go run . -dry-run

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

const (
	// Only the LinkedIn source uses this; it is a server-side "posted within"
	// filter. Deliberately ~3x the 10-minute EventBridge rate, because LinkedIn
	// throttles a share of guest requests into a false "no results" page and
	// f_TPR is a moving window. With a 30-minute window each posting is seen by
	// three consecutive runs, and the S3 key set stops duplicate emails.
	//
	// Every other source returns a full current snapshot, so for those the key
	// set alone decides what is new.
	defaultLookbackSeconds = 1800

	defaultS3Bucket = "internship-sentinel-jobs"

	// Distinct key so this deployment's dedupe state never collides with the
	// upstream project's sent_jobs.json when both share a bucket.
	defaultS3Key = "sent_jobs_swe_intern_2027.json"

	// Every subject line starts with this, so a single mail rule can route all
	// of them into a dedicated folder. Changing it breaks existing filters.
	subjectPrefix = "[SWE-Intern]"
)

type Event struct {
	Email string `json:"email"`
	// Sources lets one EventBridge rule run the cheap LinkedIn pass often while
	// another sweeps every board on a slower schedule, both against the same
	// function. Falls back to the SOURCES env var when empty.
	Sources string `json:"sources"`
}

// Result groups the surviving jobs for one source.
type Result struct {
	Source string
	Jobs   []Job
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func lookbackSeconds() int {
	if v := os.Getenv("LOOKBACK_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
		fmt.Printf("warning: ignoring invalid LOOKBACK_SECONDS=%q\n", v)
	}
	return defaultLookbackSeconds
}

func readAllLimited(r io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, 32<<20))
}

func loadSentKeys(ctx context.Context, s3Client *s3.Client, bucket, key string) (map[string]bool, error) {
	result := make(map[string]bool)

	resp, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		// If file doesn't exist yet, return empty map
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchKey" {
			return result, nil
		}
		return nil, err
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

func saveSentKeys(ctx context.Context, s3Client *s3.Client, bucket, key string, keys map[string]bool) error {
	data, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(data),
	})
	return err
}

// collect runs every enabled source, drops anything already sent or already
// seen in this run, and applies the filters. Returns the surviving jobs grouped
// by source plus the dedupe keys to persist.
func collect(ctx context.Context, sentKeys map[string]bool, sourceOverride string) (results []Result, newKeys []string) {
	seenThisRun := make(map[string]bool)
	sources := enabledSources(allSources(), sourceOverride)

	for _, source := range sources {
		fmt.Printf("[%s] fetching...\n", source.Name())

		jobs, err := source.Fetch(ctx)
		if err != nil {
			// One source failing must never sink the run.
			fmt.Printf("[%s] warning: %v\n", source.Name(), err)
			continue
		}

		var kept []Job
		for _, job := range jobs {
			key := job.dedupeKey()
			if job.Id == "" || sentKeys[key] || seenThisRun[key] {
				continue
			}
			if reason := rejectionReason(job); reason != "" {
				fmt.Printf("[%s] filtered %q @ %s — %s\n", source.Name(), job.Title, job.Company, reason)
				continue
			}
			seenThisRun[key] = true
			newKeys = append(newKeys, key)
			kept = append(kept, job)
		}

		fmt.Printf("[%s] %d new after filtering\n", source.Name(), len(kept))
		if len(kept) > 0 {
			sort.Slice(kept, func(i, j int) bool {
				if kept[i].Company != kept[j].Company {
					return kept[i].Company < kept[j].Company
				}
				return kept[i].Title < kept[j].Title
			})
			results = append(results, Result{Source: source.Name(), Jobs: kept})
		}
	}
	return results, newKeys
}

func totalJobs(results []Result) int {
	n := 0
	for _, r := range results {
		n += len(r.Jobs)
	}
	return n
}

func buildTextBody(results []Result) string {
	var b strings.Builder
	for _, r := range results {
		fmt.Fprintf(&b, "%s (%d):\n\n", r.Source, len(r.Jobs))
		for _, job := range r.Jobs {
			fmt.Fprintf(&b, "Title: %s\nCompany: %s\nLocation: %s\nPosted: %s\nLink: %s\n\n",
				job.Title, job.Company, job.Location, job.PostedAt, job.URL)
		}
	}
	return b.String()
}

func buildHTMLBody(results []Result) string {
	var b strings.Builder
	b.WriteString(`<html><body style="font-family:-apple-system,Segoe UI,Helvetica,Arial,sans-serif;font-size:14px;line-height:1.5;">`)
	for _, r := range results {
		fmt.Fprintf(&b, `<h2 style="font-size:16px;margin:20px 0 8px;">%s <span style="color:#888;font-weight:normal;">(%d)</span></h2><ul style="padding-left:18px;">`,
			html.EscapeString(r.Source), len(r.Jobs))
		for _, job := range r.Jobs {
			fmt.Fprintf(&b, `<li style="margin-bottom:12px;"><a href="%s"><strong>%s</strong></a><br>%s &middot; %s`,
				html.EscapeString(job.URL),
				html.EscapeString(job.Title),
				html.EscapeString(job.Company),
				html.EscapeString(job.Location))
			if job.PostedAt != "" {
				b.WriteString(" &middot; " + html.EscapeString(job.PostedAt))
			}
			b.WriteString("</li>")
		}
		b.WriteString("</ul>")
	}
	b.WriteString(`<p style="color:#888;font-size:12px;">Summer 2027 SWE/ML internships. Quant and defense employers filtered out.</p>`)
	b.WriteString(`</body></html>`)
	return b.String()
}

func handler(ctx context.Context, event Event) error {
	// Recipient comes from the EventBridge payload, but fall back to an env
	// var so the Lambda works with an empty test event.
	toEmail := event.Email
	if toEmail == "" {
		toEmail = os.Getenv("SENTINEL_TO_EMAIL")
	}
	if toEmail == "" {
		return fmt.Errorf(`no recipient: set the event's "email" field or the SENTINEL_TO_EMAIL environment variable`)
	}

	bucket := envOr("S3_BUCKET", defaultS3Bucket)
	key := envOr("S3_KEY", defaultS3Key)

	cfg, err := loadAWSConfig(ctx)
	if err != nil {
		return err
	}

	s3Client := s3.NewFromConfig(cfg)
	sentKeys, err := loadSentKeys(ctx, s3Client, bucket, key)
	if err != nil {
		return fmt.Errorf("failed to load sent keys: %v", err)
	}
	firstRun := len(sentKeys) == 0

	results, newKeys := collect(ctx, sentKeys, event.Sources)

	if len(results) == 0 {
		fmt.Println("no new jobs to send")
		return nil
	}
	if firstRun {
		fmt.Printf("first run against an empty key set — emailing the current backlog (%d jobs)\n", totalJobs(results))
	}

	if err := newNotifier(cfg, toEmail).Send(ctx, results); err != nil {
		return fmt.Errorf("failed to send notification: %v", err)
	}
	fmt.Printf("sent %d new jobs to %s\n", totalJobs(results), toEmail)

	// Only mark as sent once the email actually went out, so a SES failure
	// doesn't silently swallow the listings.
	for _, k := range newKeys {
		sentKeys[k] = true
	}
	if err := saveSentKeys(ctx, s3Client, bucket, key, sentKeys); err != nil {
		return fmt.Errorf("failed to save sent keys: %v", err)
	}

	return nil
}

// dryRun runs every enabled source without touching S3 or SES, so filter
// changes can be tuned locally. Every rejection prints its reason.
func dryRun(sources *string) {
	fmt.Printf("DRY RUN — no AWS calls, LinkedIn lookback %ds\n\n", lookbackSeconds())

	results, _ := collect(context.Background(), map[string]bool{}, *sources)
	if len(results) == 0 {
		fmt.Println("\nNothing survived the filters.")
		return
	}

	fmt.Printf("\n===== would email %d job(s) =====\n\n", totalJobs(results))
	fmt.Print(buildTextBody(results))
}

func main() {
	dry := flag.Bool("dry-run", false, "run all sources locally without calling S3 or SES")
	sources := flag.String("sources", "", "comma-separated sources to run (default: SOURCES env, else all)")
	flag.Parse()

	if *dry {
		dryRun(sources)
		return
	}
	lambda.Start(handler)
}
