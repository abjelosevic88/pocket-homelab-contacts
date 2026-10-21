package vcard

import (
	"reflect"
	"regexp"
	"strings"
	"time"
)

// Field is one labelled value: a phone, an email, a web address…
// Label is a known name (mobile, home, work, other, iphone, main, pager,
// home fax, work fax, other fax, homepage, …) or the person's own words.
type Field struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Pref  bool   `json:"pref,omitempty"`
}

// Address is one postal address.
type Address struct {
	Label    string `json:"label"`
	Street   string `json:"street"`
	Ext      string `json:"ext,omitempty"`
	POBox    string `json:"pobox,omitempty"`
	City     string `json:"city"`
	Region   string `json:"region"`
	Postcode string `json:"postcode"`
	Country  string `json:"country"`
	Pref     bool   `json:"pref,omitempty"`
}

// Contact is the structured view of a card the app edits.
type Contact struct {
	Kind           string    `json:"kind"` // individual | org | group
	FN             string    `json:"fn"`
	Prefix         string    `json:"prefix"`
	Given          string    `json:"given"`
	Middle         string    `json:"middle"`
	Family         string    `json:"family"`
	Suffix         string    `json:"suffix"`
	Nickname       string    `json:"nickname"`
	PhoneticGiven  string    `json:"phonetic_given"`
	PhoneticFamily string    `json:"phonetic_family"`
	Org            string    `json:"org"`
	Department     string    `json:"department"`
	Title          string    `json:"title"`
	Emails         []Field   `json:"emails"`
	Phones         []Field   `json:"phones"`
	Addresses      []Address `json:"addresses"`
	URLs           []Field   `json:"urls"`
	IMs            []Field   `json:"ims"`         // label = service (skype, telegram, …), value = handle
	Socials        []Field   `json:"socials"`     // label = network, value = profile address or handle
	Birthday       string    `json:"birthday"`    // YYYY-MM-DD or --MM-DD
	Anniversary    string    `json:"anniversary"` // YYYY-MM-DD or --MM-DD
	Dates          []Field   `json:"dates"`       // other dates, value YYYY-MM-DD or --MM-DD
	Related        []Field   `json:"related"`     // label = relation (mother, spouse, …), value = name
	Note           string    `json:"note"`
	Categories     []string  `json:"categories"`
	Members        []string  `json:"members,omitempty"` // group cards: member UIDs
}

// ---------- labels ----------

var appleLabel = regexp.MustCompile(`^_\$!<(.+)>!\$_$`)

var appleNames = map[string]string{
	"mobile": "Mobile", "home": "Home", "work": "Work", "other": "Other", "main": "Main", "pager": "Pager",
	"home fax": "HomeFAX", "work fax": "WorkFAX", "other fax": "OtherFAX", "homepage": "HomePage",
	"anniversary": "Anniversary", "mother": "Mother", "father": "Father", "parent": "Parent", "brother": "Brother",
	"sister": "Sister", "child": "Child", "friend": "Friend", "spouse": "Spouse", "partner": "Partner",
	"assistant": "Assistant", "manager": "Manager", "school": "School",
}

// fromApple turns "_$!<HomeFAX>!$_" into "home fax"; other text is kept.
func fromApple(l string) string {
	m := appleLabel.FindStringSubmatch(strings.TrimSpace(l))
	if m == nil {
		return strings.TrimSpace(l)
	}
	for k, v := range appleNames {
		if strings.EqualFold(v, m[1]) {
			return k
		}
	}
	return strings.ToLower(m[1])
}

func toApple(l string) string {
	if v, ok := appleNames[strings.ToLower(l)]; ok {
		return "_$!<" + v + ">!$_"
	}
	return l
}

// groupLabel is the X-ABLabel of a property's group ("" when none).
func (c *Card) groupLabel(p *Property) string {
	for _, q := range c.GroupProps(p) {
		if q.Name == "X-ABLABEL" {
			return fromApple(q.Text())
		}
	}
	return ""
}

func isPref(p *Property) bool {
	if p.HasType("pref") {
		return true
	}
	if v := p.Param("PREF"); v != "" && v != "0" {
		return true
	}
	return false
}

