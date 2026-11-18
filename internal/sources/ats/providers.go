package ats

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
)

// --- Greenhouse: https://developers.greenhouse.io/job-board.html -----------

type greenhouse struct{ base string }

type ghResponse struct {
	Jobs []struct {
		ID          int64  `json:"id"`
		Title       string `json:"title"`
		AbsoluteURL string `json:"absolute_url"`
		Location    struct {
			Name string `json:"name"`
		} `json:"location"`
		UpdatedAt      string `json:"updated_at"`
		FirstPublished string `json:"first_published"`
		Content        string `json:"content"`
		Departments    []struct {
			Name string `json:"name"`
		} `json:"departments"`
		Offices []struct {
			Name string `json:"name"`
		} `json:"offices"`
	} `json:"jobs"`
}

func (g greenhouse) fetchBoard(ctx context.Context, cl *sources.Client, c Company) ([]model.Job, error) {
	var resp ghResponse
	if err := cl.GetJSON(ctx, fmt.Sprintf("%s/%s/jobs?content=true", g.base, c.Slug), nil, &resp); err != nil {
		return nil, err
	}
	return g.parse(resp, c), nil
}

func (greenhouse) parse(resp ghResponse, c Company) []model.Job {
	jobs := make([]model.Job, 0, len(resp.Jobs))
	for _, r := range resp.Jobs {
		countries, city := locate(r.Location.Name)
		if len(countries) == 0 {
			var offices []string
			for _, o := range r.Offices {
				offices = append(offices, o.Name)
			}
			countries, city = locate(strings.Join(offices, ", "))
		}
		posted := sources.ParseTime(r.FirstPublished)
		if posted.IsZero() {
			posted = sources.ParseTime(r.UpdatedAt)
		}
		j := model.Job{
			ID:          fmt.Sprintf("greenhouse:%s:%d", c.Slug, r.ID),
			Source:      "greenhouse",
			Title:       sources.CleanText(r.Title),
			Company:     c.Name,
			URL:         r.AbsoluteURL,
			Countries:   countries,
			City:        city,
			Location:    r.Location.Name,
			WorkMode:    model.ModeUnknown,
			JobType:     model.TypeUnknown,
			Experience:  model.ExpUnknown,
			PostedAt:    posted,
			Description: sources.Truncate(sources.StripHTML(r.Content), maxDesc),
		}
		for _, d := range r.Departments {
			j.Tags = append(j.Tags, d.Name)
		}
		jobs = append(jobs, j)
	}
	return jobs
}

func (greenhouse) boardURL(c Company) string { return "https://boards.greenhouse.io/" + c.Slug }

// --- Lever: https://github.com/lever/postings-api ---------------------------

type lever struct{ base string }

type leverPosting struct {
	ID         string `json:"id"`
	Text       string `json:"text"`
	HostedURL  string `json:"hostedUrl"`
	CreatedAt  int64  `json:"createdAt"` // ms
	Country    string `json:"country"`
	Workplace  string `json:"workplaceType"`
	Categories struct {
		Commitment   string   `json:"commitment"`
		Location     string   `json:"location"`
		Team         string   `json:"team"`
		Department   string   `json:"department"`
		AllLocations []string `json:"allLocations"`
	} `json:"categories"`
	DescriptionPlain string `json:"descriptionPlain"`
	Lists            []struct {
		Text    string `json:"text"`
		Content string `json:"content"`
	} `json:"lists"`
	AdditionalPlain string `json:"additionalPlain"`
	SalaryRange     *struct {
		Min      float64 `json:"min"`
		Max      float64 `json:"max"`
		Currency string  `json:"currency"`
		Interval string  `json:"interval"`
	} `json:"salaryRange"`
}

func (l lever) fetchBoard(ctx context.Context, cl *sources.Client, c Company) ([]model.Job, error) {
	var resp []leverPosting
	if err := cl.GetJSON(ctx, fmt.Sprintf("%s/%s?mode=json", l.base, c.Slug), nil, &resp); err != nil {
		return nil, err
	}
	return l.parse(resp, c), nil
}

