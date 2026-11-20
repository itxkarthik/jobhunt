// Package export writes jobs as CSV, JSON, or Markdown.
package export

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
)

// Formats lists supported formats.
var Formats = []string{"csv", "json", "md"}

// Row is a job plus its tracker status (may be empty).
type Row struct {
	Job    model.Job
	Status string
}

// FormatFor guesses a format from a file name, defaulting to csv.
func FormatFor(path string) string {
	switch {
	case strings.HasSuffix(path, ".json"):
		return "json"
	case strings.HasSuffix(path, ".md"), strings.HasSuffix(path, ".markdown"):
		return "md"
	default:
		return "csv"
	}
}

func Write(w io.Writer, format string, rows []Row) error {
	switch format {
	case "csv":
		return writeCSV(w, rows)
	case "json":
		return writeJSON(w, rows)
	case "md", "markdown":
		return writeMD(w, rows)
	}
	return fmt.Errorf("unknown format %q (want csv, json, md)", format)
}

var header = []string{"key", "score", "status", "title", "company", "location", "countries", "mode", "type",
	"experience", "areas", "salary_min", "salary_max", "currency", "pay_period", "paid", "pay", "visa", "posted", "first_seen",
	"sources", "url"}

func writeCSV(w io.Writer, rows []Row) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, r := range rows {
		j := r.Job
		if err := cw.Write([]string{
			j.Key, fmt.Sprintf("%.0f", j.Score), r.Status, cell(j.Title), cell(j.Company), cell(loc(&j)),
			strings.Join(j.Countries, ";"), string(j.WorkMode), string(j.JobType), string(j.Experience),
			areas(j.Areas, ";"), num(j.SalaryMin), num(j.SalaryMax), j.Currency, period(&j), paid(&j), j.Pay(), visa(j.VisaSponsorship),
			date(j.PostedAt), date(j.FirstSeenAt), strings.Join(j.Sources, ";"), j.URL,
		}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func writeJSON(w io.Writer, rows []Row) error {
	type out struct {
		model.Job
		Status string `json:"status,omitempty"`
	}
	list := make([]out, len(rows))
	for i, r := range rows {
		list[i] = out{r.Job, r.Status}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(list)
}

func writeMD(w io.Writer, rows []Row) error {
	fmt.Fprintf(w, "# Jobs (%d)\n\n", len(rows))
	fmt.Fprintln(w, "| Score | Status | Title | Company | Location | Type | Pay | Posted | Source |")
	fmt.Fprintln(w, "|---:|---|---|---|---|---|---|---|---|")
	for _, r := range rows {
		j := r.Job
		_, err := fmt.Fprintf(w, "| %.0f | %s | [%s](%s) | %s | %s | %s | %s | %s | %s |\n",
			j.Score, r.Status, md(j.Title), mdURL(j.URL), md(j.Company), md(loc(&j)), j.JobType, md(j.Pay()), date(j.PostedAt),
			strings.Join(j.Sources, ", "))
		if err != nil {
			return err
		}
	}
	return nil
}

// cell neutralizes spreadsheet formula injection (OWASP "CSV Injection"):
// a cell starting with = + - @ or a tab/CR is prefixed with a quote so
// Excel/Sheets treat it as text.
func cell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// mdURL escapes characters that would end a Markdown link target.
func mdURL(u string) string {
	return strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29").Replace(u)
}

func md(s string) string {
	return strings.NewReplacer("|", `\|`, "\n", " ", "[", `\[`, "]", `\]`).Replace(s)
}

func loc(j *model.Job) string {
	switch {
	case j.City != "":
		return j.City
	case j.Location != "":
		return j.Location
	}
	return strings.Join(j.Countries, ",")
}

func areas(as []model.Area, sep string) string {
	s := make([]string, len(as))
	for i, a := range as {
		s[i] = string(a)
	}
	return strings.Join(s, sep)
}

func num(p *float64) string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf("%.0f", *p)
}

func visa(v *bool) string {
	if v == nil {
		return ""
	}
	return strconv.FormatBool(*v)
}

func date(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

// period is what the salary is per, when there is one.
func period(j *model.Job) string {
	if !j.HasSalary() {
		return ""
	}
	return string(j.Period())
}

// paid is "yes" for a stated amount or "paid", "no" for unpaid, and empty
// when the posting doesn't say.
func paid(j *model.Job) string {
	switch {
	case j.HasSalary() || (j.Paid != nil && *j.Paid):
		return "yes"
	case j.Paid != nil:
		return "no"
	}
	return ""
}
