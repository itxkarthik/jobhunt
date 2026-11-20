package classify

import (
	"slices"
	"testing"

	"github.com/itxkarthik/jobhunt/internal/model"
)

func TestDefaultLoads(t *testing.T) {
	c := Default()
	if len(c.areas) != len(model.AllAreas) {
		t.Errorf("default keywords define %d areas, want all %d", len(c.areas), len(model.AllAreas))
	}
}

func TestJobType(t *testing.T) {
	c := Default()
	tests := []struct {
		title  string
		source model.JobType
		want   model.JobType
	}{
		{"Backend Engineering Intern", model.TypeUnknown, model.TypeInternship},
		{"Software Engineer Intern (Summer 2027)", model.TypeFullTime, model.TypeInternship}, // title beats source "full_time"
		{"New Grad Software Engineer", model.TypeUnknown, model.TypeNewGrad},
		{"Graduate Engineer Trainee", model.TypeUnknown, model.TypeInternship},
		{"Contract Go Developer", model.TypeFullTime, model.TypeContract},
		{"Senior Backend Engineer", model.TypeFullTime, model.TypeFullTime},
		{"Senior Backend Engineer", model.TypeUnknown, model.TypeUnknown},
		{"Internal Tools Engineer", model.TypeUnknown, model.TypeUnknown}, // "intern" is not a prefix match
	}
	for _, tt := range tests {
		j := model.Job{Title: tt.title, JobType: tt.source}
		c.Classify(&j)
		if j.JobType != tt.want {
			t.Errorf("%q (source %s): type = %s, want %s", tt.title, tt.source, j.JobType, tt.want)
		}
	}
}

func TestExperience(t *testing.T) {
	c := Default()
	tests := []struct {
		title, desc string
		want        model.Experience
	}{
		{"Backend Intern", "", model.ExpIntern},
		{"New Grad SWE", "", model.ExpEntry},
		{"Junior Go Developer", "", model.ExpEntry},
		{"Staff Engineer", "", model.ExpSenior},
		{"SDE 2", "", model.ExpMid},
		{"Backend Engineer", "Requires 5+ years of experience", model.ExpSenior},
		{"Backend Engineer", "2-4 years experience with Go", model.ExpMid},
		{"Backend Engineer", "0-1 years, freshers welcome", model.ExpEntry},
		{"Backend Engineer", "Great team.", model.ExpUnknown},
	}
	for _, tt := range tests {
		j := model.Job{Title: tt.title, Description: tt.desc}
		c.Classify(&j)
		if j.Experience != tt.want {
			t.Errorf("%q / %q: experience = %s, want %s", tt.title, tt.desc, j.Experience, tt.want)
		}
	}
}

func TestWorkMode(t *testing.T) {
	c := Default()
	tests := []struct {
		title, loc, desc string
		source           model.WorkMode
		want             model.WorkMode
	}{
		{"Backend Engineer (Remote)", "", "", model.ModeUnknown, model.ModeRemote},
		{"Backend Engineer", "Bengaluru (Hybrid)", "", model.ModeUnknown, model.ModeHybrid},
		{"Backend Engineer", "Pune", "This is a hybrid role, 3 days in office.", model.ModeUnknown, model.ModeHybrid},
		{"Backend Engineer", "Pune", "Work with remote teams.", model.ModeUnknown, model.ModeUnknown},
		{"Backend Engineer", "Pune", "Fully remote, work from home.", model.ModeUnknown, model.ModeRemote},
		{"Backend Engineer", "Pune", "This role is on-site.", model.ModeUnknown, model.ModeOnsite},
		{"Backend Engineer (Remote)", "", "", model.ModeOnsite, model.ModeOnsite}, // source wins
	}
	for _, tt := range tests {
		j := model.Job{Title: tt.title, Location: tt.loc, Description: tt.desc, WorkMode: tt.source}
		c.Classify(&j)
		if j.WorkMode != tt.want {
			t.Errorf("%q/%q/%q: mode = %s, want %s", tt.title, tt.loc, tt.desc, j.WorkMode, tt.want)
		}
	}
}

func TestAreas(t *testing.T) {
	c := Default()
	tests := []struct {
		title, desc string
		want        []model.Area
		generic     bool
	}{
		{"Backend Engineer", "", []model.Area{model.AreaBackend}, false},
		{"Go Developer Intern", "", []model.Area{model.AreaBackend}, false},
		{"CUDA Kernel Engineer", "", []model.Area{model.AreaSystems, model.AreaHPC}, false},
		{"Android Developer", "", []model.Area{model.AreaMobile}, false},
		{"Full Stack Developer", "", []model.Area{model.AreaFullStack}, false},
		{"Machine Learning Intern", "", []model.Area{model.AreaMLAI}, false},
		{"Software Engineer Intern", "Build REST APIs with Go, PostgreSQL and Kafka.", []model.Area{model.AreaBackend}, false},
		{"Software Engineer Intern", "Join our team!", nil, true},
		{"Account Executive", "Sell software.", nil, false},
	}
	for _, tt := range tests {
		j := model.Job{Title: tt.title, Description: tt.desc}
		c.Classify(&j)
		if !slices.Equal(j.Areas, tt.want) {
			t.Errorf("%q: areas = %v, want %v", tt.title, j.Areas, tt.want)
		}
		if hasTag(j.Tags, GenericTag) != tt.generic {
			t.Errorf("%q: generic tag = %v, want %v", tt.title, !tt.generic, tt.generic)
		}
		if tt.generic && !c.MatchesArea(&j, model.AreaBackend) {
			t.Errorf("%q: generic SWE should match backend", tt.title)
		}
		if tt.generic && c.MatchesArea(&j, model.AreaMobile) {
			t.Errorf("%q: generic SWE should not match mobile", tt.title)
		}
	}
}

