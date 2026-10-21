package server

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/vcard"
)

// CSV import: Google Contacts (the current export with "First Name … E-mail
// 1 - Label" and the old one with "Given Name … E-mail 1 - Type"), Outlook
// ("E-mail Address", "Mobile Phone", "Home Street" …) and any sheet with a
// header row (Apple Numbers, Excel), the columns found by their names in
// English, Serbian, Bosnian, Croatian or German. Several values in one cell
// are separated by " ::: " (Google). UTF-8 (with or without BOM), UTF-16 and
// Windows-1250 files; comma, semicolon or tab between the columns.

// csvContact is one row read from a CSV file.
type csvContact struct {
	ct   vcard.Contact
	line int
}

// csvCol says what one column holds.
type csvCol struct {
	field string // given, family, middle, prefix, suffix, name, nickname, phonetic_given, phonetic_family, org, title, dept, note, bday, anniv, labels, email, phone, adr, url, related, event, im, custom ("" = ignored)
	slot  string // which entry of a list field ("1" of "E-mail 1 - Value", "home" of "Home Street")
	part  string // value | label | street | ext | pobox | city | region | postcode | country | formatted
	label string // label the header implies ("mobile" for "Mobile Phone")
}

var exactCols = map[string]csvCol{}

func init() {
	for field, names := range map[string][]string{
		"given":           {"first name", "given name", "firstname", "first", "given", "ime", "vorname", "prenom"},
		"middle":          {"middle name", "additional name", "srednje ime", "middle"},
		"family":          {"last name", "family name", "lastname", "surname", "last", "prezime", "nachname", "nom"},
		"name":            {"name", "full name", "display name", "ime i prezime", "naziv", "kontakt", "contact", "contact name", "osoba", "imeprezime"},
		"prefix":          {"name prefix", "prefix", "salutation", "anrede"},
		"suffix":          {"name suffix", "suffix"},
		"nickname":        {"nickname", "nadimak", "spitzname"},
		"phonetic_given":  {"phonetic first name", "given name yomi", "yomi first name"},
		"phonetic_family": {"phonetic last name", "family name yomi", "yomi last name"},
		"org":             {"organization name", "organization", "organisation", "company", "company name", "firma", "kompanija", "organizacija", "preduzece", "employer", "unternehmen"},
		"title":           {"organization title", "job title", "title", "position", "pozicija", "funkcija", "zanimanje", "radno mjesto", "radno mesto", "role", "beruf"},
		"dept":            {"organization department", "department", "odjel", "odjeljenje", "odeljenje", "sektor", "abteilung"},
		"note":            {"notes", "note", "napomena", "napomene", "biljeska", "biljeske", "beleska", "beleske", "komentar", "opis", "comments", "comment", "notizen"},
		"bday":            {"birthday", "bday", "birth date", "birthdate", "date of birth", "dob", "rodendan", "rodjendan", "datum rodenja", "datum rodjenja", "geburtstag"},
		"anniv":           {"anniversary", "wedding anniversary", "godisnjica", "jahrestag"},
		"labels":          {"labels", "group membership", "categories", "category", "groups", "group", "grupa", "grupe", "kategorija", "kategorije", "oznake", "tags"},
		"url":             {"web page", "website", "web", "url", "homepage", "home page", "sajt", "web stranica", "personal web page", "webseite"},
		"":                {"file as", "photo", "e-mail display name", "e-mail 2 display name", "e-mail 3 display name", "e-mail type", "e-mail 2 type", "e-mail 3 type", "priority", "sensitivity", "private", "gender", "initials", "language", "location", "mileage", "billing information", "directory server", "keywords", "user 1", "user 2", "user 3", "user 4", "account", "hobby", "children", "spouse", "referred by", "manager's name", "assistant's name", "office location", "government id number", "organizational id number", "internet free busy", "yomi name", "short name", "maiden name", "sort name", "occupation"},
	} {
		for _, n := range names {
			exactCols[n] = csvCol{field: field}
		}
	}
	// "Address", "Adresa" alone: one line of a home address
	for _, n := range []string{"address", "adresa", "home address", "kucna adresa", "anschrift"} {
		exactCols[n] = csvCol{field: "adr", slot: "home", part: "formatted"}
	}
	for _, n := range []string{"city", "grad", "mjesto", "mesto", "town", "ort", "stadt"} {
		exactCols[n] = csvCol{field: "adr", slot: "home", part: "city"}
	}
	for _, n := range []string{"street", "ulica", "strasse"} {
		exactCols[n] = csvCol{field: "adr", slot: "home", part: "street"}
	}
	for _, n := range []string{"zip", "zip code", "postal code", "postcode", "postanski broj", "ptt", "plz"} {
		exactCols[n] = csvCol{field: "adr", slot: "home", part: "postcode"}
	}
	for _, n := range []string{"country", "drzava", "zemlja", "land"} {
		exactCols[n] = csvCol{field: "adr", slot: "home", part: "country"}
	}
	for _, n := range []string{"state", "region", "province", "regija", "kanton", "entitet", "bundesland"} {
		exactCols[n] = csvCol{field: "adr", slot: "home", part: "region"}
	}
}

