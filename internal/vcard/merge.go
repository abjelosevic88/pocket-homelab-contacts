package vcard

import "strings"

// Merge folds other into ct and returns the result: ct keeps its name and
// kind, every list is the union of both (emails and web addresses by their
// lower-case value, phones by their last 8 digits, addresses by street, city
// and postcode), labels are joined, a birthday or anniversary comes from
// whichever has one (a full date beats one without a year), company, title
// and nickname are filled in when ct has none, and different notes are
// joined with a blank line. Neither argument is changed.
func Merge(ct, other Contact) Contact {
	out := ct
	out.Clean()
	o := other
	o.Clean()

	fill := func(dst *string, v string) {
		if strings.TrimSpace(*dst) == "" {
			*dst = v
		}
	}
	fill(&out.Org, o.Org)
	fill(&out.Department, o.Department)
	fill(&out.Title, o.Title)
	fill(&out.Nickname, o.Nickname)
	fill(&out.PhoneticGiven, o.PhoneticGiven)
	fill(&out.PhoneticFamily, o.PhoneticFamily)

	byText := func(f Field) string { return strings.ToLower(strings.TrimSpace(f.Value)) }
	byName := func(f Field) string { return Fold(f.Value) }
	byPhone := func(f Field) string {
		if k := PhoneKey(f.Value); k != "" {
			return k
		}
		return byText(f)
	}
	byDate := func(f Field) string { return strings.ToLower(f.Label) + "|" + f.Value }
	out.Emails = unionFields(out.Emails, o.Emails, byText)
	out.Phones = unionFields(out.Phones, o.Phones, byPhone)
	out.URLs = unionFields(out.URLs, o.URLs, byText)
	out.IMs = unionFields(out.IMs, o.IMs, byText)
	out.Socials = unionFields(out.Socials, o.Socials, byText)
	out.Dates = unionFields(out.Dates, o.Dates, byDate)
	out.Related = unionFields(out.Related, o.Related, byName)

	adrKey := func(a Address) string { return Fold(a.Street + "|" + a.City + "|" + a.Postcode + "|" + a.POBox) }
	seen := map[string]bool{}
	adrs := append([]Address(nil), out.Addresses...)
	for _, a := range adrs {
		seen[adrKey(a)] = true
	}
	for _, a := range o.Addresses {
		if !seen[adrKey(a)] {
			seen[adrKey(a)] = true
			a.Pref = false
			adrs = append(adrs, a)
		}
	}
	out.Addresses = adrs

	out.Categories = appendUnique(append([]string(nil), out.Categories...), o.Categories...)
	out.Members = appendUnique(append([]string(nil), out.Members...), o.Members...)
	out.Birthday = betterDate(out.Birthday, o.Birthday)
	out.Anniversary = betterDate(out.Anniversary, o.Anniversary)

	crlf := strings.NewReplacer("\r\n", "\n", "\r", "\n")
	switch a, b := strings.TrimSpace(crlf.Replace(out.Note)), strings.TrimSpace(crlf.Replace(o.Note)); {
	case b == "" || strings.Contains(a, b):
	case a == "" || strings.Contains(b, a):
		out.Note = b
	default:
		out.Note = a + "\n\n" + b
	}
	return out
}

// unionFields appends the fields of add whose key is not in fs yet (a
// second preferred entry is no longer preferred).
func unionFields(fs, add []Field, key func(Field) string) []Field {
	out := append([]Field(nil), fs...)
	seen := map[string]bool{}
	for _, f := range out {
		seen[key(f)] = true
	}
	for _, f := range add {
		k := key(f)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		f.Pref = false
		out = append(out, f)
	}
	return out
}

// betterDate picks between two dates of the same kind: the one with a year,
// else whichever is set (a keeps the tie).
func betterDate(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	case strings.HasPrefix(a, "--") && !strings.HasPrefix(b, "--"):
		return b
	}
	return a
}
