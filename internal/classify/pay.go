package classify

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/itxkarthik/jobhunt/internal/model"
)

// Pay from a description. Postings state it in many ways ("stipend:
// ₹12,000/month", "the base salary range is $180,000—$270,000 USD",
// "$35.34 - $41.20 per hour", "CTC 6-8 LPA"), and also mention amounts
// that aren't pay ("gym reimbursement up to $75/month"). An amount counts
// only shortly after a pay word, with no benefit word before it, and only
// with a stated period, or as a salary range of at least 10,000 a year.
//
// As with the "N years" checks, the regexes run only on short windows
// that start at a pay word, never on a whole description.

// payBefore is how far before a pay word the window starts: "monthly
// stipend", or a benefit word ("learning stipend").
const payBefore = 30

var payWords = []string{"salary", "stipend", "pay", "compensation", "wage", "rate", "ctc", "earn", "remunerat", "range"}

// Words that make a nearby amount a benefit, not pay.
var benefitWords = []string{"reimburs", "allowance", "budget", "premium", "differential", "bonus", "credit", "perk", "gym",
	"wellness", "learning", "education", "home office", "internet", "phone", "meal", "lunch", "commut", "cowork",
	"equipment", "transport", "referral", "relocation", "signing", "sign-on", "401", "pension", "insurance",
	"qualification", "spending", "tech setup", "housing"}

const (
	curRe  = `(\b(?:usd|inr|eur|gbp|cad|aud|sgd|jpy|chf)\s?\$?|\brs\.?|\bus\$|\bca\$|\bc\$|\bau\$|\ba\$|\bsg\$|\bs\$|\$|₹|€|£|¥)`
	numRe  = `(\d[\d,]*(?:\.\d+)?)`
	mulRe  = `(k|lakhs?|lacs?)?`
	sepRe  = `\s*(?:-|–|—|to|and)\s*`
	perRe  = `(?:\s*(?:/|per|an?|each|every)\s*(month|mo|hour|hr|week|wk|day|year|yr|annum)\b|\s+(annually|monthly|hourly|weekly|yearly)\b)`
	codeRe = `(?:\s*\b(usd|inr|eur|gbp|cad|aud|sgd|jpy|chf)\b)?`
)

var (
	// "$35.34 - $41.20 per hour", "₹5,000 – ₹8,000 per month", "$180,000—$270,000 usd".
	amountRe = regexp.MustCompile(curRe + `\s?` + numRe + `\s?` + mulRe + `\b(?:` + sepRe + `(?:` + curRe + `)?\s?` + numRe + `\s?` + mulRe + `\b)?` + codeRe + perRe + `?`)
	// "15,000 inr per month": the code after the amount.
	amountAfterRe = regexp.MustCompile(`\b` + numRe + `\s?` + mulRe + `(?:` + sepRe + numRe + `\s?` + mulRe + `)?\s*(usd|inr|eur|gbp|cad|aud|sgd|rs\.?|rupees)` + perRe)
	// "monthly stipend: 32,000", "a weekly stipend of 4,615 usd": the period first.
	periodFirstRe = regexp.MustCompile(`\b(monthly|weekly|hourly|annual|yearly) (?:base )?(?:stipend|salary|pay|wage|rate|compensation)s?(?: range)?\s*(?:of|:|-|–|is|=)?\s*(?:` + curRe + `\s?)?` + numRe + `\s?(k)?\b(?:` + sepRe + `(?:` + curRe + `\s?)?` + numRe + `\s?(k)?\b)?` + codeRe)
	// "20,000 to 30,000 per month": no currency, so only per month or
	// year, and at least 1,000.
	bareRe = regexp.MustCompile(`\b` + numRe + `\s?(k)?` + `(?:` + sepRe + numRe + `\s?(k)?)?\s*(?:/|per|an?)\s*(month|annum|year)\b`)
	// "6-8 lpa", "ctc 12 lakhs per annum".
	lpaRe = regexp.MustCompile(numRe + `(?:` + sepRe + numRe + `)?\s*(?:lpa|l\.p\.a|lakhs? (?:per annum|p\.?a\.?|a year|per year))`)
)

// pay is an amount read from a description.
type pay struct {
	min, max *float64
	currency string
	period   model.PayPeriod // "" for a year
}

// hint is an amount_hints term: a word that, like a currency sign, may
// sit right next to an amount.
type hint struct {
	id   int
	text string
}

