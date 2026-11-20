package app

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/itxkarthik/jobhunt/internal/company"
	"github.com/itxkarthik/jobhunt/internal/feed"
	"github.com/itxkarthik/jobhunt/internal/model"
)

// Digest is what's new since the last digest: matching jobs, and companies
// that were added or changed. It's built for reading, not as a contract;
// automations should use `jobhunt feed`.
type Digest struct {
	At time.Time `json:"at"`
	// First is the first digest for this cursor: it shows the best of what
	// matches now and records the rest as seen, so later ones are short.
	First         bool            `json:"first,omitempty"`
	Jobs          []DigestJob     `json:"jobs"`
	MoreJobs      int             `json:"more_jobs,omitempty"` // new, but over the limit
	Companies     []DigestCompany `json:"companies"`
	MoreCompanies int             `json:"more_companies,omitempty"`
}

// DigestJob is one job in a digest.
type DigestJob struct {
	Key      string    `json:"key"`
	Title    string    `json:"title"`
	Company  string    `json:"company"`
	Location string    `json:"location,omitempty"`
	URL      string    `json:"url"`
	Score    float64   `json:"score"`
	PostedAt time.Time `json:"posted_at,omitzero"`
}

// DigestCompany is one company in a digest.
type DigestCompany struct {
	Key       string          `json:"key"`
	Name      string          `json:"name"`
	Change    feed.Change     `json:"change"`
	Stage     model.Stage     `json:"stage"`
	Headcount model.Headcount `json:"headcount"`
	Where     string          `json:"where,omitempty"`
	Website   string          `json:"website,omitempty"`
	Round     *model.Round    `json:"round,omitempty"` // the latest, with its source
	// Draft is an outreach email to edit and send yourself, present when a
	// contact published an email address. jobhunt never sends it.
	Draft *company.Draft `json:"draft,omitempty"`
}

// Empty reports whether there's nothing to send.
func (d *Digest) Empty() bool {
	return len(d.Jobs) == 0 && len(d.Companies) == 0
}

// DigestOptions shape a digest.
type DigestOptions struct {
	MinScore     float64
	MaxJobs      int // 0: 20
	MaxCompanies int // 0: 10
	Stack        []string
	Now          time.Time
}