// googleCol: "E-mail 1 - Value", "Phone 2 - Label", "Address 1 - City", "Organization 1 - Name" …
var googleCol = regexp.MustCompile(`^(e-mail|email|phone|address|website|relation|event|im|organization|custom field|external id) (\d+) - (.+)$`)

var googleParts = map[string]string{
	"value": "value", "type": "label", "label": "label", "service": "service", "street": "street", "city": "city",
	"po box": "pobox", "region": "region", "postal code": "postcode", "country": "country", "extended address": "ext",
	"formatted": "formatted",
}

func normHeader(h string) string {
	h = vcard.Fold(strings.TrimSpace(h))
	h = strings.NewReplacer("_", " ", "/", " ", ".", " ", ":", " ", "(", " ", ")", " ").Replace(h)
	return strings.Join(strings.Fields(h), " ")
}

func hasWord(h string, words ...string) bool {
	for _, w := range words {
		if h == w || strings.HasPrefix(h, w+" ") || strings.HasSuffix(h, " "+w) || strings.Contains(h, " "+w+" ") {
			return true
		}
	}
	return false
}

// slotOf is the place an Outlook-style header names ("Business Street" → work).
func slotOf(h string) string {
	switch {
	case hasWord(h, "business", "work", "office", "posao", "posla", "sluzbeni", "sluzbena", "firma", "geschaftlich"):
		return "work"
	case hasWord(h, "other", "ostalo", "drugi", "druga"):
		return "other"
	case hasWord(h, "home", "private", "personal", "kucni", "kucna", "privatni", "privatna", "licni", "privat"):
		return "home"
	}
	return ""
}

