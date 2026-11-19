package company

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
)

// Draft is an outreach email for the user to edit and send themselves.
// jobhunt never sends email.
type Draft struct {
	To      string `json:"to,omitempty"` // a published address, or empty
	ToName  string `json:"to_name,omitempty"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	// Mailto opens the draft in the user's mail client; empty without To.
	Mailto string `json:"mailto,omitempty"`
	// SourceURL is where the recipient published their address.
	SourceURL string `json:"source_url,omitempty"`
}

// DraftOptions personalise a draft.
type DraftOptions struct {
	Stack []string // the user's languages in priority order, e.g. Go, Rust
	Now   time.Time
}

// rolePriority orders contacts: founders and engineering leaders first,
// then hiring addresses.
var rolePriority = []string{"ceo", "founder", "cto", "head of engineering", "engineering", "hiring", "recruit", "talent"}

// BestContact picks who to write to: a contact with a published email,
// preferring founders and engineering leaders.
func BestContact(c *model.Company) (model.Contact, bool) {
	best, rank := model.Contact{}, len(rolePriority)+1
	for _, ct := range c.Contacts {
		if ct.Email == "" {
			continue
		}
		r := len(rolePriority)
		role := strings.ToLower(ct.Role)
		for i, p := range rolePriority {
			if strings.Contains(role, p) {
				r = i
				break
			}
		}
		if r < rank {
			best, rank = ct, r
		}
	}
	return best, rank <= len(rolePriority)
}

// NewDraft writes a short, specific email about job (may be nil) at c.
// Placeholders in [brackets] are for the user to fill in.
func NewDraft(c *model.Company, job *model.Job, o DraftOptions) Draft {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	ct, ok := BestContact(c)
	greet := "Hi " + c.Name + " team,"
	if ok && ct.Name != "" {
		first, _, _ := strings.Cut(strings.TrimSpace(ct.Name), " ")
		greet = "Hi " + first + ","
	}
	role := "an engineering role"
	subject := "Engineering at " + c.Name
	if job != nil && job.Title != "" {
		role = "the " + job.Title + " role"
		subject = job.Title + " at " + c.Name
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", greet)
	fmt.Fprintf(&b, "I'm writing about %s at %s", role, c.Name)
	if job != nil && job.URL != "" {
		fmt.Fprintf(&b, " (%s)", job.URL)
	}
	b.WriteString(".")
	if r := c.LatestRound(); r != nil && !r.Date.IsZero() && o.Now.Sub(r.Date) < 365*24*time.Hour {
		stage := ""
		if r.Stage != "" && r.Stage != model.StageUnknown {
			stage = " " + strings.ReplaceAll(string(r.Stage), "-", " ")
		}
		fmt.Fprintf(&b, " Congratulations on the recent%s round.", stage)
	}
	b.WriteString("\n\n[One or two sentences on what you've built that fits this team, with a link.]")
	if langs := topLanguages(o.Stack, 2); len(langs) > 0 {
		fmt.Fprintf(&b, "\n\nI work mostly in %s.", joinAnd(langs))
	}
	b.WriteString("\n\nWould you be open to a short call, or should I apply through the usual process?\n\nThanks,\n[Your name]\n[Link to your work]\n")
	d := Draft{Subject: subject, Body: b.String()}
	if ok {
		d.To, d.ToName, d.SourceURL = ct.Email, ct.Name, ct.SourceURL
		d.Mailto = "mailto:" + url.PathEscape(ct.Email) + "?subject=" + mailEscape(d.Subject) + "&body=" + mailEscape(d.Body)
	}
	return d
}

// mailEscape encodes a mailto header value: spaces as %20, since mail
// clients show a '+' literally.
func mailEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// languageNames spell config stack entries the way people write them.
var languageNames = map[string]string{
	"go": "Go", "golang": "Go", "rust": "Rust", "node": "Node.js", "nodejs": "Node.js", "node.js": "Node.js",
	"python": "Python", "java": "Java", "javascript": "JavaScript", "typescript": "TypeScript",
	"c++": "C++", "c#": "C#", "kotlin": "Kotlin", "swift": "Swift", "ruby": "Ruby", "elixir": "Elixir", "scala": "Scala",
}

// topLanguages returns the first n non-empty stack entries, properly spelled.
func topLanguages(stack []string, n int) []string {
	var out []string
	for _, l := range stack {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if name, ok := languageNames[strings.ToLower(l)]; ok {
			l = name
		}
		out = append(out, l)
		if len(out) == n {
			break
		}
	}
	return out
}

func joinAnd(xs []string) string {
	switch len(xs) {
	case 1:
		return xs[0]
	case 2:
		return xs[0] + " and " + xs[1]
	default:
		return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
	}
}