func (lever) parse(resp []leverPosting, c Company) []model.Job {
	jobs := make([]model.Job, 0, len(resp))
	for _, r := range resp {
		locs := r.Categories.AllLocations
		if len(locs) == 0 && r.Categories.Location != "" {
			locs = []string{r.Categories.Location}
		}
		countries, city := locate(locs...)
		if r.Country != "" && !slices.Contains(countries, strings.ToUpper(r.Country)) {
			countries = append([]string{strings.ToUpper(r.Country)}, countries...)
		}
		var desc strings.Builder
		desc.WriteString(r.DescriptionPlain)
		for _, l := range r.Lists {
			fmt.Fprintf(&desc, "\n\n%s\n%s", l.Text, sources.StripHTML(l.Content))
		}
		if r.AdditionalPlain != "" {
			desc.WriteString("\n\n" + r.AdditionalPlain)
		}
		j := model.Job{
			ID:          "lever:" + r.ID,
			Source:      "lever",
			Title:       sources.CleanText(r.Text),
			Company:     c.Name,
			URL:         r.HostedURL,
			Countries:   countries,
			City:        city,
			Location:    strings.Join(locs, "; "),
			WorkMode:    leverMode(r.Workplace),
			JobType:     commitment(r.Categories.Commitment),
			Experience:  model.ExpUnknown,
			PostedAt:    time.UnixMilli(r.CreatedAt).UTC(),
			Description: sources.Truncate(strings.TrimSpace(desc.String()), maxDesc),
		}
		for _, t := range []string{r.Categories.Department, r.Categories.Team} {
			if t != "" {
				j.Tags = append(j.Tags, t)
			}
		}
		if sr := r.SalaryRange; sr != nil && strings.Contains(sr.Interval, "year") {
			j.SalaryMin, j.SalaryMax, j.Currency = sources.Float(sr.Min), sources.Float(sr.Max), sr.Currency
		}
		jobs = append(jobs, j)
	}
	return jobs
}

func leverMode(s string) model.WorkMode {
	switch s {
	case "remote":
		return model.ModeRemote
	case "hybrid":
		return model.ModeHybrid
	case "onsite", "on-site":
		return model.ModeOnsite
	}
	return model.ModeUnknown
}

func commitment(s string) model.JobType {
	l := strings.ToLower(s)
	switch {
	case strings.Contains(l, "intern"):
		return model.TypeInternship
	case strings.Contains(l, "part"):
		return model.TypePartTime
	case strings.Contains(l, "contract"), strings.Contains(l, "temporary"), strings.Contains(l, "fixed term"):
		return model.TypeContract
	case strings.Contains(l, "full"), strings.Contains(l, "permanent"), strings.Contains(l, "regular"):
		return model.TypeFullTime
	}
	return model.TypeUnknown
}

func (lever) boardURL(c Company) string { return "https://jobs.lever.co/" + c.Slug }

// --- Ashby: https://developers.ashbyhq.com/docs/public-job-posting-api ------

type ashby struct{ base string }

type ashbyAddress struct {
	PostalAddress struct {
		AddressCountry  string `json:"addressCountry"`
		AddressLocality string `json:"addressLocality"`
	} `json:"postalAddress"`
}

