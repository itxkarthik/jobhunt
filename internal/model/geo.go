package model

import (
	"regexp"
	"slices"
	"sort"
	"strings"
)

// RemoteGlobal marks a remote job open to candidates anywhere.
const RemoteGlobal = "remote-global"

// Region codes appear in Job.Countries when a remote job is restricted to a
// region rather than a list of countries.
const (
	RegionAPAC     = "region:apac"
	RegionAsia     = "region:asia"
	RegionEurope   = "region:europe"
	RegionEMEA     = "region:emea"
	RegionAmericas = "region:americas"
	RegionNA       = "region:north-america"
)

// FilterCountries are the codes accepted by --country.
var FilterCountries = []string{"IN", "US", "JP", "GB", "DE", "CA", "SG", "NL", "AU", "AE", RemoteGlobal}

var regionMembers = map[string][]string{
	RegionAPAC:     {"IN", "JP", "SG", "AU", "NZ", "KR", "CN", "HK", "TW", "ID", "MY", "PH", "TH", "VN", "PK", "BD", "LK"},
	RegionAsia:     {"IN", "JP", "SG", "KR", "CN", "HK", "TW", "ID", "MY", "PH", "TH", "VN", "AE", "PK", "BD", "LK"},
	RegionEurope:   {"GB", "DE", "NL", "FR", "IE", "ES", "PT", "IT", "PL", "SE", "DK", "NO", "FI", "CH", "AT", "BE", "CZ", "RO"},
	RegionEMEA:     {"GB", "DE", "NL", "FR", "IE", "ES", "PT", "IT", "PL", "SE", "DK", "NO", "FI", "CH", "AT", "BE", "CZ", "RO", "AE", "IL", "ZA", "EG", "NG", "KE"},
	RegionAmericas: {"US", "CA", "MX", "BR", "AR", "CO", "CL"},
	RegionNA:       {"US", "CA", "MX"},
}

// RegionContains reports whether region code r includes country c.
func RegionContains(r, c string) bool {
	return slices.Contains(regionMembers[r], c)
}

// CurrencyFor returns the local currency for a country code.
func CurrencyFor(country string) string {
	switch country {
	case "IN":
		return "INR"
	case "US":
		return "USD"
	case "GB":
		return "GBP"
	case "DE", "NL", "FR", "IE", "ES", "IT", "AT", "BE":
		return "EUR"
	case "CA":
		return "CAD"
	case "SG":
		return "SGD"
	case "AU":
		return "AUD"
	case "AE":
		return "AED"
	case "JP":
		return "JPY"
	}
	return ""
}

// NormalizeCountry maps user input ("uk", "India", "remote") to a filter code.
func NormalizeCountry(s string) (string, bool) {
	u := strings.ToUpper(strings.TrimSpace(s))
	switch u {
	case "UK":
		return "GB", true
	case "REMOTE-GLOBAL", "REMOTE", "GLOBAL", "ANYWHERE", "WORLDWIDE":
		return RemoteGlobal, true
	case "UAE":
		return "AE", true
	case "USA":
		return "US", true
	}
	if len(u) == 2 {
		return u, true
	}
	if c, ok := countryNames[strings.ToLower(s)]; ok {
		return c, true
	}
	return "", false
}

// countryNames maps lowercase names/aliases to ISO codes.
var countryNames = map[string]string{
	"india": "IN", "bharat": "IN",
	"united states": "US", "united states of america": "US", "usa": "US", "u.s.": "US", "america": "US",
	"japan":          "JP",
	"united kingdom": "GB", "uk": "GB", "great britain": "GB", "england": "GB", "scotland": "GB", "britain": "GB",
	"germany": "DE", "deutschland": "DE",
	"canada":      "CA",
	"singapore":   "SG",
	"netherlands": "NL", "the netherlands": "NL", "holland": "NL",
	"australia":            "AU",
	"united arab emirates": "AE", "uae": "AE",
	"france": "FR", "ireland": "IE", "spain": "ES", "portugal": "PT", "italy": "IT", "poland": "PL",
	"sweden": "SE", "denmark": "DK", "norway": "NO", "finland": "FI", "switzerland": "CH",
	"austria": "AT", "belgium": "BE", "czech republic": "CZ", "czechia": "CZ", "romania": "RO",
	"israel": "IL", "south africa": "ZA", "egypt": "EG", "nigeria": "NG", "kenya": "KE",
	"mexico": "MX", "brazil": "BR", "argentina": "AR", "colombia": "CO", "chile": "CL",
	"new zealand": "NZ", "south korea": "KR", "korea": "KR", "china": "CN", "hong kong": "HK",
	"taiwan": "TW", "indonesia": "ID", "malaysia": "MY", "philippines": "PH", "thailand": "TH",
	"vietnam": "VN", "pakistan": "PK", "bangladesh": "BD", "sri lanka": "LK",
}