// BuildDigest picks the jobs (sorted best first) and companies the cursor
// hasn't received. delivered holds job keys and company versions
// (feed.CompanyVersion); first marks a cursor with no history. It returns
// the digest and every key to record as delivered once it's sent: the
// shown items plus those over the limits, so nothing repeats.
func BuildDigest(jobs []model.Job, companies []model.Company, delivered map[string]bool, first bool, o DigestOptions) (Digest, []string) {
	if o.MaxJobs <= 0 {
		o.MaxJobs = 20
	}
	if o.MaxCompanies <= 0 {
		o.MaxCompanies = 10
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	d := Digest{At: o.Now.UTC(), First: first, Jobs: []DigestJob{}, Companies: []DigestCompany{}}
	var keys []string
	for _, j := range jobs {
		if delivered[j.Key] || j.Score < o.MinScore {
			continue
		}
		keys = append(keys, j.Key)
		if len(d.Jobs) == o.MaxJobs {
			d.MoreJobs++
			continue
		}
		d.Jobs = append(d.Jobs, DigestJob{
			Key: j.Key, Title: j.Title, Company: j.Company, Location: jobPlace(&j), URL: j.URL,
			Score: j.Score, PostedAt: j.PostedAt.UTC(),
		})
	}

	// Newest changes first: on a first digest, that's what's worth a look.
	cs := slices.Clone(companies)
	slices.SortStableFunc(cs, func(a, b model.Company) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	for i := range cs {
		c := &cs[i]
		v := feed.CompanyVersion(c)
		if delivered[v] {
			continue
		}
		keys = append(keys, v)
		if len(d.Companies) == o.MaxCompanies {
			d.MoreCompanies++
			continue
		}
		dc := DigestCompany{
			Key: c.Key, Name: c.Name, Change: feed.ChangeOf(c), Stage: c.Stage, Headcount: c.Headcount,
			Where: companyPlace(c), Website: c.Website, Round: c.LatestRound(),
		}
		if _, ok := company.BestContact(c); ok {
			dr := company.NewDraft(c, nil, company.DraftOptions{Stack: o.Stack, Now: o.Now})
			dc.Draft = &dr
		}
		d.Companies = append(d.Companies, dc)
	}
	return d, keys
}

func jobPlace(j *model.Job) string {
	if j.Location != "" {
		return j.Location
	}
	return strings.Join(j.Countries, ", ")
}

func companyPlace(c *model.Company) string {
	p := strings.Join(slices.DeleteFunc([]string{c.City, c.Country}, func(s string) bool { return s == "" }), ", ")
	if c.Remote {
		if p == "" {
			return "remote"
		}
		return p + " · remote"
	}
	return p
}

// Markdown renders the digest for a chat message or a file.
func (d *Digest) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# jobhunt digest, %s\n", d.At.Local().Format("Mon 2 Jan 2006"))
	if d.First {
		b.WriteString("\nFirst digest: the best of what matches now. From here on, only what's new.\n")
	}
	if len(d.Jobs) > 0 {
		fmt.Fprintf(&b, "\n## %s\n\n", plural(len(d.Jobs)+d.MoreJobs, "new job", "new jobs"))
		for _, j := range d.Jobs {
			fmt.Fprintf(&b, "- [%s](%s) at %s", mdText(j.Title), mdURL(j.URL), mdText(j.Company))
			if j.Location != "" {
				fmt.Fprintf(&b, ", %s", mdText(j.Location))
			}
			fmt.Fprintf(&b, " (score %.0f)\n", j.Score)
		}
		if d.MoreJobs > 0 {
			fmt.Fprintf(&b, "- and %d more: `jobhunt search --profile`\n", d.MoreJobs)
		}
	}
	if len(d.Companies) > 0 {
		fmt.Fprintf(&b, "\n## %s\n\n", plural(len(d.Companies)+d.MoreCompanies, "company", "companies"))
		for _, c := range d.Companies {
			fmt.Fprintf(&b, "- **%s** (%s)", mdText(c.Name), c.Change)
			var facts []string
			if c.Stage != "" && c.Stage != model.StageUnknown {
				facts = append(facts, string(c.Stage))
			}
			if r := c.Round; r != nil && !r.Date.IsZero() {
				facts = append(facts, fmt.Sprintf("raised %s, [source](%s)", r.Date.Format("Jan 2006"), mdURL(r.SourceURL)))
			}
			if c.Headcount != "" && c.Headcount != model.HeadcountUnknown {
				facts = append(facts, string(c.Headcount)+" people")
			}
			if c.Where != "" {
				facts = append(facts, mdText(c.Where))
			}
			if c.Website != "" {
				facts = append(facts, mdURL(c.Website))
			}
			if len(facts) > 0 {
				b.WriteString(": " + strings.Join(facts, " · "))
			}
			if c.Draft != nil && c.Draft.Mailto != "" {
				fmt.Fprintf(&b, " · [draft email](%s)", c.Draft.Mailto)
			}
			b.WriteString("\n")
		}
		if d.MoreCompanies > 0 {
			fmt.Fprintf(&b, "- and %d more: `jobhunt companies --profile`\n", d.MoreCompanies)
		}
	}
	return b.String()
}

// Notification is a short title and body for a desktop notification.
func (d *Digest) Notification() (title, body string) {
	var parts []string
	if n := len(d.Jobs) + d.MoreJobs; n > 0 {
		parts = append(parts, plural(n, "new job", "new jobs"))
	}
	if n := len(d.Companies) + d.MoreCompanies; n > 0 {
		parts = append(parts, plural(n, "company", "companies"))
	}
	var lines []string
	for _, j := range d.Jobs[:min(3, len(d.Jobs))] {
		lines = append(lines, fmt.Sprintf("%s at %s", j.Title, j.Company))
	}
	for _, c := range d.Companies[:min(3-len(lines), len(d.Companies))] {
		lines = append(lines, fmt.Sprintf("%s (%s)", c.Name, c.Change))
	}
	return "jobhunt: " + strings.Join(parts, ", "), strings.Join(lines, "\n")
}

// mdURL escapes the characters that would end a Markdown link target.
func mdURL(u string) string {
	return strings.NewReplacer("(", "%28", ")", "%29", " ", "%20", "<", "%3C", ">", "%3E").Replace(u)
}

// mdText keeps fetched text from breaking out of Markdown link or bold
// syntax.
func mdText(s string) string {
	return strings.NewReplacer("[", "(", "]", ")", "*", "", "`", "'", "\n", " ", "<", "‹", ">", "›").Replace(s)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
