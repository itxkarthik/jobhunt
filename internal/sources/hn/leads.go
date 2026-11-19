package hn

// Company leads from "Who is hiring?" posts.
//
// Each top-level post becomes a company lead: its name and location from
// the header line, its website and ATS job board from links in the post,
// and a contact only when the poster published an email address in the
// post (plain, mailto, or written as "name [at] co [dot] com"). The post
// is the source URL of every contact. Addresses are never guessed and no
// email-finder service is used.
//
// Access: the Algolia HN Search API (https://hn.algolia.com/api) is the
// documented way to read HN programmatically and has no published rate
// limit; news.ycombinator.com's robots.txt asks crawlers for a 30 s delay,
// so jobhunt never fetches that site, only the API. One run reads the
// latest thread with two requests.

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
)

// Lead is one post turned into a company update.
type Lead struct {
	PostID  int64
	Company model.Company
}

// Thread is the latest "Who is hiring?" thread.
type Thread struct {
	ID    string
	Title string
	item  item
}

// Latest fetches the newest "Who is hiring?" thread.
func (s *Source) Latest(ctx context.Context) (*Thread, error) {
	var sr searchResp
	if err := s.Client.GetJSON(ctx, s.BaseURL+"/search_by_date?tags=story,author_whoishiring&hitsPerPage=10", nil, &sr); err != nil {
		return nil, err
	}
	for _, h := range sr.Hits {
		if strings.HasPrefix(h.Title, "Ask HN: Who is hiring?") {
			var it item
			if err := s.Client.GetJSON(ctx, s.BaseURL+"/items/"+h.ObjectID, nil, &it); err != nil {
				return nil, err
			}
			return &Thread{ID: h.ObjectID, Title: h.Title, item: it}, nil
		}
	}
	return nil, errors.New(`no recent "Who is hiring?" thread found`)
}

// Leads turns the thread's posts into company leads, skipping posts for
// which seen(id) is true.
func (t *Thread) Leads(seen func(postID int64) bool) []Lead {
	var out []Lead
	for _, c := range t.item.Children {
		if c.Text == "" || c.Author == "" || seen(c.ID) {
			continue
		}
		if l, ok := lead(c); ok {
			out = append(out, l)
		}
	}
	return out
}