func phoneLabel(p *Property) string {
	fax := p.HasType("fax")
	switch {
	case fax && p.HasType("home"):
		return "home fax"
	case fax && p.HasType("work"):
		return "work fax"
	case fax:
		return "other fax"
	case p.HasType("iphone"):
		return "iphone"
	case p.HasType("cell") || p.HasType("mobile"):
		return "mobile"
	case p.HasType("main"):
		return "main"
	case p.HasType("pager"):
		return "pager"
	case p.HasType("home"):
		return "home"
	case p.HasType("work"):
		return "work"
	case p.HasType("car"):
		return "car"
	}
	return "other"
}

func simpleLabel(p *Property, def string) string {
	for _, t := range []string{"home", "work", "other", "school"} {
		if p.HasType(t) {
			return t
		}
	}
	for _, t := range p.Types() {
		switch t {
		case "internet", "pref", "voice", "x400", "postal", "parcel", "dom", "intl":
			continue
		default:
			return t
		}
	}
	return def
}

// ---------- dates ----------

var dateDigits = regexp.MustCompile(`^(\d{4}|--)-?(\d{2})-?(\d{2})`)

// NormDate reads the date forms found in the wild (19880503, 1988-05-03,
// 1988-05-03T00:00:00Z, --0503, --05-03, Apple's 1604-05-03 for "no year")
// and returns YYYY-MM-DD or --MM-DD ("" when unreadable).
func NormDate(v string, omitYear bool) string {
	v = strings.TrimSpace(v)
	m := dateDigits.FindStringSubmatch(v)
	if m == nil {
		return ""
	}
	y, mo, d := m[1], m[2], m[3]
	if mo < "01" || mo > "12" || d < "01" || d > "31" {
		return ""
	}
	if y == "--" || y == "1604" || y == "0000" || omitYear {
		return "--" + mo + "-" + d
	}
	return y + "-" + mo + "-" + d
}

func (c *Card) dateProp(p *Property) string {
	omit := p.Param("X-APPLE-OMIT-YEAR") != ""
	return NormDate(p.Value, omit)
}

// writeDate stores a date the way the card's version wants it.
func writeDate(p *Property, v, version string) {
	if strings.HasPrefix(v, "--") {
		md := strings.ReplaceAll(v[2:], "-", "")
		if version == "4.0" {
			p.Value = "--" + md
			return
		}
		p.SetParam("X-APPLE-OMIT-YEAR", "1604")
		p.Value = "1604-" + md[:2] + "-" + md[2:]
		return
	}
	p.Value = v
}

// ---------- IM ----------

var imProps = map[string]string{
	"X-AIM": "aim", "X-JABBER": "jabber", "X-SKYPE": "skype", "X-SKYPE-USERNAME": "skype", "X-ICQ": "icq",
	"X-MSN": "msn", "X-YAHOO": "yahoo", "X-QQ": "qq", "X-GOOGLE-TALK": "google talk", "X-GTALK": "google talk",
	"X-TWITTER": "twitter",
}

var imSchemes = map[string]string{
	"skype": "skype", "aim": "aim", "jabber": "xmpp", "xmpp": "xmpp", "google talk": "xmpp", "facebook": "xmpp",
	"icq": "aim", "msn": "msnim", "yahoo": "ymsgr", "sip": "sip", "telegram": "x-apple", "signal": "x-apple",
	"whatsapp": "x-apple", "viber": "x-apple", "matrix": "matrix",
}

func imFromImpp(p *Property) Field {
	v := p.Text()
	svc := strings.ToLower(p.Param("X-SERVICE-TYPE"))
	scheme, handle, ok := strings.Cut(v, ":")
	if !ok {
		handle = v
	} else if svc == "" {
		switch strings.ToLower(scheme) {
		case "xmpp":
			svc = "jabber"
		case "msnim":
			svc = "msn"
		case "ymsgr":
			svc = "yahoo"
		case "x-apple":
			svc = "other"
		default:
			svc = strings.ToLower(scheme)
		}
	}
	if svc == "" {
		svc = "other"
	}
	return Field{Label: svc, Value: handle, Pref: isPref(p)}
}

// ---------- Card → Contact ----------

