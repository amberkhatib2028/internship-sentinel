package main

import (
	"regexp"
	"strings"
)

// ---------------------------------------------------------------------------
// Title matching
//
// A listing is kept only if its title reads as BOTH an internship AND a
// software-engineering role. Matching on two independent signals instead of a
// fixed phrase list means we survive LinkedIn's endless title permutations
// ("Intern - Software Engineer", "2027 Software Engineering Summer Analyst",
// "Intern, Backend").
// ---------------------------------------------------------------------------

// \bintern\b, \binterns\b, \binternship\b — but never "internal" or "international".
// "co-op" and "summer analyst" are the same thing under different names; banks
// and large engineering firms use them almost exclusively.
var internRe = regexp.MustCompile(`(\bintern(ship)?s?\b|\bco[ -]?op\b|\bsummer analyst\b|\bsummer technology analyst\b)`)

// Engineering signal. Any one of these in the title qualifies.
var engineeringRe = regexp.MustCompile(`\b(` + strings.Join([]string{
	`software`,
	`swe`,
	`developer`,
	`development`,
	`engineer`,
	`engineering`,
	`programmer`,
	`programming`,
	`backend`, `back[ -]end`,
	`frontend`, `front[ -]end`,
	`full[ -]?stack`,
	`web dev\w*`,
	`mobile dev\w*`,
	`ios`, `android`,
	// ML/AI counts as in-scope.
	`machine learning`, `\bml\b`, `\bmlops\b`,
	`artificial intelligence`, `\bai\b`,
	`deep learning`, `computer vision`, `\bnlp\b`, `\bllm\b`,
	`data science`, `data scientist`,
	`applied scientist`, `research engineer`,
	`perception`, `robotics`,
	`computer science`, `\bcs\b`,
	`data engineer\w*`,
	`platform`,
	`infrastructure`,
	`devops`,
	`site reliability`, `\bsre\b`,
	`cloud`,
	`compiler`,
	`systems`,
}, `|`) + `)\b`)

// Unambiguous software signals. If one of these is present, the listing is
// software work even when a non-software discipline also appears in the title
// ("Imaging Software Engineer Intern", "Enterprise Systems Software Engineer").
var strongSoftwareRe = regexp.MustCompile(`\b(` + strings.Join([]string{
	`software`, `swe`,
	`developer`, `programmer`, `programming`,
	`backend`, `back[ -]end`,
	`frontend`, `front[ -]end`,
	`full[ -]?stack`,
	`web dev\w*`, `web development`,
	// ML/AI signals are strong enough to override a co-occurring discipline
	// word, except "robotics"/"perception", which are left out deliberately so
	// that e.g. "Robotics Hardware Intern" is still caught as hardware.
	`machine learning`, `\bml\b`, `\bmlops\b`,
	`artificial intelligence`, `\bai\b`,
	`deep learning`, `computer vision`, `\bnlp\b`, `\bllm\b`,
	`data science`, `data scientist`,
	`data engineer\w*`,
	`infrastructure`, `devops`, `site reliability`, `\bsre\b`,
	`cloud`, `compiler`, `firmware`,
	`ios`, `android`,
	`computer science`,
}, `|`) + `)\b`)

// Engineering disciplines that are not software. These titles reach us because
// they contain "engineering intern"; reject them unless a strong software
// signal is also present.
var otherDisciplineRe = regexp.MustCompile(`\b(` + strings.Join([]string{
	`electrical`, `mechanical`, `civil`, `chemical`, `industrial`,
	`aerospace`, `aeronautical`, `astronautical`, `avionics`,
	`biomedical`, `bioengineering`, `biological`,
	`structural`, `materials`, `metallurgical`,
	`petroleum`, `nuclear`, `environmental`, `geotechnical`, `mining`,
	`agricultural`, `ocean`, `naval architecture`,
	`optical`, `photonics`, `antenna`, `\brf\b`,
	`hardware`, `\bpcb\b`, `\basic\b`, `\bvlsi\b`, `semiconductor`,
	`packaging`, `manufacturing`, `plant`, `hvac`, `automotive`,
	`process engineer\w*`, `quality engineer\w*`, `design engineer\w*`,
	`systems engineer\w*`,
}, `|`) + `)\b`)