// mapHeader works out what a column holds from its header.
func mapHeader(raw string, idx int, outlook bool) csvCol {
	h := normHeader(raw)
	if outlook && h == "title" {
		return csvCol{field: "prefix"} // Outlook: "Title" is Mr./Dr., "Job Title" the job
	}
	if c, ok := exactCols[h]; ok {
		return c
	}
	if m := googleCol.FindStringSubmatch(h); m != nil {
		part := googleParts[m[3]]
		if m[1] == "im" && m[3] == "type" {
			part = "" // the service names the account better than home/work
		}
		if part == "" && m[1] != "organization" {
			return csvCol{}
		}
		switch m[1] {
		case "e-mail", "email":
			return csvCol{field: "email", slot: m[2], part: part}
		case "phone":
			return csvCol{field: "phone", slot: m[2], part: part}
		case "address":
			return csvCol{field: "adr", slot: m[2], part: part}
		case "website":
			return csvCol{field: "url", slot: m[2], part: part}
		case "relation":
			return csvCol{field: "related", slot: m[2], part: part}
		case "event":
			return csvCol{field: "event", slot: m[2], part: part}
		case "im":
			if part == "service" {
				part = "label"
			}
			return csvCol{field: "im", slot: m[2], part: part}
		case "custom field":
			return csvCol{field: "custom", slot: m[2], part: part}
		case "organization":
			return csvCol{field: map[string]string{"name": "org", "title": "title", "department": "dept"}[m[3]]}
		}
		return csvCol{}
	}
	col := strconv.Itoa(1000 + idx)
	slot := slotOf(h)
	switch {
	case strings.Contains(h, "display") || strings.HasSuffix(h, " type"):
		return csvCol{}
	case strings.Contains(h, "e-mail") || strings.Contains(h, "email") || strings.Contains(h, "e-posta") || hasWord(h, "mail"):
		return csvCol{field: "email", slot: col, part: "value", label: slot}
	case strings.Contains(h, "phone") || strings.Contains(h, "telefon") || hasWord(h, "tel", "mobitel", "mobilni", "mob", "gsm", "cell", "mobile", "fax", "pager", "handy", "faks"):
		label := slot
		switch {
		case hasWord(h, "fax", "faks"):
			label = firstNonEmpty(slot, "other") + " fax"
		case hasWord(h, "mobile", "mobitel", "mobilni", "mob", "gsm", "cell", "handy", "car"):
			label = "mobile"
		case hasWord(h, "pager"):
			label = "pager"
		case hasWord(h, "main", "primary", "glavni"):
			label = "main"
		}
		return csvCol{field: "phone", slot: col, part: "value", label: label}
	}
	// Outlook / generic address parts: "Home Street", "Business Postal Code", "Other Country/Region"
	part := ""
	switch {
	case hasWord(h, "po box", "pobox", "postfach"):
		part = "pobox"
	case hasWord(h, "street", "ulica", "street 2", "street 3", "strasse") || strings.Contains(h, "street"):
		part = "street"
	case hasWord(h, "city", "grad", "mjesto", "mesto", "town", "ort", "stadt"):
		part = "city"
	case hasWord(h, "postal code", "zip", "postcode", "postanski broj", "ptt", "plz") || strings.Contains(h, "postal"):
		part = "postcode"
	case hasWord(h, "country", "drzava", "zemlja", "land") || strings.Contains(h, "country"):
		part = "country"
	case hasWord(h, "state", "region", "province", "regija", "kanton"):
		part = "region"
	case hasWord(h, "address", "adresa"):
		part = "formatted"
	}
	if part != "" {
		return csvCol{field: "adr", slot: firstNonEmpty(slot, "home"), part: part}
	}
	if hasWord(h, "web", "website", "homepage", "url") {
		return csvCol{field: "url", slot: col, part: "value", label: slot}
	}
	return csvCol{}
}

// decodeCSVText turns the file into UTF-8 text.
func decodeCSVText(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		data = data[3:]
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
		if b, err := unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM).NewDecoder().Bytes(data); err == nil {
			data = b
		}
	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		if b, err := unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM).NewDecoder().Bytes(data); err == nil {
			data = b
		}
	}
	if !utf8.Valid(data) {
		if b, err := charmap.Windows1250.NewDecoder().Bytes(data); err == nil {
			data = b
		}
	}
	return string(data)
}

// csvDelimiter guesses the separator from the header line.
func csvDelimiter(text string) rune {
	head, _, _ := strings.Cut(text, "\n")
	best, n := ',', strings.Count(head, ",")
	for _, d := range []rune{';', '\t'} {
		if c := strings.Count(head, string(d)); c > n {
			best, n = d, c
		}
	}
	return best
}

