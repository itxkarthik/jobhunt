package model

import "testing"

func TestCityMatcher(t *testing.T) {
	remote := func(city, loc string) Job { return Job{WorkMode: ModeRemote, City: city, Location: loc} }
	onsite := func(city, loc string) Job { return Job{WorkMode: ModeOnsite, City: city, Location: loc} }
	tests := []struct {
		cities []string
		j      Job
		strict bool
		want   bool
	}{
		{[]string{"BANGALORE"}, onsite("Bengaluru", "Bengaluru, Karnataka"), true, true},
		{[]string{"calicut"}, onsite("", "Pune / Kozhikode"), true, true},
		{[]string{"navimumbai"}, onsite("", "NaviMumbai, MH"), true, true},
		{[]string{"Mumbai"}, onsite("Navi Mumbai", "Navi Mumbai"), true, false},
		{[]string{"Kochi"}, onsite("Mumbai", "Mumbai"), false, false},
		{[]string{"Kochi"}, remote("", "India"), false, true},
		{[]string{"Kochi"}, remote("", "India"), true, false},
		{[]string{"Kochi"}, remote("", ""), false, true},
		{[]string{"Kochi"}, remote("Remote", "Remote"), false, true},                           // Arbeitnow
		{[]string{"Kochi"}, remote("", "Acme | Backend Intern | Remote (India)"), false, true}, // HN
		{[]string{"Kochi"}, remote("", "Acme | Intern | Remote, Pune"), false, false},
		{[]string{"Kochi"}, remote("", "Kerala, India"), false, true},
		{[]string{"Kochi"}, remote("", "Karnataka, India"), false, false},
		{[]string{"Seattle"}, remote("", "North America"), false, true},
		{[]string{"Kochi"}, remote("", "Sitamarhi"), false, false},
		{[]string{"Kochi"}, remote("United Kindom", "Remote job"), false, false},
		{[]string{"Kochi"}, Job{Location: "India"}, false, false},
		{[]string{"Atlantis"}, onsite("", "Atlantis, Sea"), true, true},
	}
	for _, tt := range tests {
		if got := NewCityMatcher(tt.cities).Allows(&tt.j, tt.strict); got != tt.want {
			t.Errorf("%v strict=%v %+v: got %v", tt.cities, tt.strict, tt.j, got)
		}
	}
	if NewCityMatcher([]string{" "}) != nil {
		t.Error("blank cities should mean no filter")
	}
}
