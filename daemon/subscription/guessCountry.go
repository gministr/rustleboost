package subscription

import "strings"

// guessCountry extracts country and flag from a server name/host.
// Priority: flag emoji at start of name > keyword search in name+host.
func guessCountry(name, host string) (string, string) {
	// 1. If name starts with a flag emoji (two regional indicator chars), use it directly
	runes := []rune(name)
	if len(runes) >= 2 {
		r0, r1 := runes[0], runes[1]
		if r0 >= 0x1F1E6 && r0 <= 0x1F1FF && r1 >= 0x1F1E6 && r1 <= 0x1F1FF {
			flag := string(runes[:2])
			letterA := rune(r0-0x1F1E6) + 'A'
			letterB := rune(r1-0x1F1E6) + 'A'
			code := strings.ToLower(string([]rune{letterA, letterB}))
			if country, ok := codeToCountry[code]; ok {
				return country, flag
			}
			return strings.ToUpper(code), flag
		}
	}

	// 2. Keyword search — use word-boundary matching for short codes to avoid
	// false positives like "us" matching "ruslte"
	search := strings.ToLower(name + " " + host)
	for _, entry := range countryEntries {
		for _, kw := range entry.keywords {
			if len(kw) <= 2 {
				if containsWord(search, kw) {
					return entry.country, entry.flag
				}
			} else if strings.Contains(search, kw) {
				return entry.country, entry.flag
			}
		}
	}
	return "Unknown", "\U0001F310" // 🌐
}

// containsWord checks if s contains kw as a whole word (surrounded by non-letters or boundaries).
func containsWord(s, kw string) bool {
	idx := 0
	for {
		i := strings.Index(s[idx:], kw)
		if i < 0 {
			return false
		}
		pos := idx + i
		// Check left boundary
		leftOK := pos == 0 || !isAlpha(rune(s[pos-1]))
		// Check right boundary
		end := pos + len(kw)
		rightOK := end >= len(s) || !isAlpha(rune(s[end]))
		if leftOK && rightOK {
			return true
		}
		idx = pos + 1
		if idx >= len(s) {
			return false
		}
	}
}

func isAlpha(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// codeToCountry maps ISO 2-letter code → English country name
var codeToCountry = map[string]string{
	"eu": "European Union", "es": "Spain", "pt": "Portugal", "be": "Belgium",
	"ie": "Ireland", "gr": "Greece", "rs": "Serbia", "sk": "Slovakia",
	"si": "Slovenia", "is": "Iceland", "lu": "Luxembourg", "cy": "Cyprus",
	"ge": "Georgia", "uz": "Uzbekistan", "kg": "Kyrgyzstan", "tw": "Taiwan",
	"ru": "Russia", "nl": "Netherlands", "de": "Germany", "pl": "Poland",
	"fi": "Finland", "at": "Austria", "us": "USA", "gb": "UK",
	"fr": "France", "se": "Sweden", "tr": "Turkey", "jp": "Japan",
	"sg": "Singapore", "ua": "Ukraine", "cz": "Czechia", "ch": "Switzerland",
	"it": "Italy", "ca": "Canada", "au": "Australia", "nz": "New Zealand",
	"no": "Norway", "dk": "Denmark", "hu": "Hungary", "ro": "Romania",
	"lt": "Lithuania", "lv": "Latvia", "ee": "Estonia", "am": "Armenia",
	"az": "Azerbaijan", "kz": "Kazakhstan", "md": "Moldova", "bg": "Bulgaria",
	"hr": "Croatia", "kr": "South Korea", "hk": "Hong Kong", "cn": "China",
	"in": "India", "br": "Brazil", "mx": "Mexico", "ar": "Argentina",
	"za": "South Africa", "ng": "Nigeria", "eg": "Egypt", "ae": "UAE",
	"sa": "Saudi Arabia", "il": "Israel", "th": "Thailand", "vn": "Vietnam",
	"id": "Indonesia", "my": "Malaysia", "ph": "Philippines", "pk": "Pakistan",
}

// countryEntries for keyword-based fallback
var countryEntries = []struct {
	keywords []string
	country  string
	flag     string
}{
	{[]string{"netherlands", "amsterdam", "ams"}, "Netherlands", "\U0001F1F3\U0001F1F1"},
	{[]string{"germany", "deutschland", "frankfurt"}, "Germany", "\U0001F1E9\U0001F1EA"},
	{[]string{"united states", "new york", "los angeles", "dallas"}, "USA", "\U0001F1FA\U0001F1F8"},
	{[]string{"united kingdom", "britain", "london"}, "UK", "\U0001F1EC\U0001F1E7"},
	{[]string{"france", "paris"}, "France", "\U0001F1EB\U0001F1F7"},
	{[]string{"finland", "helsinki"}, "Finland", "\U0001F1EB\U0001F1EE"},
	{[]string{"sweden", "stockholm"}, "Sweden", "\U0001F1F8\U0001F1EA"},
	{[]string{"norway", "oslo"}, "Norway", "\U0001F1F3\U0001F1F4"},
	{[]string{"switzerland", "zurich"}, "Switzerland", "\U0001F1E8\U0001F1ED"},
	{[]string{"poland", "warsaw"}, "Poland", "\U0001F1F5\U0001F1F1"},
	{[]string{"turkey", "istanbul"}, "Turkey", "\U0001F1F9\U0001F1F7"},
	{[]string{"singapore"}, "Singapore", "\U0001F1F8\U0001F1EC"},
	{[]string{"japan", "tokyo"}, "Japan", "\U0001F1EF\U0001F1F5"},
	{[]string{"south korea", "seoul"}, "South Korea", "\U0001F1F0\U0001F1F7"},
	{[]string{"hong kong", "hongkong"}, "Hong Kong", "\U0001F1ED\U0001F1F0"},
	{[]string{"australia", "sydney"}, "Australia", "\U0001F1E6\U0001F1FA"},
	{[]string{"canada", "toronto"}, "Canada", "\U0001F1E8\U0001F1E6"},
	{[]string{"russia", "moscow"}, "Russia", "\U0001F1F7\U0001F1FA"},
	{[]string{"ukraine", "kyiv"}, "Ukraine", "\U0001F1FA\U0001F1E6"},
	{[]string{"austria", "vienna"}, "Austria", "\U0001F1E6\U0001F1F9"},
}
