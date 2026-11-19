package classify

import (
	"strings"
	"unicode"
)

// Tokenize lowercases s and splits it into words. Letters, digits, '+', '#'
// and inner '.' are word characters, so "C++", "C#", "node.js" and ".NET"
// survive while "back-end", "ci/cd" and "100%" split into separate words.
// Terms are tokenized the same way, so "back-end" matches "back end".
func Tokenize(s string) []string {
	return tokenizeLower(strings.ToLower(s))
}

// tokenizeLower is Tokenize for text that is already lower-cased.
func tokenizeLower(s string) []string {
	toks := make([]string, 0, len(s)/5)
	start := -1
	flush := func(end int) {
		if start < 0 {
			return
		}
		t := strings.TrimRight(s[start:end], ".")
		if t != "" && t != "." {
			toks = append(toks, t)
		}
		start = -1
	}
	for i, r := range s {
		word := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '+' || r == '#' || r == '.'
		if word {
			if start < 0 {
				if r == '.' && !(i+1 < len(s) && isLetter(s[i+1])) {
					continue // a stray period, not ".net"
				}
				start = i
			}
		} else {
			flush(i)
		}
	}
	flush(len(s))
	return toks
}

func isLetter(b byte) bool { return b >= 'a' && b <= 'z' }

// TermSet matches multi-word terms against tokenized text in one pass:
// each token is looked up by first word, then candidates are compared.
type TermSet struct {
	terms [][]string
	first map[string][]int
}

// NewTermSet builds a TermSet; empty terms are ignored.
func NewTermSet(terms []string) *TermSet {
	ts := &TermSet{first: map[string][]int{}}
	for _, t := range terms {
		ts.add(t)
	}
	return ts
}

// add appends one term and returns its index, or -1 if it's empty.
func (ts *TermSet) add(term string) int {
	toks := Tokenize(term)
	if len(toks) == 0 {
		return -1
	}
	id := len(ts.terms)
	ts.first[toks[0]] = append(ts.first[toks[0]], id)
	ts.terms = append(ts.terms, toks)
	return id
}

// mark returns, per term index, whether it occurs in toks.
func (ts *TermSet) mark(toks []string) []bool {
	m := make([]bool, len(ts.terms))
	ts.scan(toks, func(i int) bool { m[i] = true; return true })
	return m
}

// Len is the number of terms.
func (ts *TermSet) Len() int { return len(ts.terms) }

// Any reports whether any term occurs in toks.
func (ts *TermSet) Any(toks []string) bool {
	found := false
	ts.scan(toks, func(int) bool { found = true; return false })
	return found
}

// Hits counts distinct terms occurring in toks.
func (ts *TermSet) Hits(toks []string) int {
	seen := map[int]bool{}
	ts.scan(toks, func(i int) bool { seen[i] = true; return true })
	return len(seen)
}

// scan calls fn for each match; fn returns false to stop.
func (ts *TermSet) scan(toks []string, fn func(term int) bool) {
	if len(ts.terms) == 0 {
		return
	}
	for i, tok := range toks {
		for _, ti := range ts.first[tok] {
			t := ts.terms[ti]
			if i+len(t) > len(toks) {
				continue
			}
			match := true
			for k := 1; k < len(t); k++ {
				if toks[i+k] != t[k] {
					match = false
					break
				}
			}
			if match && !fn(ti) {
				return
			}
		}
	}
}