func TestVisa(t *testing.T) {
	c := Default()
	yes, no := true, false
	tests := []struct {
		desc string
		want *bool
	}{
		{"We offer visa sponsorship and relocation support.", &yes},
		{"Unfortunately we are unable to sponsor visas.", &no},
		{"No visa sponsorship available.", &no},
		{"Great snacks.", nil},
	}
	for _, tt := range tests {
		j := model.Job{Title: "Engineer", Description: tt.desc}
		c.Classify(&j)
		if (j.VisaSponsorship == nil) != (tt.want == nil) || (tt.want != nil && *j.VisaSponsorship != *tt.want) {
			t.Errorf("%q: visa = %v, want %v", tt.desc, j.VisaSponsorship, tt.want)
		}
	}
}

func TestFromYAMLRejectsUnknownArea(t *testing.T) {
	if _, err := FromYAML([]byte("areas:\n  basket-weaving: [baskets]\n")); err == nil {
		t.Error("expected error for unknown area")
	}
}

func TestRequiresExperience(t *testing.T) {
	c := Default()
	tests := []struct {
		desc string
		want bool
	}{
		{"Candidates should have prior internship experience in backend development.", true},
		{"You have 1+ years of experience building APIs in Go.", true},
		{"2-3 years of relevant industry experience with Kubernetes.", true},
		{"Requires at least one internship at a product company.", true},
		{"No prior experience needed... we will train you. Freshers welcome!", false},
		{"0-1 years of experience. Pursuing a 4 year degree in CS.", false},
		{"Experience with Go or Rust is a plus.", false},
		{"Final-year students graduating in 2027. 3 months, stipend provided.", false},
		{"No prior work experience required; 2+ years of coursework in systems.", false},
	}
	for _, tt := range tests {
		j := model.Job{Title: "Backend Intern", Description: tt.desc}
		c.Classify(&j)
		if got := slices.Contains(j.Tags, RequiresExperienceTag); got != tt.want {
			t.Errorf("%q: requires experience = %v, want %v", tt.desc, got, tt.want)
		}
	}
}

// Pay phrases as real postings write them, and amounts that aren't pay.
func TestPay(t *testing.T) {
	c := Default()
	tests := []struct{ title, desc, want string }{
		{"PPC Intern", "Shift: 11 am – 8 pm. Stipend: ₹12,000/month. Duration: 6 months.", "₹12k/mo"},
		{"Content Intern", "Performance-based stipend of₹5,000 – ₹8,000 per month.", "₹5k–8k/mo"},
		{"Apprentice", "Monday to Friday. Monthly stipend: 32,000. Location: remote.", "32k/mo"},
		{"Backend Intern", "Stipend: ranges between 20,000 to 30,000 per month based on interview performance.", "20k–30k/mo"},
		{"Sales Manager", "Location: Gurgaon. CTC upto 15 LPA.", "₹15L/yr"},
		{"Data Intern", "The base pay range for this opportunity in the U.S. is $35.34 - $41.20 per hour.", "$35.34–41.20/hr"},
		{"Engineer", "The annual base salary range for this role is: $180,000—$270,000 USD.", "$180k–270k/yr"},
		{"Engineer", "The estimated annual cash salary for this role is CAD $123,750 - CAD $165,000.", "CAD 124k–165k/yr"},
		{"Fellow", "Weekly stipend of 3,850 USD / 2,310 GBP / 4,300 CAD + benefits.", "$3.9k/wk"},
		{"Shift Lead", "Benefits: 401k match, gym membership reimbursement (up to $75/month).", ""},
		{"Engineer", "We offer a learning stipend of $1,500 per year and great snacks.", ""},
		{"Engineer", "Typical deals will be $20k to $100k. Unpaid leave is available.", ""},
		{"Social Media Intern", "This internship is unpaid.", "unpaid"},
		{"HR Intern", "3 months full-time internship without stipend.", "unpaid"},
		{"SDE Intern", "Is it a paid/unpaid internship? It is a paid internship.", "paid"},
		{"Research Intern", "A monthly stipend will be paid.", "paid"},
		{"Backend Intern (Unpaid)", "Build APIs in Go.", "unpaid"},
		{"Engineer", "Salary: $100–150k/year, plus equity.", "$100k–150k/yr"},
		{"Engineer", "A monthly stipend will be paid.", ""}, // only internships say "paid" without an amount
	}
	for _, tt := range tests {
		j := model.Job{Title: tt.title, Description: tt.desc}
		c.Classify(&j)
		if got := j.Pay(); got != tt.want {
			t.Errorf("%q: pay %q, want %q", tt.desc, got, tt.want)
		}
	}
	// A source's own pay wins over the description.
	j := model.Job{Title: "Intern", Description: "This internship is unpaid.", SalaryMin: new(40.0), Currency: "USD", PayPeriod: model.PerHour}
	if c.Classify(&j); j.Pay() != "$40/hr" {
		t.Errorf("source pay replaced: %q", j.Pay())
	}
}
