package links

// Startup directories: where to look for small funded companies by hand.
//
// None of these is fetched. Terms review for #22:
//
//   - Y Combinator company directory (ycombinator.com/companies): the
//     site's terms forbid scraping and automated collection (checked
//     2026-11-19). The unofficial yc-oss mirror is built from YC's search
//     index and carries no licence, so it isn't used either.
//   - Inc42 and Entrackr (Indian funding news): their terms forbid
//     automated collection of their content, RSS feeds included (checked
//     2026-11-19).
//   - Crunchbase, Tracxn, Wellfound and LinkedIn: excluded by the
//     project's rules (AGENTS.md) without further review; their terms
//     restrict automated access and the useful data is paid.
//
// What jobhunt does fetch instead: SEC Form D filings (sources/edgar), the
// MCA company master data API (sources/mca) and Hacker News "Who is
// hiring?" posts (sources/hn), each with its own terms note.

import "slices"

// Directories returns startup-directory links for the given countries.
func Directories(countries []string) []Link {
	india := len(countries) == 0 || slices.Contains(countries, "IN")
	out := []Link{
		{"YC company directory", "https://www.ycombinator.com/companies"},
		{"Wellfound startups", "https://wellfound.com/startups"},
		{"Crunchbase (recent funding)", "https://www.crunchbase.com/discover/funding_rounds"},
		{"Tracxn", "https://tracxn.com/"},
	}
	if india {
		out = append(out,
			Link{"Inc42 funding news", "https://inc42.com/buzz/funding-galore/"},
			Link{"Entrackr funding news", "https://entrackr.com/category/funding-and-acquisitions/"},
		)
	}
	return out
}
