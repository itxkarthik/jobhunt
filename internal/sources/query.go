package sources

import (
	"net/url"
	"strings"

	"github.com/itxkarthik/jobhunt/internal/model"
)

// AreaTerm is a short human search phrase per area, used for server-side
// queries and browser links.
var AreaTerm = map[model.Area]string{
	model.AreaBackend:    "backend",
	model.AreaFrontend:   "frontend",
	model.AreaFullStack:  "full stack",
	model.AreaMobile:     "mobile developer",
	model.AreaMLAI:       "machine learning",
	model.AreaDataEng:    "data engineer",
	model.AreaDataSci:    "data scientist",
	model.AreaDevOps:     "devops",
	model.AreaSystems:    "systems engineer",
	model.AreaHPC:        "gpu",
	model.AreaSecurity:   "security engineer",
	model.AreaEmbedded:   "embedded",
	model.AreaQA:         "qa engineer",
	model.AreaResearch:   "research engineer",
	model.AreaBlockchain: "blockchain",
	model.AreaGameDev:    "game developer",
}

// Topic is the main search phrase: keywords if given, else the single
// requested area, else "" (several areas are filtered locally instead).
func Topic(f model.Filters) string {
	if len(f.Keywords) > 0 {
		return strings.Join(f.Keywords, " ")
	}
	if len(f.Areas) == 1 {
		return AreaTerm[f.Areas[0]]
	}
	return ""
}

// TypeWord is the job-type phrase to add to free-text searches.
func TypeWord(f model.Filters) string {
	switch {
	case f.WantsType(model.TypeInternship):
		return "intern"
	case f.WantsType(model.TypeNewGrad):
		return "new grad"
	case len(f.Experience) == 1 && f.Experience[0] == model.ExpEntry:
		return "junior"
	}
	return ""
}

// Phrase combines topic and type for sites that take one search box,
// defaulting to "software engineer".
func Phrase(f model.Filters) string {
	t := Topic(f)
	if t == "" {
		t = "software engineer"
	}
	if w := TypeWord(f); w != "" {
		t += " " + w
	}
	return t
}

// CountryName returns a display name for a filter country code.
func CountryName(code string) string {
	switch code {
	case "IN":
		return "India"
	case "US":
		return "United States"
	case "JP":
		return "Japan"
	case "GB":
		return "United Kingdom"
	case "DE":
		return "Germany"
	case "CA":
		return "Canada"
	case "SG":
		return "Singapore"
	case "NL":
		return "Netherlands"
	case "AU":
		return "Australia"
	case "AE":
		return "United Arab Emirates"
	case model.RemoteGlobal:
		return "Remote"
	}
	return code
}

// CloneValues deep-copies query values, so a per-page copy can be changed
// without touching the shared base query.
func CloneValues(v url.Values) url.Values {
	c := url.Values{}
	for k, vs := range v {
		c[k] = append([]string(nil), vs...)
	}
	return c
}
