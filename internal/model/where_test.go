package model

import "testing"

func TestShortLocation(t *testing.T) {
	// A remote internship open across Asia: its location text starts with
	// countries the user never searched for.
	asia := Job{
		Location:  "Afghanistan, Armenia, Australia, Azerbaijan, Bahrain, Bangladesh, India, Japan",
		Countries: []string{"AU", "BD", "IN", "JP"},
	}
	tests := []struct {
		name string
		job  Job
		want []string
		out  string
	}{
		{"multi-country names the searched country", asia, []string{"IN"}, "India +3"},
		{"first searched country it lists wins", asia, []string{"US", "JP", "IN"}, "Japan +3"},
		{"no searched country falls back to the first", asia, []string{"US"}, "Australia +3"},
		{"no filter falls back to the first", asia, nil, "Australia +3"},
		{"unlabelled code shows as the code", Job{Countries: []string{"BD", "IN"}}, nil, "BD +1"},
		{"region code", Job{Countries: []string{"region:apac", "IN"}}, nil, "APAC +1"},
		{"city wins", Job{City: "Bengaluru", Location: "Bengaluru, India", Countries: []string{"IN", "SG"}}, []string{"IN"}, "Bengaluru"},
		{"one country keeps its text", Job{Location: "Pune, Maharashtra", Countries: []string{"IN"}}, []string{"IN"}, "Pune, Maharashtra"},
		{"no text names the country", Job{Countries: []string{"IN"}}, nil, "India"},
		{"worldwide remote", Job{Countries: []string{RemoteGlobal}}, []string{"IN"}, "Worldwide"},
		{"nothing known", Job{}, nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShortLocation(&tt.job, tt.want); got != tt.out {
				t.Errorf("ShortLocation = %q, want %q", got, tt.out)
			}
		})
	}
}