// pay fills in j's pay from its description: an amount, or failing that
// whether it's unpaid, or (for internships) paid.
func (c *Classifier) pay(j *model.Job, desc marks, lower string) {
	if c.payWord.any(desc) {
		var words []string
		for _, h := range c.amountHints {
			if desc[h.id] {
				words = append(words, h.text)
			}
		}
		if p, ok := findAmount(lower, words); ok {
			j.SalaryMin, j.SalaryMax, j.Currency, j.PayPeriod = p.min, p.max, p.currency, p.period
			return
		}
	}
	switch {
	case c.unpaid.any(desc) && !c.eitherPaid.any(desc):
		j.Paid = new(false)
	case j.JobType == model.TypeInternship && c.paid.any(desc):
		j.Paid = new(true)
	}
}

const (
	payReach = 100 // bytes before a currency sign that must hold a pay word
	payAfter = 70  // bytes after it that may hold the rest of the amount
	payHint  = 20  // bytes after it that may hold the pay word of a hint
)

// findAmount reads an amount from a lower-cased description. It starts
// from currency signs and the given hint words, which are rarer than pay
// words, and reads the window from the pay word before one.
func findAmount(lower string, hints []string) (pay, bool) {
	var at []int
	for _, sign := range []string{"$", "₹", "€", "£", "¥"} {
		at = appendIndexes(at, lower, sign, false)
	}
	for _, h := range hints {
		at = appendIndexes(at, lower, h, true)
	}
	if len(at) == 0 {
		return pay{}, false
	}
	slices.Sort(at)
	done := 0
	for _, a := range at {
		if a < done {
			continue // read with the anchor before it
		}
		// The pay word may be in the hint itself ("monthly stipend").
		pre := lower[max(0, a-payReach):runeStart(lower, min(len(lower), a+payHint))]
		w := -1
		for _, pw := range payWords {
			if i := strings.LastIndex(pre, pw); i > w && (i == 0 || !isLetter(pre[i-1])) {
				w = i
			}
		}
		if w < 0 {
			continue
		}
		w += max(0, a-payReach)
		lo, hi := runeStart(lower, max(0, w-payBefore)), runeStart(lower, min(len(lower), a+payAfter))
		if p, ok := payIn(lower, lo, lower[lo:hi]); ok {
			return p, true
		}
		done = hi - payAfter/2
	}
	return pay{}, false
}

// appendIndexes appends where sub occurs in s; word skips occurrences
// inside a longer word.
func appendIndexes(at []int, s, sub string, word bool) []int {
	for off := 0; ; {
		i := strings.Index(s[off:], sub)
		if i < 0 {
			return at
		}
		i += off
		off = i + len(sub)
		if word && ((i > 0 && isLetter(s[i-1])) || (off < len(s) && isLetter(s[off]))) {
			continue
		}
		at = append(at, i)
	}
}

func runeStart(s string, i int) int {
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return i
}