type ashbyResponse struct {
	Jobs []struct {
		ID                 string `json:"id"`
		Title              string `json:"title"`
		Department         string `json:"department"`
		Team               string `json:"team"`
		EmploymentType     string `json:"employmentType"`
		Location           string `json:"location"`
		SecondaryLocations []struct {
			Location string       `json:"location"`
			Address  ashbyAddress `json:"address"`
		} `json:"secondaryLocations"`
		PublishedAt      string        `json:"publishedAt"`
		IsListed         bool          `json:"isListed"`
		IsRemote         bool          `json:"isRemote"`
		WorkplaceType    string        `json:"workplaceType"`
		Address          *ashbyAddress `json:"address"`
		JobURL           string        `json:"jobUrl"`
		DescriptionPlain string        `json:"descriptionPlain"`
		Compensation     struct {
			SummaryComponents []struct {
				CompensationType string  `json:"compensationType"`
				Interval         string  `json:"interval"`
				CurrencyCode     string  `json:"currencyCode"`
				MinValue         float64 `json:"minValue"`
				MaxValue         float64 `json:"maxValue"`
			} `json:"summaryComponents"`
		} `json:"compensation"`
	} `json:"jobs"`
}

func (a ashby) fetchBoard(ctx context.Context, cl *sources.Client, c Company) ([]model.Job, error) {
	var resp ashbyResponse
	if err := cl.GetJSON(ctx, fmt.Sprintf("%s/%s?includeCompensation=true", a.base, c.Slug), nil, &resp); err != nil {
		return nil, err
	}
	return a.parse(resp, c), nil
}

func (ashby) parse(resp ashbyResponse, c Company) []model.Job {
	jobs := make([]model.Job, 0, len(resp.Jobs))
	for _, r := range resp.Jobs {
		if !r.IsListed {
			continue
		}
		texts := []string{r.Location}
		var countries []string
		addCountry := func(name string) {
			if code, ok := model.NormalizeCountry(name); ok && name != "" && !slices.Contains(countries, code) {
				countries = append(countries, code)
			}
		}
		if r.Address != nil {
			addCountry(r.Address.PostalAddress.AddressCountry)
		}
		for _, sl := range r.SecondaryLocations {
			texts = append(texts, sl.Location)
			addCountry(sl.Address.PostalAddress.AddressCountry)
		}
		parsed, city := locate(texts...)
		for _, p := range parsed {
			if !slices.Contains(countries, p) {
				countries = append(countries, p)
			}
		}
		j := model.Job{
			ID:          "ashby:" + r.ID,
			Source:      "ashby",
			Title:       sources.CleanText(r.Title),
			Company:     c.Name,
			URL:         r.JobURL,
			Countries:   countries,
			City:        city,
			Location:    strings.Join(texts, "; "),
			WorkMode:    ashbyMode(r.WorkplaceType, r.IsRemote),
			JobType:     ashbyType(r.EmploymentType),
			Experience:  model.ExpUnknown,
			PostedAt:    sources.ParseTime(r.PublishedAt),
			Description: sources.Truncate(r.DescriptionPlain, maxDesc),
		}
		for _, t := range []string{r.Department, r.Team} {
			if t != "" && !slices.Contains(j.Tags, t) {
				j.Tags = append(j.Tags, t)
			}
		}
		for _, sc := range r.Compensation.SummaryComponents {
			if sc.CompensationType == "Salary" && sc.Interval == "1 YEAR" {
				j.SalaryMin, j.SalaryMax, j.Currency = sources.Float(sc.MinValue), sources.Float(sc.MaxValue), sc.CurrencyCode
				break
			}
		}
		jobs = append(jobs, j)
	}
	return jobs
}

func ashbyMode(w string, isRemote bool) model.WorkMode {
	switch w {
	case "Remote":
		return model.ModeRemote
	case "Hybrid":
		return model.ModeHybrid
	case "OnSite":
		return model.ModeOnsite
	}
	if isRemote {
		return model.ModeRemote
	}
	return model.ModeUnknown
}

func ashbyType(s string) model.JobType {
	switch s {
	case "FullTime":
		return model.TypeFullTime
	case "PartTime":
		return model.TypePartTime
	case "Intern":
		return model.TypeInternship
	case "Contract", "Temporary":
		return model.TypeContract
	}
	return model.TypeUnknown
}

func (ashby) boardURL(c Company) string { return "https://jobs.ashbyhq.com/" + c.Slug }