// cities maps lowercase city names to (canonical name, country).
var cities = map[string][2]string{
	"bengaluru": {"Bengaluru", "IN"}, "bangalore": {"Bengaluru", "IN"}, "mumbai": {"Mumbai", "IN"},
	"pune": {"Pune", "IN"}, "hyderabad": {"Hyderabad", "IN"}, "chennai": {"Chennai", "IN"},
	"gurgaon": {"Gurugram", "IN"}, "gurugram": {"Gurugram", "IN"}, "noida": {"Noida", "IN"},
	"new delhi": {"Delhi", "IN"}, "delhi": {"Delhi", "IN"}, "kolkata": {"Kolkata", "IN"},
	"ahmedabad": {"Ahmedabad", "IN"}, "kochi": {"Kochi", "IN"}, "thiruvananthapuram": {"Thiruvananthapuram", "IN"},
	"trivandrum": {"Thiruvananthapuram", "IN"}, "coimbatore": {"Coimbatore", "IN"}, "jaipur": {"Jaipur", "IN"},
	"chandigarh": {"Chandigarh", "IN"}, "indore": {"Indore", "IN"},
	"bombay": {"Mumbai", "IN"}, "navi mumbai": {"Navi Mumbai", "IN"}, "thane": {"Thane", "IN"},
	"madras": {"Chennai", "IN"}, "calcutta": {"Kolkata", "IN"}, "poona": {"Pune", "IN"},
	"cochin": {"Kochi", "IN"}, "ernakulam": {"Kochi", "IN"}, "kozhikode": {"Kozhikode", "IN"}, "calicut": {"Kozhikode", "IN"},
	"thrissur": {"Thrissur", "IN"}, "kannur": {"Kannur", "IN"}, "mysuru": {"Mysuru", "IN"}, "mysore": {"Mysuru", "IN"},
	"mangaluru": {"Mangaluru", "IN"}, "mangalore": {"Mangaluru", "IN"}, "hubballi": {"Hubballi", "IN"}, "hubli": {"Hubballi", "IN"},
	"visakhapatnam": {"Visakhapatnam", "IN"}, "vizag": {"Visakhapatnam", "IN"}, "vijayawada": {"Vijayawada", "IN"},
	"vadodara": {"Vadodara", "IN"}, "baroda": {"Vadodara", "IN"}, "surat": {"Surat", "IN"}, "nagpur": {"Nagpur", "IN"},
	"lucknow": {"Lucknow", "IN"}, "bhubaneswar": {"Bhubaneswar", "IN"}, "bhopal": {"Bhopal", "IN"},
	"madurai": {"Madurai", "IN"}, "tiruchirappalli": {"Tiruchirappalli", "IN"}, "trichy": {"Tiruchirappalli", "IN"},
	"puducherry": {"Puducherry", "IN"}, "pondicherry": {"Puducherry", "IN"}, "patna": {"Patna", "IN"},
	"tokyo": {"Tokyo", "JP"}, "osaka": {"Osaka", "JP"}, "kyoto": {"Kyoto", "JP"}, "fukuoka": {"Fukuoka", "JP"},
	"yokohama": {"Yokohama", "JP"}, "nagoya": {"Nagoya", "JP"},
	"san francisco": {"San Francisco", "US"}, "new york": {"New York", "US"}, "nyc": {"New York", "US"},
	"seattle": {"Seattle", "US"}, "austin": {"Austin", "US"}, "boston": {"Boston", "US"},
	"los angeles": {"Los Angeles", "US"}, "chicago": {"Chicago", "US"}, "mountain view": {"Mountain View", "US"},
	"palo alto": {"Palo Alto", "US"}, "sunnyvale": {"Sunnyvale", "US"}, "san jose": {"San Jose", "US"},
	"menlo park": {"Menlo Park", "US"}, "denver": {"Denver", "US"}, "atlanta": {"Atlanta", "US"},
	"washington, dc": {"Washington", "US"}, "san diego": {"San Diego", "US"},
	"london": {"London", "GB"}, "manchester": {"Manchester", "GB"}, "edinburgh": {"Edinburgh", "GB"},
	"cambridge, uk": {"Cambridge", "GB"},
	"berlin":        {"Berlin", "DE"}, "munich": {"Munich", "DE"}, "münchen": {"Munich", "DE"}, "hamburg": {"Hamburg", "DE"},
	"frankfurt": {"Frankfurt", "DE"}, "cologne": {"Cologne", "DE"}, "köln": {"Cologne", "DE"}, "stuttgart": {"Stuttgart", "DE"},
	"toronto": {"Toronto", "CA"}, "vancouver": {"Vancouver", "CA"}, "montreal": {"Montreal", "CA"}, "waterloo": {"Waterloo", "CA"},
	"amsterdam": {"Amsterdam", "NL"}, "rotterdam": {"Rotterdam", "NL"}, "utrecht": {"Utrecht", "NL"}, "eindhoven": {"Eindhoven", "NL"},
	"sydney": {"Sydney", "AU"}, "melbourne": {"Melbourne", "AU"}, "brisbane": {"Brisbane", "AU"},
	"dubai": {"Dubai", "AE"}, "abu dhabi": {"Abu Dhabi", "AE"},
	"dublin": {"Dublin", "IE"}, "paris": {"Paris", "FR"},
}