// payIn looks for an amount in win, which starts at offset at in lower.
// It checks cheaply for a digit and a currency before any regex.
func payIn(lower string, at int, win string) (pay, bool) {
	if !strings.ContainsAny(win, "0123456789") {
		return pay{}, false
	}
	sym := strings.ContainsAny(win, "$₹€£¥")
	code := strings.Contains(win, "usd") || strings.Contains(win, "inr") || strings.Contains(win, "eur") ||
		strings.Contains(win, "gbp") || strings.Contains(win, "cad") || strings.Contains(win, "aud") ||
		strings.Contains(win, "sgd") || strings.Contains(win, "rs")
	lakh := strings.Contains(win, "lpa") || strings.Contains(win, "l.p.a") || strings.Contains(win, "lakh")
	bare := !sym && !code && (strings.Contains(win, "month") || strings.Contains(win, "annum") || strings.Contains(win, "year"))
	if !sym && !code && !lakh && !bare && !strings.Contains(win, "ly ") {
		return pay{}, false
	}
	ok := func(m []int) bool { return m != nil && !benefitIn(lower[max(0, at+m[0]-payBefore):at+m[0]]) }
	if lakh {
		if m := lpaRe.FindStringSubmatchIndex(win); ok(m) {
			if p, good := makePay(scale(num(win, m, 1), 1e5), scale(num(win, m, 2), 1e5), "INR", model.PerYear, false); good {
				return p, true
			}
		}
	}
	if m := periodFirstRe.FindStringSubmatchIndex(win); ok(m) {
		cur := currency(sub(win, m, 2))
		if c := sub(win, m, 8); c != "" {
			cur = currency(c)
		}
		lo, hi := multiply(num(win, m, 3), sub(win, m, 4)), multiply(num(win, m, 6), sub(win, m, 7))
		if p, good := makePay(lo, hi, cur, model.ParsePayPeriod(sub(win, m, 1)), false); good {
			return p, true
		}
	}
	if sym || code {
		for _, m := range amountRe.FindAllStringSubmatchIndex(win, 3) {
			if !ok(m) {
				continue
			}
			cur := currency(sub(win, m, 1))
			if c := sub(win, m, 7); c != "" {
				cur = currency(c)
			}
			lo, hi := multiply(num(win, m, 2), sub(win, m, 3)), multiply(num(win, m, 5), sub(win, m, 6))
			if hi != nil && sub(win, m, 6) == "" && sub(win, m, 3) != "" {
				hi = multiply(hi, sub(win, m, 3)) // "$40-50k": the k covers both
			}
			per := model.ParsePayPeriod(sub(win, m, 8) + sub(win, m, 9))
			ranged := false
			if per == "" {
				if hi == nil {
					continue // one amount with no period
				}
				per, ranged = model.PerYear, true // a salary range: "$180,000—$270,000 usd"
			}
			if p, good := makePay(lo, hi, cur, per, ranged); good {
				return p, true
			}
		}
	}
	if bare {
		if m := bareRe.FindStringSubmatchIndex(win); ok(m) {
			lo, hi := multiply(num(win, m, 1), sub(win, m, 2)), multiply(num(win, m, 3), sub(win, m, 4))
			if lo != nil && *lo >= 1000 {
				if p, good := makePay(lo, hi, "", model.ParsePayPeriod(sub(win, m, 5)), false); good {
					return p, true
				}
			}
		}
	}
	if code {
		if m := amountAfterRe.FindStringSubmatchIndex(win); ok(m) {
			lo, hi := multiply(num(win, m, 1), sub(win, m, 2)), multiply(num(win, m, 3), sub(win, m, 4))
			if p, good := makePay(lo, hi, currency(sub(win, m, 5)), model.ParsePayPeriod(sub(win, m, 6)+sub(win, m, 7)), false); good {
				return p, true
			}
		}
	}
	return pay{}, false
}

// makePay checks that the amounts are plausible for the period.
func makePay(lo, hi *float64, cur string, per model.PayPeriod, ranged bool) (pay, bool) {
	if lo == nil || per == "" {
		return pay{}, false
	}
	if hi != nil && *hi < *lo {
		return pay{}, false
	}
	top := *lo
	if hi != nil {
		top = *hi
		if *hi > 10**lo {
			return pay{}, false
		}
	}
	floor := map[model.PayPeriod]float64{model.PerYear: 1000, model.PerMonth: 50, model.PerWeek: 20, model.PerDay: 5, model.PerHour: 1}[per]
	if ranged {
		floor = 10000
	}
	if *lo < floor || (per == model.PerHour && top > 2000) {
		return pay{}, false
	}
	p := pay{min: lo, max: hi, currency: cur}
	if per != model.PerYear {
		p.period = per
	}
	return p, true
}

func benefitIn(s string) bool {
	for _, w := range benefitWords {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// currency turns what a posting wrote ("$", "cad $", "rs.", "₹") into an
// ISO 4217 code. A bare "$" is read as US dollars.
func currency(s string) string {
	s = strings.TrimSpace(s)
	if s == "$" || s == "us$" {
		return "USD"
	}
	s = strings.TrimSpace(strings.TrimSuffix(s, "$"))
	switch s {
	case "":
		return ""
	case "rs", "rs.", "₹", "rupees":
		return "INR"
	case "€":
		return "EUR"
	case "£":
		return "GBP"
	case "¥":
		return "JPY"
	case "c", "ca":
		return "CAD"
	case "a", "au":
		return "AUD"
	case "s", "sg":
		return "SGD"
	}
	if len(s) == 3 {
		return strings.ToUpper(s)
	}
	return ""
}

func sub(s string, m []int, g int) string {
	if 2*g+1 >= len(m) || m[2*g] < 0 {
		return ""
	}
	return s[m[2*g]:m[2*g+1]]
}

func num(s string, m []int, g int) *float64 {
	t := strings.ReplaceAll(sub(s, m, g), ",", "")
	if t == "" {
		return nil
	}
	v, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return nil
	}
	return &v
}

func scale(v *float64, by float64) *float64 {
	if v == nil {
		return nil
	}
	return new(*v * by)
}

func multiply(v *float64, unit string) *float64 {
	switch {
	case unit == "k":
		return scale(v, 1e3)
	case strings.HasPrefix(unit, "la"):
		return scale(v, 1e5)
	}
	return v
}
