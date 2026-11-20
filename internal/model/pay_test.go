package model

import "testing"

func TestParsePayPeriod(t *testing.T) {
	for in, want := range map[string]PayPeriod{
		"1 YEAR": PerYear, "1 MONTH": PerMonth, "per-hour-wage": PerHour, "per-year-salary": PerYear,
		"monthly": PerMonth, "HOUR": PerHour, "annual": PerYear, "one-time": "", "NONE": "",
	} {
		if got := ParsePayPeriod(in); got != want {
			t.Errorf("ParsePayPeriod(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPayDisplay(t *testing.T) {
	for _, tt := range []struct {
		j    Job
		want string
	}{
		{Job{SalaryMin: new(1200000.0), SalaryMax: new(1800000.0), Currency: "INR"}, "₹12L–18L/yr"},
		{Job{SalaryMin: new(0.0), SalaryMax: new(90000.0), Currency: "EUR"}, "€90k/yr"},
		{Job{SalaryMin: new(27.25), SalaryMax: new(27.25), Currency: "usd", PayPeriod: PerHour}, "$27.25/hr"},
		{Job{SalaryMin: new(50000.0), Currency: "US Dollars"}, "50k/yr"}, // not a code: left out
		{Job{Paid: new(false)}, "unpaid"},
		{Job{}, ""},
	} {
		if got := tt.j.Pay(); got != tt.want {
			t.Errorf("Pay(%+v) = %q, want %q", tt.j, got, tt.want)
		}
	}
}