// ---------------------------------------------------------------------------
// Android
//
// App development in general is in scope — Amber shipped KazeTune, an iOS app,
// in React Native. Only Android-specific roles are dropped, since those ask for
// Kotlin and the Android SDK, which she does not have.
//
// Note how narrow this deliberately is: no `swift`, no `ios`, no `mobile`.
// Widening it to those would cut app-development roles she does want. Add an
// entry here if a stack later turns out to be a mismatch.
// ---------------------------------------------------------------------------

var androidRe = regexp.MustCompile(`\b(` + strings.Join([]string{
	`android`,
	`kotlin`,
	`jetpack compose`,
}, `|`) + `)\b`)

// ---------------------------------------------------------------------------
// Term / year targeting — Summer 2027
//
// We are targeting a Summer 2027 internship. Most listings do not put a term in
// the title at all, so the rule is permissive: reject only when the title names
// a term we know is wrong, and let untagged titles through.
// ---------------------------------------------------------------------------

var wrongTermRe = regexp.MustCompile(`\b(` + strings.Join([]string{
	// Past or already-in-progress cycles.
	`20(23|24|25|26)`,
	// 2027 cycles that are not summer. Fall 2027 / Spring 2028 collide with
	// senior year coursework; graduation is Spring 2028.
	`fall 2027`, `autumn 2027`, `winter 2027`, `spring 2027`,
	// Terms after graduation are new-grad roles, not internships.
	`20(29|3\d)`,
	// Seasons named without a year. The only cycle open to a rising junior
	// right now is summer, so a title that says "Fall Intern" is wrong
	// regardless of which year it means.
	`fall intern\w*`, `autumn intern\w*`, `winter intern\w*`, `spring intern\w*`,
	`fall co[ -]?op`, `spring co[ -]?op`, `winter co[ -]?op`,
	`fall analyst`, `spring analyst`,
}, `|`) + `)\b`)

// ---------------------------------------------------------------------------
// Seniority / eligibility exclusions
//
// Intern-titled listings that are actually for PhD candidates, MBAs, or
// experienced hires. Amber is a rising junior (BS, Spring 2028).
// ---------------------------------------------------------------------------

var wrongLevelRe = regexp.MustCompile(`\b(` + strings.Join([]string{
	`senior`, `sr\.?`,
	`staff`, `principal`, `lead`,
	`director`, `head of`, `vp`, `vice president`,
	`manager`, `management`,
	`architect`,
	`ph\.?d`, `doctoral`, `postdoc\w*`,
	`\bmba\b`,
	`masters`, `master's`, `graduate student`,
	// \bgraduate\b does not fire on "undergraduate" — the preceding "r" is a
	// word character, so there is no boundary there.
	`graduate`,
	`new grad`,
	`apprentice\w*`,
	`return offer only`,
	// Cohort-restricted programs Amber is not eligible for.
	`military veteran`, `veterans only`, `military spouse`,
	`high school`,
}, `|`) + `)\b`)

// ---------------------------------------------------------------------------
// Quant / trading exclusions
// ---------------------------------------------------------------------------

var quantKeywordRe = regexp.MustCompile(`\b(` + strings.Join([]string{
	`quant`, `quantitative`,
	`trading`, `trader`,
	`market making`, `market maker`,
	`hedge fund`,
	`proprietary trading`,
	`algorithmic trading`,
	`high frequency`,
	`portfolio manager`,
}, `|`) + `)\b`)