// Posts lists the IDs of the thread's live posts.
func (t *Thread) Posts() []int64 {
	var ids []int64
	for _, c := range t.item.Children {
		if c.Text != "" && c.Author != "" {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

func lead(c item) (Lead, bool) {
	header := c.Text
	if i := strings.Index(header, "<p>"); i >= 0 {
		header = header[:i]
	}
	header = sources.CleanText(html.UnescapeString(header))
	if header == "" {
		return Lead{}, false
	}
	if seekerRe.MatchString(header) {
		return Lead{}, false // someone looking for work, not a company hiring
	}
	name, _ := splitHeader(header)
	name, ok := leadName(name)
	if !ok {
		return Lead{}, false
	}
	post := fmt.Sprintf("https://news.ycombinator.com/item?id=%d", c.ID)
	co := model.Company{
		Name:    name,
		Tags:    []string{"hn-hiring"},
		Sources: []string{"hn"},
		Remote:  strings.Contains(strings.ToLower(header), "remote"),
	}
	if countries, city := model.ParseLocation(header); len(countries) == 1 && countries[0] != model.RemoteGlobal {
		co.Country, co.City = countries[0], city
	}
	links := postLinks(c.Text)
	for _, u := range links {
		if b, ok := BoardOf(u); ok {
			if !slices.Contains(co.Boards, b) {
				co.Boards = append(co.Boards, b)
			}
			continue
		}
		if co.Website == "" && companySite(u) {
			co.Website = u
		}
	}
	for _, e := range Emails(sources.StripHTML(c.Text) + "\n" + strings.Join(links, "\n")) {
		co.Contacts = append(co.Contacts, model.Contact{Email: e, Role: "hiring contact", SourceURL: post})
		if len(co.Contacts) == 3 {
			break
		}
	}
	return Lead{PostID: c.ID, Company: co}, true
}

var (
	// Posts in the "Who wants to be hired?" format, sometimes left in the
	// hiring thread. They name a person, so they're never imported.
	seekerRe = regexp.MustCompile(`(?i)^\s*(location|remote|willing to relocate|résumé|resume|cv)\s*:|\bseeking (freelance|work|a (job|role|position)|employment|opportunities)\b|\bfor hire\b|\bavailable for (work|hire|contracts?)\b|\blooking for (work|a (job|role))\b`)
	// Placeholders and job titles where the company name should be.
	noNameRe  = regexp.MustCompile(`(?i)\b(confidential|stealth|undisclosed|unnamed)\b|^(apply|hiring)( here| now)?$|^(an? )?(early[- ]stage |seed[- ]stage |funded |yc[- ]backed |series [a-c] )?(startup|company)$|^(senior |staff |lead |principal |founding )?(software|backend|frontend|full[- ]?stack|ml|data|platform|devops|mobile)?\s*(engineers?|developers?)$`)
	nameURLRe = regexp.MustCompile(`(?i)[:\s(]*https?://.*$`)
)

// leadName tidies a header's company name, or reports that it doesn't
// name a company.
func leadName(name string) (string, bool) {
	name = strings.TrimSpace(nameURLRe.ReplaceAllString(name, ""))
	name = strings.TrimSpace(strings.Trim(name, "*_#~"))
	if name == "" || len(name) > 60 || noNameRe.MatchString(name) {
		return "", false
	}
	return name, true
}

var hrefRe = regexp.MustCompile(`href="([^"]+)"`)
var textURLRe = regexp.MustCompile(`https?://[^\s<>"')\]]+`)

// postLinks returns the http(s) and mailto links in a post, in order.
func postLinks(raw string) []string {
	var out []string
	add := func(u string) {
		u = strings.TrimRight(html.UnescapeString(u), ".,;:")
		if !slices.Contains(out, u) {
			out = append(out, u)
		}
	}
	for _, m := range hrefRe.FindAllStringSubmatch(raw, -1) {
		add(m[1])
	}
	for _, m := range textURLRe.FindAllString(sources.StripHTML(raw), -1) {
		add(m)
	}
	return out
}

// notCompany are hosts a post links to that aren't the company's site.
var notCompany = []string{
	"ycombinator.com", "linkedin.com", "github.com", "twitter.com", "x.com", "google.com",
	"forms.gle", "calendly.com", "wellfound.com", "angel.co", "notion.site", "youtube.com",
	"medium.com", "substack.com", "techcrunch.com", "bit.ly", "lnkd.in", "typeform.com",
	"workatastartup.com", "hn.algolia.com", "youtu.be", "loom.com", "netlify.app",
}

func companySite(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return false
	}
	h := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
	return !slices.ContainsFunc(notCompany, func(d string) bool { return h == d || strings.HasSuffix(h, "."+d) })
}

// BoardOf recognises a job board link on a supported ATS.
func BoardOf(raw string) (model.Board, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return model.Board{}, false
	}
	host := strings.ToLower(u.Hostname())
	first, _, _ := strings.Cut(strings.Trim(u.Path, "/"), "/")
	slug := strings.ToLower(first)
	switch {
	case host == "boards.greenhouse.io" || host == "job-boards.greenhouse.io":
		if slug == "embed" {
			slug = strings.ToLower(u.Query().Get("for"))
		}
		return board("greenhouse", slug)
	case host == "jobs.lever.co":
		return board("lever", slug)
	case host == "jobs.ashbyhq.com":
		return board("ashby", slug)
	case host == "apply.workable.com":
		return board("workable", slug)
	case strings.HasSuffix(host, ".recruitee.com"):
		return board("recruitee", strings.TrimSuffix(host, ".recruitee.com"))
	}
	return model.Board{}, false
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,99}$`)

func board(ats, slug string) (model.Board, bool) {
	if !slugRe.MatchString(slug) || slug == "j" || slug == "api" {
		return model.Board{}, false
	}
	return model.Board{ATS: ats, Slug: slug}, true
}

var (
	plainEmailRe = regexp.MustCompile(`(?i)(?:mailto:)?([a-z0-9._%+-]+@[a-z0-9-]+(?:\.[a-z0-9-]+)*\.[a-z]{2,})`)
	// name [at] co [dot] com, name (at) co.com, name {at} co {dot} com
	bracketRe = regexp.MustCompile(`(?i)([a-z0-9._%+-]+)\s*[\[({<]\s*at\s*[\])}>]\s*([a-z0-9-]+(?:(?:\s*[\[({<]\s*dot\s*[\])}>]\s*|\.)[a-z0-9-]+)+)`)
	// name at co dot com: both words required, so prose doesn't match
	wordsRe = regexp.MustCompile(`(?i)\b([a-z0-9._%+-]+)\s+at\s+([a-z0-9-]+(?:\s+dot\s+[a-z0-9-]+)+)\b`)
	tldRe   = regexp.MustCompile(`\.[a-z]{2,}$`)
	dotRe   = regexp.MustCompile(`(?i)\s*(?:[\[({<]\s*dot\s*[\])}>]|\s+dot\s+|\.)\s*`)
)

// Emails returns the email addresses written in text, including the
// common obfuscations, deduplicated and lower-cased. Only addresses the
// text spells out are returned; none are constructed.
func Emails(text string) []string {
	var out []string
	add := func(e string) {
		e = strings.ToLower(strings.TrimRight(e, "."))
		a, err := mail.ParseAddress(e)
		if err != nil || a.Address != e || strings.HasSuffix(e, "@news.ycombinator.com") || strings.HasSuffix(e, "@example.com") {
			return
		}
		if _, domain, _ := strings.Cut(e, "@"); !tldRe.MatchString(domain) {
			return
		}
		if !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	for _, m := range plainEmailRe.FindAllStringSubmatch(text, -1) {
		add(m[1])
	}
	for _, re := range []*regexp.Regexp{bracketRe, wordsRe} {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			domain := dotRe.ReplaceAllString(m[2], ".")
			if strings.Contains(domain, ".") {
				add(m[1] + "@" + domain)
			}
		}
	}
	return out
}