// --- Workable: public careers widget API -----------------------------------
// https://apply.workable.com/api/v1/widget/accounts/{slug}?details=true

type workable struct{ base string }

type workableResponse struct {
	Jobs []struct {
		Title          string `json:"title"`
		Shortcode      string `json:"shortcode"`
		EmploymentType string `json:"employment_type"`
		Telecommuting  bool   `json:"telecommuting"`
		Department     string `json:"department"`
		URL            string `json:"url"`
		PublishedOn    string `json:"published_on"`
		Country        string `json:"country"`
		City           string `json:"city"`
		Experience     string `json:"experience"`
		Function       string `json:"function"`
		Description    string `json:"description"`
		Locations      []struct {
			Country     string `json:"country"`
			CountryCode string `json:"countryCode"`
			City        string `json:"city"`
		} `json:"locations"`
	} `json:"jobs"`
}

func (w workable) fetchBoard(ctx context.Context, cl *sources.Client, c Company) ([]model.Job, error) {
	var resp workableResponse
	if err := cl.GetJSON(ctx, fmt.Sprintf("%s/%s?details=true", w.base, c.Slug), nil, &resp); err != nil {
		return nil, err
	}
	return w.parse(resp, c), nil
}

func (workable) parse(resp workableResponse, c Company) []model.Job {
	jobs := make([]model.Job, 0, len(resp.Jobs))
	for _, r := range resp.Jobs {
		var countries, texts []string
		city := r.City
		for _, l := range r.Locations {
			if code := strings.ToUpper(l.CountryCode); len(code) == 2 && !slices.Contains(countries, code) {
				countries = append(countries, code)
			}
			if city == "" {
				city = l.City
			}
			texts = append(texts, strings.TrimSpace(l.City+", "+l.Country))
		}
		if len(countries) == 0 {
			countries, city = locate(r.City + ", " + r.Country)
		}
		mode := model.ModeUnknown
		if r.Telecommuting {
			mode = model.ModeRemote
		}
		j := model.Job{
			ID:          "workable:" + c.Slug + ":" + r.Shortcode,
			Source:      "workable",
			Title:       sources.CleanText(r.Title),
			Company:     c.Name,
			URL:         r.URL,
			Countries:   countries,
			City:        city,
			Location:    strings.Trim(strings.Join(texts, "; "), ", "),
			WorkMode:    mode,
			JobType:     commitment(r.EmploymentType),
			Experience:  workableLevel(r.Experience),
			PostedAt:    sources.ParseTime(r.PublishedOn),
			Description: sources.Truncate(sources.StripHTML(r.Description), maxDesc),
		}
		for _, t := range []string{r.Department, r.Function} {
			if t != "" && !slices.Contains(j.Tags, t) {
				j.Tags = append(j.Tags, t)
			}
		}
		jobs = append(jobs, j)
	}
	return jobs
}

func workableLevel(s string) model.Experience {
	switch s {
	case "Internship":
		return model.ExpIntern
	case "Entry level", "Associate":
		return model.ExpEntry
	case "Mid-Senior level":
		return model.ExpMid
	case "Director", "Executive":
		return model.ExpSenior
	}
	return model.ExpUnknown
}

func (workable) boardURL(c Company) string { return "https://apply.workable.com/" + c.Slug }

// --- Recruitee: public careers site API -------------------------------------
// https://{slug}.recruitee.com/api/offers/

type recruitee struct{ base string } // "" means the real per-company subdomain

