package model

import (
	"math"
	"strconv"
	"strings"
)

// PayPeriod is what a job's salary amounts are per.
type PayPeriod string

const (
	PerYear  PayPeriod = "year"
	PerMonth PayPeriod = "month"
	PerWeek  PayPeriod = "week"
	PerDay   PayPeriod = "day"
	PerHour  PayPeriod = "hour"
)

// AllPayPeriods lists the periods in schema order.
var AllPayPeriods = []PayPeriod{PerYear, PerMonth, PerWeek, PerDay, PerHour}

// ParsePayPeriod reads the period names sources use: "1 MONTH" (Ashby),
// "per-hour-wage" (Lever), "monthly" (Himalayas), "MONTH" (JSearch) and
// plain "year", "annual", "hr". It returns "" for anything else, such as
// a one-time amount.
func ParsePayPeriod(s string) PayPeriod {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(strings.TrimPrefix(s, "1 "), "per-")
	s, _, _ = strings.Cut(s, "-") // "hour-wage", "year-salary"
	switch s {
	case "year", "years", "yearly", "annual", "annually", "annum", "yr":
		return PerYear
	case "month", "months", "monthly", "mo":
		return PerMonth
	case "week", "weeks", "weekly", "wk":
		return PerWeek
	case "day", "days", "daily":
		return PerDay
	case "hour", "hours", "hourly", "hr":
		return PerHour
	}
	return ""
}

// Period is what the job's salary is per: a year unless a source or the
// description said otherwise.
func (j *Job) Period() PayPeriod {
	if j.PayPeriod == "" {
		return PerYear
	}
	return j.PayPeriod
}

// HasSalary reports whether the job gives a pay amount.
func (j *Job) HasSalary() bool { return j.SalaryMin != nil || j.SalaryMax != nil }

// Unpaid reports whether the posting says it's unpaid.
func (j *Job) Unpaid() bool { return j.Paid != nil && !*j.Paid && !j.HasSalary() }

var periodSuffix = map[PayPeriod]string{PerYear: "/yr", PerMonth: "/mo", PerWeek: "/wk", PerDay: "/day", PerHour: "/hr"}

var currencySign = map[string]string{"INR": "₹", "USD": "$", "EUR": "€", "GBP": "£", "JPY": "¥"}

// Pay is the job's pay for display: "₹15k–20k/mo", "$40/hr", "unpaid",
// "paid" (no amount given), or "" when the posting doesn't say.
func (j *Job) Pay() string {
	if !j.HasSalary() {
		switch {
		case j.Paid == nil:
			return ""
		case *j.Paid:
			return "paid"
		default:
			return "unpaid"
		}
	}
	lo, hi := j.SalaryMin, j.SalaryMax
	if lo == nil || (hi != nil && *lo == 0) {
		lo = hi
	}
	if hi == nil || *hi == *lo {
		hi = nil
	}
	sign := currencySign[strings.ToUpper(j.Currency)]
	if sign == "" && isCode(j.Currency) {
		sign = strings.ToUpper(j.Currency) + " "
	}
	inr := strings.EqualFold(j.Currency, "INR")
	s := sign + compact(*lo, inr)
	if hi != nil {
		s += "–" + compact(*hi, inr)
	}
	return s + periodSuffix[j.Period()]
}

// compact writes 15000 as "15k", 1500000 as "1.5M", and lakhs for rupees
// ("12L"), keeping small amounts like hourly pay exact ("27.5").
func compact(v float64, inr bool) string {
	unit, div := "", 1.0
	switch {
	case inr && v >= 1e7:
		unit, div = "Cr", 1e7
	case inr && v >= 1e5:
		unit, div = "L", 1e5
	case v >= 1e6:
		unit, div = "M", 1e6
	case v >= 1e3:
		unit, div = "k", 1e3
	}
	n := v / div
	prec := 0
	if n < 100 && math.Abs(n-math.Round(n)) >= 0.05 {
		prec = 1
	}
	if div == 1 && n < 100 && math.Abs(n*100-math.Round(n*100)) < 1e-6 && n != math.Trunc(n) {
		prec = 2 // $27.25/hr
	}
	return strconv.FormatFloat(n, 'f', prec, 64) + unit
}

// isCode reports whether s looks like an ISO 4217 code; anything else a
// source sends ("US Dollars") is left out of Pay.
func isCode(s string) bool {
	if len(s) != 3 {
		return false
	}
	for i := range len(s) {
		if c := s[i] | 0x20; c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}
