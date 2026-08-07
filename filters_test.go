package main

import "testing"

func TestRejectionReason_Keeps(t *testing.T) {
	keep := []Job{
		{Title: "Software Engineer Intern", Company: "Stripe"},
		{Title: "Software Engineering Intern - Summer 2027", Company: "Datadog"},
		{Title: "Intern - Software Engineer", Company: "Cloudflare"},
		{Title: "Intern, Backend Engineering", Company: "Figma"},
		{Title: "2027 Software Engineering Summer Internship", Company: "Airbnb"},
		{Title: "SWE Intern (Full Stack)", Company: "Notion"},
		{Title: "Machine Learning Intern", Company: "Hugging Face"},
		{Title: "Site Reliability Engineering Intern", Company: "Shopify"},
		{Title: "Software Development Intern", Company: "Capital One"},
		// Untagged term must survive — most listings have no term in the title.
		{Title: "Engineering Intern", Company: "Some Startup"},
		// Co-op and "summer analyst" are internships under other names.
		{Title: "Software Developer Co-op", Company: "AVEVA"},
		{Title: "Global Technology Summer Analyst 2027 - Software Engineer", Company: "Bank of America"},
		// A software signal must beat a co-occurring hardware/discipline word.
		{Title: "Imaging Software Engineer Intern", Company: "Dolby Laboratories"},
		{Title: "Enterprise Systems Software Engineer Intern", Company: "Acme"},
		{Title: "Software Engineer Intern - Robotics", Company: "PlusAI"},
		// "undergraduate" must not trip the \bgraduate\b level filter.
		{Title: "Software Engineer Intern (Undergraduate)", Company: "Acme"},
		// App development stays in scope — only Android is out. She shipped an
		// iOS app in React Native.
		{Title: "iOS Developer Internship", Company: "Duolingo"},
		{Title: "Mobile Engineering Intern", Company: "Acme"},
		{Title: "React Native Engineer Intern", Company: "Acme"},
		{Title: "Software Engineer Intern, Mobile", Company: "Acme"},
		// ML/AI roles are in scope.
		{Title: "AI Engineer Intern", Company: "NOSO Labs"},
		{Title: "Data Science Intern", Company: "Spotify"},
		{Title: "Deep Learning Intern", Company: "NVIDIA"},
		{Title: "Computer Vision Intern", Company: "Waymo"},
		{Title: "Applied Scientist Intern", Company: "Amazon"},
		{Title: "Artificial Intelligence, Data & Machine Learning Intern", Company: "Intercontinental Exchange"},
		{Title: "AI Builder Intern", Company: "Some AI Startup"},
	}

	for _, job := range keep {
		if reason := rejectionReason(job); reason != "" {
			t.Errorf("wrongly rejected %q @ %s: %s", job.Title, job.Company, reason)
		}
	}
}