type recruiteeResponse struct {
	Offers []struct {
		ID                 int64  `json:"id"`
		Title              string `json:"title"`
		CareersURL         string `json:"careers_url"`
		Location           string `json:"location"`
		City               string `json:"city"`
		CountryCode        string `json:"country_code"`
		Remote             bool   `json:"remote"`
		Hybrid             bool   `json:"hybrid"`
		OnSite             bool   `json:"on_site"`
		EmploymentTypeCode string `json:"employment_type_code"`
		ExperienceCode     string `json:"experience_code"`
		Department         string `json:"department"`
		PublishedAt        string `json:"published_at"`
		Description        string `json:"description"`
		Requirements       string `json:"requirements"`
		Locations          []struct {
			CountryCode string `json:"country_code"`
			City        string `json:"city"`
		} `json:"locations"`
		Salary struct {
			Min      sources.FlexFloat `json:"min"`
			Max      sources.FlexFloat `json:"max"`
			Period   string            `json:"period"`
			Currency string            `json:"currency"`
		} `json:"salary"`
	} `json:"offers"`
}

func (r recruitee) offersURL(slug string) string {
	if r.base != "" {
		return r.base + "/" + slug + "/api/offers/"
	}
	return "https://" + slug + ".recruitee.com/api/offers/"
}

func (r recruitee) fetchBoard(ctx context.Context, cl *sources.Client, c Company) ([]model.Job, error) {
	var resp recruiteeResponse
	if err := cl.GetJSON(ctx, r.offersURL(c.Slug), nil, &resp); err != nil {
		return nil, err
	}
	return r.parse(resp, c), nil
}

func (recruitee) parse(resp recruiteeResponse, c Company) []model.Job {
	jobs := make([]model.Job, 0, len(resp.Offers))
	for _, o := range resp.Offers {
		var countries []string
		add := func(cc string) {
			if cc = strings.ToUpper(cc); len(cc) == 2 && !slices.Contains(countries, cc) {
				countries = append(countries, cc)
			}
		}
		add(o.CountryCode)
		for _, l := range o.Locations {
			add(l.CountryCode)
		}
		city := o.City
		if len(countries) == 0 {
			countries, city = locate(o.Location)
		}
		mode := model.ModeUnknown
		switch {
		case o.Remote:
			mode = model.ModeRemote
		case o.Hybrid:
			mode = model.ModeHybrid
		case o.OnSite:
			mode = model.ModeOnsite
		}
		j := model.Job{
			ID:          fmt.Sprintf("recruitee:%s:%d", c.Slug, o.ID),
			Source:      "recruitee",
			Title:       sources.CleanText(o.Title),
			Company:     c.Name,
			URL:         o.CareersURL,
			Countries:   countries,
			City:        city,
			Location:    o.Location,
			WorkMode:    mode,
			JobType:     commitment(strings.ReplaceAll(o.EmploymentTypeCode, "_", " ")),
			Experience:  recruiteeLevel(o.ExperienceCode),
			PostedAt:    recruiteeTime(o.PublishedAt),
			Description: sources.Truncate(sources.StripHTML(o.Description+"\n"+o.Requirements), maxDesc),
		}
		if o.Department != "" {
			j.Tags = []string{o.Department}
		}
		if o.Salary.Period == "year" && (o.Salary.Min > 0 || o.Salary.Max > 0) {
			j.SalaryMin, j.SalaryMax = sources.Float(float64(o.Salary.Min)), sources.Float(float64(o.Salary.Max))
			j.Currency = o.Salary.Currency
		}
		jobs = append(jobs, j)
	}
	return jobs
}

func recruiteeLevel(s string) model.Experience {
	switch s {
	case "student", "internship":
		return model.ExpIntern
	case "entry_level", "junior":
		return model.ExpEntry
	case "mid_level", "experienced":
		return model.ExpMid
	case "senior", "manager", "executive":
		return model.ExpSenior
	}
	return model.ExpUnknown
}

// recruiteeTime parses "2026-11-11 13:21:40 UTC".
func recruiteeTime(s string) time.Time {
	if t, err := time.Parse("2006-01-02 15:04:05 MST", s); err == nil {
		return t.UTC()
	}
	return sources.ParseTime(s)
}

func (r recruitee) boardURL(c Company) string {
	return strings.TrimSuffix(r.offersURL(c.Slug), "api/offers/")
}
