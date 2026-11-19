package company

import (
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
)

// Removal names entries to take off a company: contacts by email, URL or
// name, boards as "ats:slug", tags, and the notes.
type Removal struct {
	Contacts []string `json:"contacts,omitempty"`
	Boards   []string `json:"boards,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	// Notes clears the notes. An empty notes field in an update means
	// "unchanged", so clearing needs saying.
	Notes bool `json:"notes,omitempty"`
}

// Merge applies a cleaned update to the stored record old (nil when the
// company is new) and returns the result. Fields the update leaves empty
// or unknown keep their stored value, so callers can send only what they
// learned. Lists are merged: a round, contact or board already stored is
// replaced by the update's copy, anything else is added.
func Merge(old *model.Company, in model.Company, rm Removal, now time.Time) model.Company {
	var c model.Company
	if old != nil {
		c = *old
		c.Rounds = slices.Clone(old.Rounds)
		c.Contacts = slices.Clone(old.Contacts)
		c.Boards = slices.Clone(old.Boards)
		c.Tags = slices.Clone(old.Tags)
		c.Sources = slices.Clone(old.Sources)
	} else {
		c = model.Company{Key: in.Key, Stage: model.StageUnknown, Headcount: model.HeadcountUnknown, CreatedAt: now}
	}
	if in.Name != "" {
		c.Name = in.Name // same key; the latest spelling wins
	}
	set(&c.Website, in.Website)
	set(&c.Country, in.Country)
	set(&c.City, in.City)
	c.Remote = c.Remote || in.Remote
	if in.Headcount != "" && in.Headcount != model.HeadcountUnknown {
		c.Headcount, c.HeadcountSource = in.Headcount, in.HeadcountSource
	}
	for _, r := range in.Rounds {
		if i := slices.IndexFunc(c.Rounds, func(x model.Round) bool { return sameRound(x, r) }); i >= 0 {
			c.Rounds[i] = r
		} else {
			c.Rounds = append(c.Rounds, r)
		}
	}
	slices.SortStableFunc(c.Rounds, func(a, b model.Round) int { return b.Date.Compare(a.Date) })
	if len(c.Rounds) > MaxRounds {
		c.Rounds = c.Rounds[:MaxRounds]
	}
	// An explicit stage wins; otherwise a newly reported round can move it.
	switch {
	case in.Stage != "" && in.Stage != model.StageUnknown:
		c.Stage = in.Stage
	case len(in.Rounds) > 0 && c.Rounds[0].Stage != "" && c.Rounds[0].Stage != model.StageUnknown:
		c.Stage = c.Rounds[0].Stage
	}
	for _, ct := range in.Contacts {
		if ct.AddedAt.IsZero() {
			ct.AddedAt = now
		}
		if i := slices.IndexFunc(c.Contacts, func(x model.Contact) bool { return sameContact(x, ct) }); i >= 0 {
			ct.AddedAt = c.Contacts[i].AddedAt
			c.Contacts[i] = ct
		} else if len(c.Contacts) < MaxContacts {
			c.Contacts = append(c.Contacts, ct)
		}
	}
	for _, b := range in.Boards {
		if !slices.Contains(c.Boards, b) && len(c.Boards) < MaxBoards {
			c.Boards = append(c.Boards, b)
		}
	}
	for _, t := range in.Tags {
		if !slices.Contains(c.Tags, t) && len(c.Tags) < MaxTags {
			c.Tags = append(c.Tags, t)
		}
	}
	for _, s := range in.Sources {
		if !slices.Contains(c.Sources, s) {
			c.Sources = append(c.Sources, s)
		}
	}
	set(&c.Notes, in.Notes)
	if in.CheckedAt.After(c.CheckedAt) {
		c.CheckedAt = in.CheckedAt
	}

	for _, id := range rm.Contacts {
		c.Contacts = slices.DeleteFunc(c.Contacts, func(x model.Contact) bool { return matchesContact(x, id) })
	}
	for _, id := range rm.Boards {
		c.Boards = slices.DeleteFunc(c.Boards, func(b model.Board) bool { return strings.EqualFold(b.String(), strings.TrimSpace(id)) })
	}
	if rm.Notes {
		c.Notes = ""
	}
	for _, t := range rm.Tags {
		c.Tags = slices.DeleteFunc(c.Tags, func(x string) bool { return strings.EqualFold(x, strings.TrimSpace(t)) })
	}
	if old != nil && reflect.DeepEqual(c, *old) {
		return c // nothing changed: keep UpdatedAt, so feeds and digests don't resend it
	}
	c.UpdatedAt = now
	return c
}

func set(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}

func sameRound(a, b model.Round) bool {
	return a.SourceURL == b.SourceURL && (a.Date.IsZero() || b.Date.IsZero() || a.Date.Equal(b.Date))
}

// sameContact matches by email, then profile URL, then name and role.
func sameContact(a, b model.Contact) bool {
	switch {
	case a.Email != "" && b.Email != "":
		return strings.EqualFold(a.Email, b.Email)
	case a.URL != "" && b.URL != "":
		return a.URL == b.URL
	default:
		return a.Name != "" && strings.EqualFold(a.Name, b.Name) && strings.EqualFold(a.Role, b.Role)
	}
}

func matchesContact(c model.Contact, id string) bool {
	id = strings.TrimSpace(id)
	return id != "" && (strings.EqualFold(c.Email, id) || c.URL == id || strings.EqualFold(c.Name, id))
}

// FillOnly drops the single-value fields of an imported update that old
// already has, so an importer fills gaps but never overwrites what the
// user or an earlier, likely better source set. Lists still merge.
func FillOnly(old *model.Company, in model.Company) model.Company {
	if old == nil {
		return in
	}
	keep := func(dst *string, have string) {
		if have != "" {
			*dst = ""
		}
	}
	keep(&in.Website, old.Website)
	keep(&in.Country, old.Country)
	keep(&in.City, old.City)
	keep(&in.Notes, old.Notes)
	if old.Stage != "" && old.Stage != model.StageUnknown {
		in.Stage = ""
	}
	if old.Headcount != "" && old.Headcount != model.HeadcountUnknown {
		in.Headcount, in.HeadcountSource = "", ""
	}
	return in
}