func TestRejectionReason_Rejects(t *testing.T) {
	cases := []struct {
		job  Job
		want string // substring of the expected reason
	}{
		// Not an internship
		{Job{Title: "Software Engineer", Company: "Stripe"}, "not an internship"},
		{Job{Title: "Internal Tools Engineer", Company: "Stripe"}, "not an internship"},
		{Job{Title: "International Sales Engineer", Company: "Stripe"}, "not an internship"},

		// Internship, but not engineering
		{Job{Title: "Marketing Intern", Company: "Stripe"}, "not a software engineering"},
		{Job{Title: "Finance Intern", Company: "Stripe"}, "not a software engineering"},

		// Wrong term
		{Job{Title: "Software Engineer Intern - Summer 2026", Company: "Stripe"}, "wrong term"},
		{Job{Title: "SWE Intern Fall 2027", Company: "Stripe"}, "wrong term"},
		{Job{Title: "Software Engineering Intern (Spring 2027)", Company: "Stripe"}, "wrong term"},

		// Wrong level
		{Job{Title: "Senior Software Engineer Internship Program", Company: "Stripe"}, "wrong level"},
		{Job{Title: "PhD Software Engineering Intern", Company: "Google"}, "wrong level"},
		{Job{Title: "Software Engineering Intern - Masters", Company: "Meta"}, "wrong level"},

		// Quant — by title
		{Job{Title: "Quantitative Software Engineer Intern", Company: "Some Fund"}, "quant keyword"},
		{Job{Title: "Software Engineer Intern, Trading Systems", Company: "Some Fund"}, "quant keyword"},

		// Quant — by company
		{Job{Title: "Software Engineer Intern", Company: "Jane Street"}, "quant firm"},
		{Job{Title: "Software Engineering Intern", Company: "Citadel Securities"}, "quant firm"},
		{Job{Title: "Backend Intern", Company: "Hudson River Trading"}, "quant firm"},
		{Job{Title: "SWE Intern", Company: "SIG Susquehanna"}, "quant firm"},
		{Job{Title: "Software Intern", Company: "D. E. Shaw & Co."}, "quant firm"},

		// Defense — by title
		{Job{Title: "Software Engineer Intern - Active Clearance Required", Company: "Acme"}, "defense keyword"},
		{Job{Title: "Software Engineering Intern (TS/SCI)", Company: "Acme"}, "defense keyword"},
		{Job{Title: "Defense Software Engineering Intern", Company: "Acme"}, "defense keyword"},

		// Defense — by company
		{Job{Title: "Software Engineer Intern", Company: "Lockheed Martin"}, "defense firm"},
		{Job{Title: "Software Engineering Intern", Company: "L3Harris Technologies, Inc."}, "defense firm"},
		{Job{Title: "SWE Intern", Company: "Booz Allen Hamilton"}, "defense firm"},
		{Job{Title: "Software Intern", Company: "The MITRE Corporation"}, "defense firm"},
		{Job{Title: "Backend Intern", Company: "Johns Hopkins Applied Physics Laboratory"}, "defense firm"},
		{Job{Title: "Software Engineer Intern", Company: "Anduril Industries"}, "defense firm"},
		{Job{Title: "Software Engineer Intern", Company: "Palantir Technologies"}, "defense firm"},
		{Job{Title: "Software Engineer Intern", Company: "RTX"}, "defense firm"},

		// Spam posters
		{Job{Title: "Software Engineer Intern", Company: "Jobot"}, "blocked poster"},
		{Job{Title: "Software Developer Intern", Company: "Insight Global"}, "blocked poster"},
		{Job{Title: "Software Engineer Intern", Company: "Jobright.ai"}, "blocked poster"},
		{Job{Title: "Summer 2027 Software Engineering Intern", Company: "SpeedyApply"}, "blocked poster"},

		// Non-software engineering disciplines
		{Job{Title: "Electrical Engineering Internships/Co-ops - $22/hr", Company: "RCT Systems"}, "different engineering discipline"},
		{Job{Title: "Design Engineering Intern", Company: "MeeBoss"}, "different engineering discipline"},
		{Job{Title: "Mechanical Engineering Intern", Company: "Acme"}, "different engineering discipline"},
		{Job{Title: "Avionics Engineering Intern", Company: "Acme"}, "different engineering discipline"},
		{Job{Title: "Systems Engineering Intern", Company: "Acme"}, "different engineering discipline"},
		// "robotics" is deliberately not a strong-software override.
		{Job{Title: "Robotics Hardware & Supplier Engineering Intern", Company: "Anyware Robotics"}, "different engineering discipline"},
		// No engineering signal at all, so this is caught one gate earlier.
		{Job{Title: "Analog IC Design Co-op", Company: "Skyworks"}, "not a software engineering"},
		// ML titles must still respect the quant and level filters.
		{Job{Title: "Quantitative Developer Intern", Company: "Point72"}, "quant"},
		{Job{Title: "Machine Learning Intern - PhD", Company: "Acme"}, "wrong level"},

		// Season without a year, and cohort-restricted programs
		{Job{Title: "Software Development Engineer Fall Intern - Military Veteran", Company: "Amazon"}, "wrong term"},
		{Job{Title: "Software Engineering Spring Co-op", Company: "Acme"}, "wrong term"},
		{Job{Title: "Year-Round Graduate Data Engineer Intern, DATA", Company: "Federal Reserve Board"}, "wrong level"},

		// Defense labs surfaced by the dry run
		{Job{Title: "R&D Software Engineer Intern", Company: "The Applied Research Laboratory at Penn State University"}, "defense firm"},
		{Job{Title: "Internship - Machine Learning Research", Company: "Novateur Research Solutions"}, "defense firm"},

		// Android-specific — she is not an Android developer
		{Job{Title: "Software Engineer Internship, Android", Company: "Ramp"}, "android-specific"},
		{Job{Title: "Kotlin Software Engineer Intern", Company: "Acme"}, "android-specific"},

		// Commodities trading
		{Job{Title: "Full-Stack Software Engineer Internship (Summer 2027)", Company: "Castleton Commodities International"}, "quant firm"},
	}

	for _, tc := range cases {
		reason := rejectionReason(tc.job)
		if reason == "" {
			t.Errorf("failed to reject %q @ %s (expected %q)", tc.job.Title, tc.job.Company, tc.want)
			continue
		}
		if !contains(reason, tc.want) {
			t.Errorf("%q @ %s: got reason %q, want it to mention %q",
				tc.job.Title, tc.job.Company, reason, tc.want)
		}
	}
}

// Short blocklist entries must not fire on unrelated companies that merely
// contain those letters.
func TestShortEntriesDoNotOvermatch(t *testing.T) {
	safe := []Job{
		{Title: "Software Engineer Intern", Company: "Signal Messenger"},   // "sig"
		{Title: "Software Engineer Intern", Company: "Sigma Computing"},    // "sig"
		{Title: "Software Engineer Intern", Company: "IMCentrix Health"},   // "imc"
		{Title: "Software Engineer Intern", Company: "Drawbridge Systems"}, // "drw"
		{Title: "Software Engineer Intern", Company: "Shorthand"},          // "hrt"
	}

	for _, job := range safe {
		if reason := rejectionReason(job); reason != "" {
			t.Errorf("overmatched %q @ %s: %s", job.Title, job.Company, reason)
		}
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"L3Harris Technologies, Inc.": "l3harris technologies inc",
		"D. E. Shaw & Co.":            "d e shaw co",
		"  Two  Sigma  ":              "two sigma",
	}
	for in, want := range cases {
		if got := normalize(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