// Company names are normalized (lowercase, punctuation stripped) before
// matching. Entries of 4 characters or fewer with no space match whole tokens
// only, so "hrt" does not fire on "Shorthand Robotics".
var quantFirms = []string{
	"jane street",
	"citadel",
	"two sigma",
	"jump trading", "jump crypto",
	"hudson river trading", "hrt",
	"optiver",
	"imc trading", "imc",
	"drw",
	"susquehanna", "sig",
	"akuna capital",
	"old mission",
	"five rings",
	"radix trading",
	"tower research",
	"headlands technologies",
	"quantlab",
	"vatic labs",
	"point72", "cubist",
	"millennium management",
	"balyasny",
	"de shaw", "d e shaw",
	"aqr capital",
	"virtu financial",
	"xtx markets",
	"squarepoint",
	"belvedere trading",
	"chicago trading company",
	"group one trading",
	"peak6",
	"wolverine trading",
	"transmarket",
	"flow traders",
	"maven securities",
	"qube research",
	"geneva trading",
	"simplex trading",
	"valkyrie trading",
	"eagle seven",
	"teza technologies",
	"voleon",
	"engineers gate",
	"walleye capital",
	"schonfeld",
	"exoduspoint",
	"marshall wace",
	"arrowstreet capital",
	"dv trading",
	"allston trading",
	"tradebot",
	"hehmeyer",
	"wintermute",
	"gts securities",
	"aquatic capital", "aquatic",

	// Physical-commodities trading houses — same trading-desk culture.
	"castleton commodities",
	"trafigura",
	"vitol",
	"mercuria",
	"gunvor",
	"freepoint commodities",
	"hartree partners",
}

// ---------------------------------------------------------------------------
// Defense exclusions (strict)
//
// Covers traditional primes, the services/IT contractors that dominate DMV
// listings, the FFRDCs and national labs, and defense-tech startups.
// ---------------------------------------------------------------------------

var defenseKeywordRe = regexp.MustCompile(`(` + strings.Join([]string{
	`\bclearance\b`,
	`\bts/sci\b`, `\bts sci\b`,
	`\btop secret\b`,
	`\bsecret\b`,
	`\bpolygraph\b`,
	`\bitar\b`,
	`\bdod\b`, `\bdepartment of defense\b`,
	`\bdefense\b`, `\bdefence\b`,
	`\bwarfare\b`, `\bwarfighter\b`,
	`\bmunitions\b`, `\bweapons\b`,
	`\bmissile\b`,
	`\bintelligence community\b`,
	`\bnational security\b`,
	`\bclassified\b`,
	`\bfederal civilian\b`,
}, `|`) + `)`)

var defenseFirms = []string{
	// Primes
	"lockheed martin", "lockheed",
	"raytheon", "rtx",
	"northrop grumman", "northrop",
	"general dynamics",
	"bae systems",
	"l3harris", "l3 harris",
	"boeing",
	"general atomics",
	"huntington ingalls",
	"textron",
	"sierra nevada corporation",
	"aerovironment",
	"kratos defense", "kratos",
	"mercury systems",
	"elbit systems",
	"rheinmetall",
	"thales",
	"leonardo drs",
	"saab",

	// Services / IT contractors — heavy DMV presence
	"leidos",
	"booz allen",
	"saic", "science applications international",
	"caci",
	"peraton",
	"mantech",
	"parsons corporation",
	"amentum",
	"vectrus", "v2x",
	"cubic corporation",
	"kbr",
	"noblis",
	"two six technologies",
	"systems planning and analysis",
	"arcfield",
	"maximus",
	"gdit", "general dynamics information technology",

	// FFRDCs, national labs, university applied-research centers
	"mitre",
	"aerospace corporation",
	"draper",
	"johns hopkins applied physics", "jhu apl",
	"lincoln laboratory",
	"institute for defense analyses",
	"sandia national",
	"lawrence livermore",
	"los alamos national",
	"battelle",
	"riverside research",
	"georgia tech research institute",
	// Penn State ARL and its peers are Navy-funded university labs.
	"applied research laboratory",
	"applied physics laboratory",
	"novateur research",

	// Defense tech
	"anduril",
	"palantir",
	"shield ai",
	"epirus",
	"rebellion defense",
	"applied intuition",
	"saronic",
	"castelion",
	"vannevar labs",
	"primer ai",

	// Borderline — significant defense revenue but primarily commercial.
	// Comment these out to let them through.
	"scale ai",
}