// Contact reads the structured view of the card.
func (c *Card) Contact() Contact {
	ct := Contact{Kind: "individual"}
	if k := strings.ToLower(c.Value("KIND") + c.Value("X-ADDRESSBOOKSERVER-KIND")); strings.Contains(k, "group") {
		ct.Kind = "group"
	} else if strings.Contains(k, "org") || strings.EqualFold(c.Value("X-ABSHOWAS"), "COMPANY") {
		ct.Kind = "org"
	}
	ct.FN = c.Value("FN")
	if n := c.Get("N"); n != nil {
		parts := append(n.Components(), "", "", "", "", "")
		ct.Family, ct.Given, ct.Middle, ct.Prefix, ct.Suffix = parts[0], parts[1], parts[2], parts[3], parts[4]
	}
	if p := c.Get("NICKNAME"); p != nil {
		ct.Nickname = strings.Join(p.List(), ", ")
	}
	ct.PhoneticGiven = c.Value("X-PHONETIC-FIRST-NAME")
	ct.PhoneticFamily = c.Value("X-PHONETIC-LAST-NAME")
	if o := c.Get("ORG"); o != nil {
		parts := append(o.Components(), "", "")
		ct.Org = parts[0]
		ct.Department = strings.Join(nonEmpty(parts[1:]), ", ")
	}
	ct.Title = c.Value("TITLE")
	ct.Note = c.Value("NOTE")
	for _, p := range c.All("CATEGORIES") {
		ct.Categories = appendUnique(ct.Categories, p.List()...)
	}
	for _, p := range c.All("EMAIL") {
		l := c.groupLabel(p)
		if l == "" {
			l = simpleLabel(p, "other")
		}
		if v := strings.TrimSpace(p.Text()); v != "" {
			ct.Emails = append(ct.Emails, Field{Label: l, Value: strings.TrimPrefix(v, "mailto:"), Pref: isPref(p)})
		}
	}
	for _, p := range c.All("TEL") {
		l := c.groupLabel(p)
		if l == "" {
			l = phoneLabel(p)
		}
		if v := strings.TrimSpace(strings.TrimPrefix(p.Text(), "tel:")); v != "" {
			ct.Phones = append(ct.Phones, Field{Label: l, Value: v, Pref: isPref(p)})
		}
	}
	for _, p := range c.All("ADR") {
		parts := append(p.Components(), "", "", "", "", "", "", "")
		l := c.groupLabel(p)
		if l == "" {
			l = simpleLabel(p, "other")
		}
		a := Address{Label: l, POBox: parts[0], Ext: parts[1], Street: parts[2], City: parts[3], Region: parts[4], Postcode: parts[5], Country: parts[6], Pref: isPref(p)}
		if a.Street+a.City+a.Region+a.Postcode+a.Country+a.POBox+a.Ext != "" {
			ct.Addresses = append(ct.Addresses, a)
		}
	}
	for _, p := range c.All("URL") {
		l := c.groupLabel(p)
		if l == "" {
			l = simpleLabel(p, "homepage")
		}
		if v := strings.TrimSpace(p.Text()); v != "" {
			ct.URLs = append(ct.URLs, Field{Label: l, Value: v, Pref: isPref(p)})
		}
	}
	for _, p := range c.Props {
		if p.Name == "IMPP" {
			if f := imFromImpp(p); f.Value != "" {
				ct.IMs = appendField(ct.IMs, f)
			}
		} else if svc, ok := imProps[p.Name]; ok {
			if v := strings.TrimSpace(p.Text()); v != "" {
				ct.IMs = appendField(ct.IMs, Field{Label: svc, Value: v})
			}
		}
	}
	for _, p := range c.All("X-SOCIALPROFILE") {
		l := strings.ToLower(p.Param("TYPE"))
		if l == "" {
			l = "other"
		}
		v := strings.TrimSpace(p.Text())
		if v == "" || strings.HasPrefix(v, "x-apple:") {
			if u := p.Param("X-USER"); u != "" {
				v = u
			}
		}
		if v != "" {
			ct.Socials = append(ct.Socials, Field{Label: l, Value: v})
		}
	}
	if p := c.Get("BDAY"); p != nil {
		ct.Birthday = c.dateProp(p)
	}
	for _, name := range []string{"ANNIVERSARY", "X-ANNIVERSARY"} {
		if p := c.Get(name); p != nil && ct.Anniversary == "" {
			ct.Anniversary = c.dateProp(p)
		}
	}
	for _, p := range c.All("X-ABDATE") {
		d := c.dateProp(p)
		if d == "" {
			continue
		}
		l := c.groupLabel(p)
		if l == "anniversary" {
			if ct.Anniversary == "" {
				ct.Anniversary = d
			}
			continue
		}
		if l == "" {
			l = "other"
		}
		ct.Dates = append(ct.Dates, Field{Label: l, Value: d})
	}
	for _, p := range c.All("X-ABRELATEDNAMES") {
		l := c.groupLabel(p)
		if l == "" {
			l = simpleLabel(p, "other")
		}
		if v := strings.TrimSpace(p.Text()); v != "" {
			ct.Related = append(ct.Related, Field{Label: l, Value: v})
		}
	}
	for _, p := range c.All("RELATED") {
		if v := strings.TrimSpace(p.Text()); v != "" {
			ct.Related = appendField(ct.Related, Field{Label: simpleLabel(p, "other"), Value: v})
		}
	}
	for _, name := range []string{"X-ADDRESSBOOKSERVER-MEMBER", "MEMBER"} {
		for _, p := range c.All(name) {
			if v := memberUID(p.Text()); v != "" {
				ct.Members = appendUnique(ct.Members, v)
			}
		}
	}
	return ct
}

