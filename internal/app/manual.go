package app

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/itxkarthik/jobhunt/internal/dedup"
	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
)

// ManualSource is the source of jobs the user tracks by hand.
const ManualSource = "manual"

// ManualInput is a posting the user found outside jobhunt's sources, such
// as on LinkedIn or Internshala, with details they (or an agent, from
// search results or text the user pasted) typed in. jobhunt never
// requests its URL: those sites forbid automated access.
type ManualInput struct {
	URL      string `json:"url"`
	Title    string `json:"title"`
	Company  string `json:"company"`
	Location string `json:"location,omitempty"`
}

const (
	maxManualURL  = 2000
	maxManualText = 300
)

// ManualJob builds the stored record for a manually tracked posting. The
// text is cleaned like any fetched job's, and the URL must be http(s).
func ManualJob(in ManualInput) (model.Job, error) {
	j := model.Job{
		Source: ManualSource, Sources: []string{ManualSource},
		URL:   strings.TrimSpace(in.URL),
		Title: strings.TrimSpace(in.Title), Company: strings.TrimSpace(in.Company),
		Location:   strings.TrimSpace(in.Location),
		WorkMode:   model.ModeUnknown,
		JobType:    model.TypeUnknown,
		Experience: model.ExpUnknown,
	}
	if len(j.URL) > maxManualURL {
		return j, fmt.Errorf("url is longer than %d characters", maxManualURL)
	}
	for name, v := range map[string]string{"title": j.Title, "company": j.Company, "location": j.Location} {
		if utf8.RuneCountInString(v) > maxManualText {
			return j, fmt.Errorf("%s is longer than %d characters", name, maxManualText)
		}
	}
	sources.SanitizeJob(&j)
	switch {
	case j.URL == "":
		return j, errors.New("url must be an http(s) link")
	case j.Title == "":
		return j, errors.New("title is required")
	case j.Company == "":
		return j, errors.New("company is required")
	}
	j.Countries, j.City = model.ParseLocation(j.Location)
	j.ID = ManualSource + ":" + j.URL
	j.Key = dedup.Key(&j)
	return j, nil
}