var (
	dotDate   = regexp.MustCompile(`^(\d{1,2})\.\s*(\d{1,2})\.\s*(\d{4})\.?$`)
	slashDate = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})/(\d{2}|\d{4})$`)
)

// csvDate reads the date forms spreadsheets write: 1988-05-03, --05-03,
// 3.5.1988 (European), 5/3/1988 (Outlook, month first unless the first number
// can't be a month), 19880503.
func csvDate(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || strings.HasPrefix(v, "0/0/") || strings.HasPrefix(v, "00.00.") {
		return ""
	}
	if m := dotDate.FindStringSubmatch(v); m != nil {
		return ymd(m[3], m[2], m[1])
	}
	if m := slashDate.FindStringSubmatch(v); m != nil {
		a, b := m[1], m[2]
		if n, _ := strconv.Atoi(a); n > 12 {
			return ymd(m[3], b, a)
		}
		return ymd(m[3], a, b)
	}
	return vcard.NormDate(v, false)
}

func ymd(y, m, d string) string {
	yy, _ := strconv.Atoi(y)
	mm, _ := strconv.Atoi(m)
	dd, _ := strconv.Atoi(d)
	if len(y) == 2 {
		if yy > 30 {
			yy += 1900
		} else {
			yy += 2000
		}
	}
	if mm < 1 || mm > 12 || dd < 1 || dd > 31 || yy < 1800 {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", yy, mm, dd)
}

// splitCell splits Google's " ::: " multi-values.
func splitCell(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ":::") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// cleanLabel reads a Google label: "* Mobile" is the preferred one.
func cleanLabel(l string) (string, bool) {
	l = strings.TrimSpace(l)
	pref := strings.HasPrefix(l, "* ")
	return strings.TrimSpace(strings.TrimPrefix(l, "* ")), pref
}

// parseCSV reads every contact of a CSV file; errors name the line.
func parseCSV(data []byte) ([]csvContact, []string, error) {
	text := decodeCSVText(data)
	rd := csv.NewReader(strings.NewReader(text))
	rd.Comma = csvDelimiter(text)
	rd.LazyQuotes = true
	rd.FieldsPerRecord = -1
	header, err := rd.Read()
	if err != nil {
		return nil, nil, errors.New("the CSV file is empty")
	}
	outlook := false
	for _, h := range header {
		if normHeader(h) == "job title" {
			outlook = true
		}
	}
	cols := make([]csvCol, len(header))
	useful := false
	for i, h := range header {
		cols[i] = mapHeader(h, i, outlook)
		switch cols[i].field {
		case "given", "family", "name", "org", "email", "phone":
			useful = true
		}
	}
	if !useful {
		return nil, nil, errors.New("no column looks like a name, an email or a phone number (the first row must be the column names)")
	}
	var out []csvContact
	var errs []string
	for {
		rec, err := rd.Read()
		if err == io.EOF {
			break
		}
		line, _ := rd.FieldPos(0)
		if err != nil {
			errs = append(errs, fmt.Sprintf("line %d: %v", line, err))
			continue
		}
		ct, ok := csvRow(cols, rec)
		if !ok {
			continue // an empty row
		}
		ct.Clean()
		if ct.DisplayName() == "" {
			errs = append(errs, fmt.Sprintf("line %d: no name, email or phone number", line))
			continue
		}
		out = append(out, csvContact{ct: ct, line: line})
	}
	return out, errs, nil
}

// csvRow turns one record into a contact (false when the row is empty).
func csvRow(cols []csvCol, rec []string) (vcard.Contact, bool) {
	var ct vcard.Contact
	type group struct {
		col   csvCol
		parts map[string]string
	}
	groups := map[string]*group{}
	var order []string
	var notes []string
	fullName := ""
	filled := false
	for i, raw := range rec {
		if i >= len(cols) || cols[i].field == "" {
			continue
		}
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		filled = true
		c := cols[i]
		switch c.field {
		case "given":
			ct.Given = v
		case "middle":
			ct.Middle = v
		case "family":
			ct.Family = v
		case "name":
			fullName = v
		case "prefix":
			ct.Prefix = v
		case "suffix":
			ct.Suffix = v
		case "nickname":
			ct.Nickname = v
		case "phonetic_given":
			ct.PhoneticGiven = v
		case "phonetic_family":
			ct.PhoneticFamily = v
		case "org":
			ct.Org = v
		case "title":
			ct.Title = v
		case "dept":
			ct.Department = v
		case "note":
			notes = append(notes, v)
		case "bday":
			ct.Birthday = csvDate(v)
		case "anniv":
			ct.Anniversary = csvDate(v)
		case "labels":
			for _, l := range strings.FieldsFunc(v, func(r rune) bool { return r == ';' || r == ',' }) {
				for _, x := range splitCell(l) {
					x = strings.TrimSpace(strings.TrimPrefix(x, "* "))
					if x == "" || strings.EqualFold(x, "myContacts") || strings.EqualFold(x, "starred") {
						continue
					}
					ct.Categories = append(ct.Categories, x)
				}
			}
		default:
			key := c.field + "|" + c.slot
			g := groups[key]
			if g == nil {
				g = &group{col: c, parts: map[string]string{}}
				groups[key] = g
				order = append(order, key)
			}
			part := firstNonEmpty(c.part, "value")
			if prev := g.parts[part]; prev != "" {
				v = prev + ", " + v // Outlook "Home Street 2"
			}
			g.parts[part] = v
		}
	}
	if !filled {
		return ct, false
	}
	for _, key := range order {
		g := groups[key]
		label := g.parts["label"]
		values := splitCell(g.parts["value"])
		labels := splitCell(label)
		// the label of the i-th value: its own, else the first one (the "* "
		// of the preferred entry only marks the first value)
		labelAt := func(i int) (string, bool) {
			if i < len(labels) {
				return cleanLabel(labels[i])
			}
			l := g.col.label
			if len(labels) > 0 {
				l = labels[0]
			}
			l, _ = cleanLabel(l)
			return l, false
		}
		switch g.col.field {
		case "email", "phone", "url", "related", "im":
			for i, v := range values {
				l, pref := labelAt(i)
				f := vcard.Field{Label: l, Value: v, Pref: pref}
				switch g.col.field {
				case "email":
					ct.Emails = append(ct.Emails, f)
				case "phone":
					ct.Phones = append(ct.Phones, f)
				case "url":
					ct.URLs = append(ct.URLs, f)
				case "related":
					f.Label = strings.ToLower(f.Label)
					ct.Related = append(ct.Related, f)
				case "im":
					f.Label = strings.ToLower(f.Label)
					ct.IMs = append(ct.IMs, f)
				}
			}
		case "event":
			for i, v := range values {
				l, _ := labelAt(i)
				d := csvDate(v)
				if d == "" {
					continue
				}
				if strings.EqualFold(l, "anniversary") || strings.EqualFold(l, "godišnjica") {
					ct.Anniversary = d
				} else if strings.EqualFold(l, "birthday") && ct.Birthday == "" {
					ct.Birthday = d
				} else {
					ct.Dates = append(ct.Dates, vcard.Field{Label: firstNonEmpty(l, "other"), Value: d})
				}
			}
		case "custom":
			if v := g.parts["value"]; v != "" {
				notes = append(notes, strings.TrimSpace(firstNonEmpty(label, "Note")+": "+v))
			}
		case "adr":
			l, pref := cleanLabel(firstNonEmpty(label, g.col.slot))
			if _, err := strconv.Atoi(l); err == nil {
				l = "home"
			}
			a := vcard.Address{Label: strings.ToLower(l), Street: g.parts["street"], Ext: g.parts["ext"], POBox: g.parts["pobox"],
				City: g.parts["city"], Region: g.parts["region"], Postcode: g.parts["postcode"], Country: g.parts["country"], Pref: pref}
			if a.Street == "" && a.City == "" && a.Postcode == "" && a.Country == "" {
				// only the one-line form: keep it as the street
				a.Street = strings.Join(strings.Fields(strings.ReplaceAll(g.parts["formatted"], "\n", ", ")), " ")
			}
			ct.Addresses = append(ct.Addresses, a)
		}
	}
	if len(notes) > 0 {
		ct.Note = strings.Join(notes, "\n")
	}
	if fullName != "" {
		if ct.Given == "" && ct.Family == "" {
			ct.Given, ct.Family = splitName(fullName)
		}
		if ct.Given+" "+ct.Family != fullName {
			ct.FN = fullName
		}
	}
	if ct.Given == "" && ct.Family == "" && ct.FN == "" && ct.Org != "" {
		ct.Kind = "org"
	}
	return ct, true
}

// splitName reads "Marko Petrović" or "Petrović, Marko" as given and family name.
func splitName(full string) (given, family string) {
	if f, g, ok := strings.Cut(full, ","); ok {
		return strings.TrimSpace(g), strings.TrimSpace(f)
	}
	words := strings.Fields(full)
	if len(words) < 2 {
		return full, ""
	}
	return strings.Join(words[:len(words)-1], " "), words[len(words)-1]
}
