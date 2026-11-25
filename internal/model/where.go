package model

import (
	"slices"
	"strconv"
	"strings"
)

// placeLabels names the codes ShortLocation shows; others show as the code.
var placeLabels = map[string]string{
	"IN": "India", "US": "USA", "JP": "Japan", "GB": "UK", "DE": "Germany",
	"CA": "Canada", "SG": "Singapore", "NL": "Netherlands", "AU": "Australia", "AE": "UAE",
	RemoteGlobal: "Worldwide",
}

// placeLabel names a country, region or RemoteGlobal code for display;
// format.js placeLabel must match it.
func placeLabel(code string) string {
	if l, ok := placeLabels[code]; ok {
		return l
	}
	if r, ok := strings.CutPrefix(code, "region:"); ok {
		return strings.ToUpper(r)
	}
	return code
}

// FullLocation is a job's location in full: its city, else its Location
// text, else its country codes.
func FullLocation(j *Job) string {
	switch {
	case j.City != "":
		return j.City
	case j.Location != "":
		return j.Location
	}
	return strings.Join(j.Countries, ",")
}

// ShortLocation is a job's location for a narrow table column. A job open in
// several countries reads as one of them plus a count, "India +17", naming
// the first of want (the searched countries) that it lists. Its raw Location
// text is usually a long alphabetical list ("Afghanistan, Armenia, …") that a
// column would cut before the country the user searched for. Other jobs show
// their city, then their Location text, then their countries' names.
// internal/web/static/js/format.js shortLocation must match it.
func ShortLocation(j *Job, want []string) string {
	if j.City != "" {
		return j.City
	}
	if len(j.Countries) > 1 {
		pick := j.Countries[0]
		if i := slices.IndexFunc(want, func(w string) bool { return slices.Contains(j.Countries, w) }); i >= 0 {
			pick = want[i]
		}
		return placeLabel(pick) + " +" + strconv.Itoa(len(j.Countries)-1)
	}
	if j.Location != "" {
		return j.Location
	}
	labels := make([]string, len(j.Countries))
	for i, c := range j.Countries {
		labels[i] = placeLabel(c)
	}
	return strings.Join(labels, ", ")
}
