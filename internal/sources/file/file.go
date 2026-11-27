// Package file reads jobs from local NDJSON files, one model.Job per line,
// written by other job tools or your own scripts. Nothing is fetched over
// the network.
//
// Each job keeps the source name its line gives (default "file"), so it is
// shown, merged and tracked like a job from that source. Fields jobhunt
// computes itself (key, score, first_seen_at, …) are ignored.
package file

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/itxkarthik/jobhunt/internal/model"
	"github.com/itxkarthik/jobhunt/internal/sources"
)

// maxLine bounds one job's line; real postings are a few KB.
const maxLine = 4 << 20

// MaxAge is how long a file may go unchanged before its jobs count as
// stale, as long as a failed source's cache may stand in for it.
const MaxAge = 3 * 24 * time.Hour

var (
	nameRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	countryRe = regexp.MustCompile(`^(?:[A-Z]{2}|remote-global|region:[a-z-]{2,24})$`)
)

type Source struct {
	Paths []string
	// Reserved are other sources' names. A job claiming one is labelled
	// "file", so a file can't stand in for a source that failed.
	Reserved []string
	Now      func() time.Time // for tests; nil means time.Now
}

func New(paths []string) *Source {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p = expand(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return &Source{Paths: out}
}

func (s *Source) Name() string                { return "file" }
func (s *Source) Supports(model.Filters) bool { return true }

func (s *Source) Ready() error {
	if len(s.Paths) == 0 {
		return sources.NotConfiguredError{Msg: "set sources.file.paths"}
	}
	return nil
}

// DefaultTTL is long because CacheKey changes whenever a file does.
func (s *Source) DefaultTTL() time.Duration { return 24 * time.Hour }

// CacheKey is each file's size and modification time, so a rewritten file
// misses the cache and is read again on the next search.
// A file going stale changes it too, so its jobs are read again and
// reported stale.
func (s *Source) CacheKey(model.Filters) string {
	var b strings.Builder
	for _, p := range s.Paths {
		b.WriteString(p)
		if fi, err := os.Stat(p); err == nil {
			fmt.Fprintf(&b, ":%d:%d", fi.Size(), fi.ModTime().UnixNano())
			if s.stale(fi.ModTime()) {
				b.WriteString(":stale")
			}
		} else {
			b.WriteString(":missing")
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func (s *Source) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Source) stale(mod time.Time) bool { return s.now().Sub(mod) > MaxAge }

func (s *Source) Fetch(ctx context.Context, _ model.Filters) ([]model.Job, error) {
	var (
		jobs   []model.Job
		failed []string
		first  error
		total  int
		stale  []string
	)
	fail := func(what string, err error) {
		failed = append(failed, what)
		if first == nil {
			first = err
		}
	}
	for _, p := range s.Paths {
		if fi, err := os.Stat(p); err == nil && s.stale(fi.ModTime()) {
			stale = append(stale, fmt.Sprintf("%s not updated for %s", filepath.Base(p), s.age(fi.ModTime())))
		}
		js, bad, err := readFile(ctx, p, s.Reserved)
		jobs = append(jobs, js...)
		total += len(js) + len(bad)
		for _, n := range bad {
			fail(filepath.Base(p)+":"+strconv.Itoa(n), fmt.Errorf("%s:%d: not a job", filepath.Base(p), n))
		}
		if err != nil {
			total++
			fail(filepath.Base(p), err)
		}
	}
	switch {
	case len(stale) > 0 && len(jobs) > 0:
		// One stale file is enough: the source can only answer as a whole.
		return jobs, &sources.StaleError{Err: errors.New(strings.Join(stale, "; "))}
	case first == nil:
		return jobs, nil
	case len(jobs) == 0:
		return nil, first
	}
	return jobs, &sources.PartialError{Failed: failed, Total: total, Err: first}
}

// age is how long ago mod was, in whole days.
func (s *Source) age(mod time.Time) string {
	return strconv.Itoa(int(s.now().Sub(mod)/(24*time.Hour))) + "d"
}

// readFile returns the jobs in path and the line numbers that weren't jobs.
func readFile(ctx context.Context, path string, reserved []string) (jobs []model.Job, bad []int, err error) {
	f, err := os.Open(path) //nolint:gosec // G304: a file the user listed in config
	if err != nil {
		// Name only the file, not the whole path, in errors shown in the UI.
		if pe, ok := errors.AsType[*os.PathError](err); ok {
			err = pe.Err
		}
		return nil, nil, fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	defer func() { _ = f.Close() }() // read-only
	r := bufio.NewReader(f)
	for n := 1; ; n++ {
		if n%1000 == 0 && ctx.Err() != nil {
			return jobs, bad, ctx.Err()
		}
		line, rerr := readLine(r)
		if len(line) > 0 {
			if j, ok := parse(line, reserved); ok {
				jobs = append(jobs, j)
			} else {
				bad = append(bad, n)
			}
		}
		if errors.Is(rerr, io.EOF) {
			return jobs, bad, nil
		}
		if rerr != nil {
			return jobs, bad, fmt.Errorf("read %s: %w", filepath.Base(path), rerr)
		}
	}
}

// readLine returns one trimmed line. A line over maxLine comes back as
// "-", so it counts as a bad line instead of ending the file.
func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	long := false
	for {
		chunk, err := r.ReadSlice('\n')
		if !long && len(line)+len(chunk) > maxLine {
			long, line = true, nil
		}
		if !long {
			line = append(line, chunk...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if long {
			return []byte("-"), err
		}
		return bytes.TrimSpace(line), err
	}
}

func parse(line []byte, reserved []string) (model.Job, bool) {
	if line[0] != '{' {
		return model.Job{}, false
	}
	var j model.Job
	if json.Unmarshal(line, &j) != nil || strings.TrimSpace(j.Title) == "" || sources.SafeURL(j.URL) == "" {
		return model.Job{}, false
	}
	// jobhunt computes these; a file's values could clash with real ones.
	j.Key, j.Sources, j.FirstSeenAt, j.Score, j.ScoreParts, j.New = "", nil, time.Time{}, 0, nil, false

	j.Source = strings.ToLower(strings.TrimSpace(j.Source))
	if !nameRe.MatchString(j.Source) || slices.Contains(reserved, j.Source) {
		j.Source = "file"
	}
	// SanitizeJob cleans the text fields; ID and Countries come from
	// the file here, not from a source's own parsing, so clean them too.
	id := strings.TrimSpace(sources.SafeText(j.ID, false))
	if len(id) > 512 {
		id = ""
	}
	if id == "" {
		id = j.URL
	}
	if !strings.HasPrefix(id, j.Source+":") {
		id = j.Source + ":" + id
	}
	j.ID = id
	if strings.TrimSpace(j.Company) == "" {
		j.Company = "unknown"
	}

	// Unknown enum values stay unknown, and the classifier fills them.
	if m, err := model.ParseWorkMode(string(j.WorkMode)); err == nil {
		j.WorkMode = m
	} else {
		j.WorkMode = model.ModeUnknown
	}
	if t, err := model.ParseJobType(string(j.JobType)); err == nil {
		j.JobType = t
	} else {
		j.JobType = model.TypeUnknown
	}
	if e, err := model.ParseExperience(string(j.Experience)); err == nil {
		j.Experience = e
	} else {
		j.Experience = model.ExpUnknown
	}
	j.PayPeriod = model.ParsePayPeriod(string(j.PayPeriod))
	if j.PayPeriod == model.PerYear {
		j.PayPeriod = "" // empty means a year
	}
	j.Areas = slices.DeleteFunc(j.Areas, func(a model.Area) bool { _, err := model.ParseArea(string(a)); return err != nil })

	// Country and city go into the dedup key, so fill them the way the
	// other sources do; otherwise the same posting from a board with a
	// parsed location would get a different key.
	codes := j.Countries[:0]
	for _, c := range j.Countries {
		if !countryRe.MatchString(c) {
			c, _ = model.NormalizeCountry(c) // "India", "in", "remote"
		}
		if countryRe.MatchString(c) && !slices.Contains(codes, c) {
			codes = append(codes, c)
		}
	}
	j.Countries = codes
	countries, city := model.ParseLocation(j.Location)
	if len(j.Countries) == 0 {
		j.Countries = countries
	}
	if j.City == "" {
		j.City = city
	}
	j.Description = sources.Truncate(j.Description, 6000)
	return j, true
}

func expand(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}