// ---------------------------------------------------------------------------
// Spam / low-signal posters (inherited, plus common intern-board noise)
// ---------------------------------------------------------------------------

var blockedPosters = []string{
	"dataannotation",
	"jobs via dice",
	"jobot",
	"actalent",
	"insight global",
	"teksystems",
	"robert half",
	"cybercoders",
	"motion recruitment",
	"revature",
	"smartdept",
	"talentify",
	"get it recruit",
	"lensa",
	"clearancejobs",
	"crossover",
	// Aggregators that repost other companies' listings under their own name,
	// so the real employer is never visible on the card.
	"jobright",
	"speedyapply",
	"hiring cafe",
	"simplify jobs",
	"joblist",
	"ziprecruiter",
}

// ---------------------------------------------------------------------------
// Matching helpers
// ---------------------------------------------------------------------------

var nonAlphaNumRe = regexp.MustCompile(`[^a-z0-9]+`)

// normalize lowercases and reduces punctuation to single spaces so that
// "L3Harris Technologies, Inc." and "l3 harris" compare equal.
func normalize(s string) string {
	return strings.TrimSpace(nonAlphaNumRe.ReplaceAllString(strings.ToLower(s), " "))
}

// matchesAny reports whether the normalized company name matches any entry.
// Short single-word entries ("sig", "drw", "hrt", "rtx") match whole tokens
// only; everything else matches as a substring.
func matchesAny(normalizedCompany string, entries []string) (string, bool) {
	tokens := strings.Fields(normalizedCompany)
	tokenSet := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		tokenSet[t] = true
	}

	for _, entry := range entries {
		if len(entry) <= 4 && !strings.Contains(entry, " ") {
			if tokenSet[entry] {
				return entry, true
			}
			continue
		}
		if strings.Contains(normalizedCompany, entry) {
			return entry, true
		}
	}
	return "", false
}

// rejectionReason returns a human-readable reason the job was dropped, or ""
// if the job should be kept. Ordered cheapest-and-most-decisive first.
func rejectionReason(job Job) string {
	title := strings.ToLower(job.Title)
	company := normalize(job.Company)

	if !internRe.MatchString(title) {
		return "not an internship title"
	}
	if !engineeringRe.MatchString(title) {
		return "not a software engineering title"
	}
	// A non-software discipline only disqualifies when nothing in the title
	// says software, so "Imaging Software Engineer Intern" survives but
	// "Electrical Engineering Intern" does not.
	if m := otherDisciplineRe.FindString(title); m != "" && !strongSoftwareRe.MatchString(title) {
		return "different engineering discipline: " + m
	}
	if m := androidRe.FindString(title); m != "" {
		return "android-specific: " + m
	}
	if m := wrongTermRe.FindString(title); m != "" {
		return "wrong term/year: " + m
	}
	if m := wrongLevelRe.FindString(title); m != "" {
		return "wrong level: " + m
	}
	if m := quantKeywordRe.FindString(title); m != "" {
		return "quant keyword: " + m
	}
	if m := defenseKeywordRe.FindString(title); m != "" {
		return "defense keyword: " + m
	}
	if entry, ok := matchesAny(company, quantFirms); ok {
		return "quant firm: " + entry
	}
	if entry, ok := matchesAny(company, defenseFirms); ok {
		return "defense firm: " + entry
	}
	if entry, ok := matchesAny(company, blockedPosters); ok {
		return "blocked poster: " + entry
	}
	return ""
}