var regionNames = map[string]string{
	"worldwide": RemoteGlobal, "anywhere": RemoteGlobal, "global": RemoteGlobal, "anywhere in the world": RemoteGlobal,
	"apac": RegionAPAC, "asia pacific": RegionAPAC, "asia-pacific": RegionAPAC,
	"asia":   RegionAsia,
	"europe": RegionEurope, "eu": RegionEurope, "european union": RegionEurope,
	"emea":     RegionEMEA,
	"americas": RegionAmericas, "latam": RegionAmericas, "latin america": RegionAmericas,
	"north america": RegionNA,
}

type geoKey struct {
	re   *regexp.Regexp
	kind int // 0 country, 1 city, 2 region
	key  string
}

var geoKeys = buildGeoKeys()

func buildGeoKeys() []geoKey {
	var ks []geoKey
	add := func(m map[string]string, kind int) {
		for k := range m {
			ks = append(ks, geoKey{kind: kind, key: k})
		}
	}
	add(countryNames, 0)
	for k := range cities {
		ks = append(ks, geoKey{kind: 1, key: k})
	}
	add(regionNames, 2)
	// Longest first so "new delhi" wins over "delhi", "united states of america" over "america".
	sort.Slice(ks, func(i, j int) bool {
		if len(ks[i].key) != len(ks[j].key) {
			return len(ks[i].key) > len(ks[j].key)
		}
		return ks[i].key < ks[j].key
	})
	for i := range ks {
		ks[i].re = regexp.MustCompile(`(^|[^\p{L}])` + regexp.QuoteMeta(ks[i].key) + `($|[^\p{L}])`)
	}
	return ks
}

// Uppercase-only abbreviations; matched case-sensitively so "us" in prose is ignored.
var upperCodes = regexp.MustCompile(`\b(USA|US|UK|UAE|EU)\b`)

// ParseLocation extracts country/region codes and the first recognized city
// from free-form location text. Unrecognized text yields no countries.
func ParseLocation(s string) (countries []string, city string) {
	if strings.TrimSpace(s) == "" {
		return nil, ""
	}
	low := strings.ToLower(s)
	seen := map[string]bool{}
	addC := func(c string) {
		if c != "" && !seen[c] {
			seen[c] = true
			countries = append(countries, c)
		}
	}
	consumed := low
	for _, k := range geoKeys {
		if !k.re.MatchString(consumed) {
			continue
		}
		// Blank out the match so "new delhi" doesn't also match "delhi".
		consumed = k.re.ReplaceAllString(consumed, "$1 $2")
		switch k.kind {
		case 0:
			addC(countryNames[k.key])
		case 1:
			c := cities[k.key]
			if city == "" {
				city = c[0]
			}
			addC(c[1])
		case 2:
			addC(regionNames[k.key])
		}
	}
	for _, m := range upperCodes.FindAllString(s, -1) {
		switch m {
		case "USA", "US":
			addC("US")
		case "UK":
			addC("GB")
		case "UAE":
			addC("AE")
		case "EU":
			addC(RegionEurope)
		}
	}
	return countries, city
}

// CountryAllows reports whether a job tagged with codes is available to
// someone in country c. Remote jobs open worldwide or to a region containing
// c count unless strict is set.
func CountryAllows(codes []string, c string, remote, strict bool) bool {
	for _, x := range codes {
		if x == c {
			return true
		}
		if strict || !remote {
			continue
		}
		if x == RemoteGlobal || RegionContains(x, c) {
			return true
		}
	}
	return false
}