func memberUID(v string) string {
	v = strings.TrimSpace(v)
	for _, p := range []string{"urn:uuid:", "uuid:"} {
		if len(v) > len(p) && strings.EqualFold(v[:len(p)], p) {
			return v[len(p):]
		}
	}
	return v
}

func nonEmpty(xs []string) []string {
	var out []string
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			out = append(out, strings.TrimSpace(x))
		}
	}
	return out
}

func appendUnique(xs []string, add ...string) []string {
outer:
	for _, a := range add {
		for _, x := range xs {
			if strings.EqualFold(x, a) {
				continue outer
			}
		}
		xs = append(xs, a)
	}
	return xs
}

func appendField(fs []Field, f Field) []Field {
	for _, x := range fs {
		if strings.EqualFold(x.Value, f.Value) && strings.EqualFold(x.Label, f.Label) {
			return fs
		}
	}
	return append(fs, f)
}

// ComposedName joins the name parts ("" when there are none).
func (ct Contact) ComposedName() string {
	return strings.Join(nonEmpty([]string{ct.Prefix, ct.Given, ct.Middle, ct.Family, ct.Suffix}), " ")
}

// DisplayName is what a list shows: the stored FN, else the composed name,
// the company, a nickname, the first email or phone.
func (ct Contact) DisplayName() string {
	if ct.Kind == "org" && ct.Org != "" {
		return ct.Org
	}
	for _, v := range []string{ct.FN, ct.ComposedName(), ct.Org, ct.Nickname} {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	if len(ct.Emails) > 0 {
		return ct.Emails[0].Value
	}
	if len(ct.Phones) > 0 {
		return ct.Phones[0].Value
	}
	return ""
}

// Clean trims every value and drops empty entries.
func (ct *Contact) Clean() {
	tr := strings.TrimSpace
	ct.FN, ct.Prefix, ct.Given, ct.Middle, ct.Family, ct.Suffix = tr(ct.FN), tr(ct.Prefix), tr(ct.Given), tr(ct.Middle), tr(ct.Family), tr(ct.Suffix)
	ct.Nickname, ct.PhoneticGiven, ct.PhoneticFamily = tr(ct.Nickname), tr(ct.PhoneticGiven), tr(ct.PhoneticFamily)
	ct.Org, ct.Department, ct.Title, ct.Note = tr(ct.Org), tr(ct.Department), tr(ct.Title), strings.TrimRight(ct.Note, " \n\t")
	clean := func(fs []Field, def string) []Field {
		var out []Field
		for _, f := range fs {
			f.Value, f.Label = tr(f.Value), knownLabel(tr(f.Label))
			if f.Label == "" {
				f.Label = def
			}
			if f.Value != "" {
				out = append(out, f)
			}
		}
		return out
	}
	ct.Emails, ct.Phones, ct.URLs = clean(ct.Emails, "other"), clean(ct.Phones, "mobile"), clean(ct.URLs, "homepage")
	ct.IMs, ct.Socials, ct.Related = clean(ct.IMs, "other"), clean(ct.Socials, "other"), clean(ct.Related, "other")
	for _, fs := range [][]Field{ct.IMs, ct.Socials} {
		for i := range fs {
			fs[i].Label = strings.ToLower(fs[i].Label)
		}
	}
	var dates []Field
	for _, f := range clean(ct.Dates, "other") {
		if f.Value = NormDate(f.Value, false); f.Value != "" {
			dates = append(dates, f)
		}
	}
	ct.Dates = dates
	var adrs []Address
	for _, a := range ct.Addresses {
		a.Label = knownLabel(tr(a.Label))
		if a.Label == "" {
			a.Label = "home"
		}
		a.Street, a.Ext, a.POBox, a.City, a.Region, a.Postcode, a.Country = strings.TrimSpace(a.Street), tr(a.Ext), tr(a.POBox), tr(a.City), tr(a.Region), tr(a.Postcode), tr(a.Country)
		if a.Street+a.Ext+a.POBox+a.City+a.Region+a.Postcode+a.Country != "" {
			adrs = append(adrs, a)
		}
	}
	ct.Addresses = adrs
	if ct.Birthday != "" {
		ct.Birthday = NormDate(ct.Birthday, false)
	}
	if ct.Anniversary != "" {
		ct.Anniversary = NormDate(ct.Anniversary, false)
	}
	ct.Categories = appendUnique(nil, nonEmpty(ct.Categories)...)
	switch ct.Kind {
	case "org", "group":
	default:
		ct.Kind = "individual"
	}
}

var known = map[string]bool{"iphone": true, "car": true, "voice": true}

// knownLabel lower-cases the labels the app knows and keeps the person's own words.
func knownLabel(l string) string {
	low := strings.ToLower(l)
	if _, ok := appleNames[low]; ok || known[low] {
		return low
	}
	return l
}

// ---------- Contact → Card ----------

// New makes a card for a contact (vCard 3.0, what iOS writes).
func New(uid string, ct Contact) *Card {
	c := &Card{}
	c.Add(&Property{Name: "VERSION", Value: "3.0"})
	c.Add(&Property{Name: "PRODID", Value: "-//Pocket Contacts//EN"})
	c.Set("UID", uid)
	c.Apply(Contact{Kind: "individual"}, ct)
	if c.Get("N") == nil && ct.Kind != "group" {
		c.Add(&Property{Name: "N", Value: ";;;;"})
	}
	return c
}

// Apply writes the sections of next that differ from prev into the card.
// Sections that did not change keep their original properties untouched.
func (c *Card) Apply(prev, next Contact) {
	next.Clean()
	v := c.Version()
	same := func(a, b any) bool { return reflect.DeepEqual(a, b) }
	nameChanged := !same([]string{prev.Prefix, prev.Given, prev.Middle, prev.Family, prev.Suffix}, []string{next.Prefix, next.Given, next.Middle, next.Family, next.Suffix})
	if nameChanged {
		n := c.Get("N")
		if n == nil {
			n = &Property{Name: "N"}
			c.Add(n)
		}
		n.SetComponents(next.Family, next.Given, next.Middle, next.Prefix, next.Suffix)
	}
	if prev.PhoneticGiven != next.PhoneticGiven {
		c.Set("X-PHONETIC-FIRST-NAME", next.PhoneticGiven)
	}
	if prev.PhoneticFamily != next.PhoneticFamily {
		c.Set("X-PHONETIC-LAST-NAME", next.PhoneticFamily)
	}
	if prev.Kind != next.Kind {
		c.Remove("X-ABSHOWAS")
		if v == "4.0" {
			c.Set("KIND", map[string]string{"org": "org", "group": "group"}[next.Kind])
		} else if next.Kind == "org" {
			c.Add(&Property{Name: "X-ABSHOWAS", Value: "COMPANY"})
		} else if next.Kind == "group" {
			c.Set("X-ADDRESSBOOKSERVER-KIND", "group")
		}
		if next.Kind != "group" {
			c.Remove("X-ADDRESSBOOKSERVER-KIND")
		}
	}
	if prev.Org != next.Org || prev.Department != next.Department {
		c.Remove("ORG")
		if next.Org != "" || next.Department != "" {
			p := &Property{Name: "ORG"}
			if next.Department != "" {
				p.SetComponents(next.Org, next.Department)
			} else {
				p.SetComponents(next.Org)
			}
			c.Add(p)
		}
	}
	// FN: what the person typed, else the name the card would show.
	switch {
	case next.FN != "" && next.FN != prev.FN:
		c.Set("FN", next.FN)
	case nameChanged || prev.Org != next.Org || prev.Kind != next.Kind || c.Value("FN") == "":
		auto := next
		auto.FN = ""
		if d := auto.DisplayName(); d != "" {
			c.Set("FN", d)
		} else if c.Get("FN") == nil {
			c.Add(&Property{Name: "FN", Value: ""})
		}
	}
	if prev.Nickname != next.Nickname {
		c.Set("NICKNAME", next.Nickname)
	}
	if prev.Title != next.Title {
		c.Set("TITLE", next.Title)
	}
	if prev.Note != next.Note {
		c.Set("NOTE", next.Note)
	}
	if !same(prev.Categories, next.Categories) {
		c.Remove("CATEGORIES")
		if len(next.Categories) > 0 {
			p := &Property{Name: "CATEGORIES"}
			p.SetList(next.Categories)
			c.Add(p)
		}
	}
	if !same(prev.Emails, next.Emails) {
		c.removeLabelled("EMAIL")
		for _, f := range next.Emails {
			p := &Property{Name: "EMAIL"}
			p.SetText(f.Value)
			types := []string{"INTERNET"}
			switch f.Label {
			case "home", "work", "other":
				types = append(types, strings.ToUpper(f.Label))
			default:
				p.Group = c.FreeGroup()
			}
			c.addTyped(p, types, f.Pref, v, f.Label)
		}
	}
	if !same(prev.Phones, next.Phones) {
		c.removeLabelled("TEL")
		for _, f := range next.Phones {
			p := &Property{Name: "TEL"}
			p.SetText(f.Value)
			var types []string
			switch f.Label {
			case "mobile":
				types = []string{"CELL", "VOICE"}
			case "iphone":
				types = []string{"IPHONE", "CELL", "VOICE"}
			case "home":
				types = []string{"HOME", "VOICE"}
			case "work":
				types = []string{"WORK", "VOICE"}
			case "main":
				types = []string{"MAIN"}
			case "home fax":
				types = []string{"HOME", "FAX"}
			case "work fax":
				types = []string{"WORK", "FAX"}
			case "other fax":
				types = []string{"OTHER", "FAX"}
			case "pager":
				types = []string{"PAGER"}
			case "other":
				types = []string{"OTHER", "VOICE"}
			default:
				types = []string{"VOICE"}
				p.Group = c.FreeGroup()
			}
			c.addTyped(p, types, f.Pref, v, f.Label)
		}
	}
	if !same(prev.Addresses, next.Addresses) {
		c.removeLabelled("ADR")
		for _, a := range next.Addresses {
			p := &Property{Name: "ADR"}
			p.SetComponents(a.POBox, a.Ext, a.Street, a.City, a.Region, a.Postcode, a.Country)
			var types []string
			switch a.Label {
			case "home", "work", "other":
				types = []string{strings.ToUpper(a.Label)}
			default:
				p.Group = c.FreeGroup()
			}
			c.addTyped(p, types, a.Pref, v, a.Label)
		}
	}
	if !same(prev.URLs, next.URLs) {
		c.removeLabelled("URL")
		for _, f := range next.URLs {
			p := &Property{Name: "URL"}
			p.SetText(f.Value)
			var types []string
			switch f.Label {
			case "home", "work", "other":
				types = []string{strings.ToUpper(f.Label)}
			default: // homepage and custom labels go through X-ABLabel
				p.Group = c.FreeGroup()
			}
			c.addTyped(p, types, f.Pref, v, f.Label)
		}
	}
	if !same(prev.IMs, next.IMs) {
		var names []string
		for k := range imProps {
			names = append(names, k)
		}
		c.removeLabelled(append(names, "IMPP")...)
		for _, f := range next.IMs {
			scheme := imSchemes[f.Label]
			if scheme == "" {
				scheme = "x-apple"
			}
			val := f.Value
			if !strings.Contains(val, ":") {
				val = scheme + ":" + val
			}
			p := &Property{Name: "IMPP", Value: val}
			p.SetParam("X-SERVICE-TYPE", serviceName(f.Label))
			if f.Pref {
				p.SetParam("TYPE", "pref")
			}
			c.Add(p)
		}
	}
	if !same(prev.Socials, next.Socials) {
		c.Remove("X-SOCIALPROFILE")
		for _, f := range next.Socials {
			p := &Property{Name: "X-SOCIALPROFILE"}
			p.SetText(f.Value)
			p.SetParam("TYPE", strings.ToLower(f.Label))
			c.Add(p)
		}
	}
	if prev.Birthday != next.Birthday {
		c.Remove("BDAY")
		if next.Birthday != "" {
			p := &Property{Name: "BDAY"}
			writeDate(p, next.Birthday, v)
			c.Add(p)
		}
	}
	if prev.Anniversary != next.Anniversary || !same(prev.Dates, next.Dates) {
		c.removeLabelled("X-ABDATE")
		c.Remove("ANNIVERSARY", "X-ANNIVERSARY")
		if next.Anniversary != "" {
			if v == "4.0" {
				p := &Property{Name: "ANNIVERSARY"}
				writeDate(p, next.Anniversary, v)
				c.Add(p)
			} else {
				p := &Property{Name: "X-ABDATE", Group: c.FreeGroup()}
				writeDate(p, next.Anniversary, v)
				c.Add(p)
				c.Add(&Property{Group: p.Group, Name: "X-ABLABEL", Value: Escape(toApple("anniversary"))})
				if !strings.HasPrefix(next.Anniversary, "--") {
					c.Add(&Property{Name: "X-ANNIVERSARY", Value: next.Anniversary})
				}
			}
		}
		for _, f := range next.Dates {
			p := &Property{Name: "X-ABDATE", Group: c.FreeGroup()}
			writeDate(p, f.Value, v)
			c.Add(p)
			c.Add(&Property{Group: p.Group, Name: "X-ABLABEL", Value: Escape(toApple(f.Label))})
		}
	}
	if !same(prev.Related, next.Related) {
		c.removeLabelled("X-ABRELATEDNAMES", "RELATED")
		for _, f := range next.Related {
			if v == "4.0" {
				p := &Property{Name: "RELATED"}
				p.SetText(f.Value)
				p.SetParam("VALUE", "text")
				p.SetParam("TYPE", strings.ToLower(f.Label))
				c.Add(p)
				continue
			}
			p := &Property{Name: "X-ABRELATEDNAMES", Group: c.FreeGroup()}
			p.SetText(f.Value)
			c.Add(p)
			c.Add(&Property{Group: p.Group, Name: "X-ABLABEL", Value: Escape(toApple(f.Label))})
		}
	}
	if !same(prev.Members, next.Members) {
		c.Remove("X-ADDRESSBOOKSERVER-MEMBER", "MEMBER")
		name := "X-ADDRESSBOOKSERVER-MEMBER"
		if v == "4.0" {
			name = "MEMBER"
		}
		for _, m := range next.Members {
			c.Add(&Property{Name: name, Value: "urn:uuid:" + m})
		}
	}
	c.Set("REV", time.Now().UTC().Format("20060102T150405Z"))
}

func serviceName(l string) string {
	switch strings.ToLower(l) {
	case "aim":
		return "AIM"
	case "icq":
		return "ICQ"
	case "msn":
		return "MSN"
	case "qq":
		return "QQ"
	case "google talk":
		return "GoogleTalk"
	case "whatsapp":
		return "WhatsApp"
	}
	if l == "" {
		return "Other"
	}
	return strings.ToUpper(l[:1]) + l[1:]
}

// addTyped appends p with its TYPE values, the preferred mark and, for a
// label that has no TYPE (or is the person's own), an X-ABLabel in its group.
func (c *Card) addTyped(p *Property, types []string, pref bool, version, label string) {
	if pref {
		if version == "4.0" {
			p.SetParam("PREF", "1")
		} else {
			types = append(types, "pref")
		}
	}
	if len(types) > 0 {
		p.SetParam("TYPE", types...)
	}
	c.Add(p)
	if p.Group != "" {
		c.Add(&Property{Group: p.Group, Name: "X-ABLABEL", Value: Escape(toApple(label))})
	}
}

// removeLabelled drops the properties and the X-ABLabel / X-ABADR that
// belong to them through their item group.
func (c *Card) removeLabelled(names ...string) {
	set := map[string]bool{}
	for _, n := range names {
		set[strings.ToUpper(n)] = true
	}
	groups := map[string]bool{}
	for _, p := range c.Props {
		if set[p.Name] && p.Group != "" {
			groups[strings.ToLower(p.Group)] = true
		}
	}
	c.removeWhere(func(p *Property) bool { return set[p.Name] })
	// a group whose only members left are labels goes too
	for g := range groups {
		onlyLabels := true
		for _, p := range c.Props {
			if strings.ToLower(p.Group) == g && p.Name != "X-ABLABEL" && p.Name != "X-ABADR" {
				onlyLabels = false
			}
		}
		if onlyLabels {
			c.removeWhere(func(p *Property) bool { return strings.ToLower(p.Group) == g })
		}
	}
}
