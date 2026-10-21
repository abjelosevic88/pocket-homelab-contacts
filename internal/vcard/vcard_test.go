package vcard

import (
	"strings"
	"testing"
)

const iosCard = "BEGIN:VCARD\r\nVERSION:3.0\r\nPRODID:-//Apple Inc.//iPhone OS 26.0//EN\r\nN:Petrović;Marko;;;\r\nFN:Marko Petrović\r\nORG:Acme d.o.o.;Razvoj;\r\nTITLE:Inženjer\r\nitem1.EMAIL;type=INTERNET;type=pref:marko@example.com\r\nitem1.X-ABLabel:_$!<Other>!$_\r\nEMAIL;type=INTERNET;type=WORK:m.petrovic@acme.ba\r\nTEL;type=CELL;type=VOICE;type=pref:+387 65 123 456\r\nitem2.TEL:062/555-111\r\nitem2.X-ABLabel:Vikendica\r\nitem3.ADR;type=HOME;type=pref:;;Kralja Petra 12;Banja Luka;;78000;Bosna i Hercegovina\r\nitem3.X-ABADR:ba\r\nBDAY;X-APPLE-OMIT-YEAR=1604:1604-05-03\r\nitem4.X-ABDATE;type=pref:2015-06-20\r\nitem4.X-ABLabel:_$!<Anniversary>!$_\r\nitem5.X-ABRELATEDNAMES;type=pref:Jelena\r\nitem5.X-ABLabel:_$!<Spouse>!$_\r\nIMPP;X-SERVICE-TYPE=Skype;type=pref:skype:marko.p\r\nX-SOCIALPROFILE;type=twitter;x-user=markop:x-apple:markop\r\nNOTE:Prvi red\\nDrugi red\\, sa zarezom\r\nCATEGORIES:Posao,Prijatelji\r\nX-UNKNOWN-THING;X-FOO=bar:keep me\r\nPHOTO;ENCODING=b;TYPE=JPEG:/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAP////////////////////////////////////////////////////////////////////////////////////8=\r\nUID:ABCD-1234\r\nEND:VCARD\r\n"

func TestParseIOS(t *testing.T) {
	c, err := ParseOne([]byte(iosCard))
	if err != nil {
		t.Fatal(err)
	}
	ct := c.Contact()
	if ct.Given != "Marko" || ct.Family != "Petrović" || ct.FN != "Marko Petrović" {
		t.Fatalf("name: %+v", ct)
	}
	if ct.Org != "Acme d.o.o." || ct.Department != "Razvoj" || ct.Title != "Inženjer" {
		t.Fatalf("org: %q %q %q", ct.Org, ct.Department, ct.Title)
	}
	if len(ct.Emails) != 2 || ct.Emails[0].Label != "other" || !ct.Emails[0].Pref || ct.Emails[1].Label != "work" {
		t.Fatalf("emails: %+v", ct.Emails)
	}
	if len(ct.Phones) != 2 || ct.Phones[0].Label != "mobile" || ct.Phones[1].Label != "Vikendica" {
		t.Fatalf("phones: %+v", ct.Phones)
	}
	if len(ct.Addresses) != 1 || ct.Addresses[0].City != "Banja Luka" || ct.Addresses[0].Label != "home" {
		t.Fatalf("adr: %+v", ct.Addresses)
	}
	if ct.Birthday != "--05-03" || ct.Anniversary != "2015-06-20" {
		t.Fatalf("dates: %q %q", ct.Birthday, ct.Anniversary)
	}
	if len(ct.Related) != 1 || ct.Related[0].Label != "spouse" || ct.Related[0].Value != "Jelena" {
		t.Fatalf("related: %+v", ct.Related)
	}
	if len(ct.IMs) != 1 || ct.IMs[0].Label != "skype" || ct.IMs[0].Value != "marko.p" {
		t.Fatalf("ims: %+v", ct.IMs)
	}
	if len(ct.Socials) != 1 || ct.Socials[0].Value != "markop" {
		t.Fatalf("socials: %+v", ct.Socials)
	}
	if ct.Note != "Prvi red\nDrugi red, sa zarezom" {
		t.Fatalf("note: %q", ct.Note)
	}
	if strings.Join(ct.Categories, "|") != "Posao|Prijatelji" {
		t.Fatalf("categories: %v", ct.Categories)
	}
	if b, mime, _ := c.Photo(); len(b) == 0 || mime != "image/jpeg" {
		t.Fatalf("photo: %d %s", len(b), mime)
	}
}

func TestApplyKeepsUntouchedSections(t *testing.T) {
	c, _ := ParseOne([]byte(iosCard))
	prev := c.Contact()
	next := prev
	next.Phones = append([]Field(nil), prev.Phones...)
	next.Phones = append(next.Phones, Field{Label: "work", Value: "051 222 333"})
	c.Apply(prev, next)
	out := c.String()
	for _, keep := range []string{"item1.EMAIL;TYPE=INTERNET,pref:marko@example.com", "X-UNKNOWN-THING;X-FOO=bar:keep me", "BDAY;X-APPLE-OMIT-YEAR=1604:1604-05-03", "item3.X-ABADR:ba", "UID:ABCD-1234", "PHOTO;ENCODING=b;TYPE=JPEG:"} {
		if !strings.Contains(out, keep) {
			t.Errorf("lost %q in\n%s", keep, out)
		}
	}
	re, err := ParseOne([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	got := re.Contact()
	if len(got.Phones) != 3 || got.Phones[1].Label != "Vikendica" || got.Phones[2].Label != "work" {
		t.Fatalf("phones after edit: %+v", got.Phones)
	}
	// the old item2 label group must not linger twice
	if strings.Count(out, "Vikendica") != 1 {
		t.Fatalf("label duplicated:\n%s", out)
	}
}

func TestApplyNameUpdatesFN(t *testing.T) {
	c, _ := ParseOne([]byte(iosCard))
	prev := c.Contact()
	next := prev
	next.Family = "Petrović-Jovanović"
	c.Apply(prev, next)
	if got := c.Value("FN"); got != "Marko Petrović-Jovanović" {
		t.Fatalf("FN = %q", got)
	}
	if n := c.Get("N").Components(); n[0] != "Petrović-Jovanović" || n[1] != "Marko" {
		t.Fatalf("N = %v", n)
	}
}

func TestVCard21QuotedPrintable(t *testing.T) {
	data := "BEGIN:VCARD\r\nVERSION:2.1\r\nN;CHARSET=UTF-8;ENCODING=QUOTED-PRINTABLE:=C4=86osi=C4=87;=C4=90or=C4=91e;;;\r\nTEL;CELL;PREF:+38765111222\r\nTEL;HOME:051/111-222\r\nNOTE;ENCODING=QUOTED-PRINTABLE:Dugi tekst koji se =\r\nnastavlja\r\nPHOTO;ENCODING=BASE64;JPEG:\r\n /9j/4AAQSkZJRgABAQAAAQABAAD\r\n\r\nEND:VCARD\r\n"
	c, err := ParseOne([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	ct := c.Contact()
	if ct.Family != "Ćosić" || ct.Given != "Đorđe" {
		t.Fatalf("name: %q %q", ct.Given, ct.Family)
	}
	if ct.Note != "Dugi tekst koji se nastavlja" {
		t.Fatalf("note %q", ct.Note)
	}
	if len(ct.Phones) != 2 || ct.Phones[0].Label != "mobile" || !ct.Phones[0].Pref || ct.Phones[1].Label != "home" {
		t.Fatalf("phones %+v", ct.Phones)
	}
	c.Upgrade()
	if c.Version() != "3.0" || c.Value("FN") != "Đorđe Ćosić" {
		t.Fatalf("upgrade: %s %q", c.Version(), c.Value("FN"))
	}
	if b, _, _ := c.Photo(); len(b) == 0 {
		t.Fatal("photo lost in upgrade")
	}
}

func TestWindows1250File(t *testing.T) {
	// "Šarić" in Windows-1250 with no CHARSET parameter
	data := []byte("BEGIN:VCARD\r\nVERSION:2.1\r\nN:\x8aari\xe6;Ana\r\nEND:VCARD\r\n")
	c, err := ParseOne(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Contact().Family; got != "Šarić" {
		t.Fatalf("family = %q", got)
	}
}

func TestFoldingAndAppleRoundTrip(t *testing.T) {
	if got := Fold("Đorđe  Ćosić"); got != "dorde cosic" {
		t.Fatalf("fold %q", got)
	}
	if got := Fold("Јелена Шарић"); got != "jelena saric" {
		t.Fatalf("fold cyrillic %q", got)
	}
	if !strings.Contains(SearchForms("Đorđe"), "djordje") || !strings.Contains(SearchForms("Đorđe"), "dorde") {
		t.Fatal("dj form missing")
	}
	if PhoneKey("+387 65 123 456") != PhoneKey("065/123-456") {
		t.Fatal("phone keys differ")
	}
	c := New("u1", Contact{Given: "Ana", Family: "Kovač", Birthday: "--12-24", Anniversary: "2010-01-02",
		Phones: []Field{{Label: "Posao 2", Value: "123"}}, Related: []Field{{Label: "mother", Value: "Mira"}},
		URLs: []Field{{Label: "homepage", Value: "https://ana.example"}}, IMs: []Field{{Label: "telegram", Value: "anak"}}})
	got := c.Contact()
	if got.FN != "Ana Kovač" || got.Birthday != "--12-24" || got.Anniversary != "2010-01-02" {
		t.Fatalf("roundtrip %+v", got)
	}
	if got.Phones[0].Label != "Posao 2" || got.Related[0].Label != "mother" || got.URLs[0].Label != "homepage" {
		t.Fatalf("labels %+v %+v %+v", got.Phones, got.Related, got.URLs)
	}
	if got.IMs[0].Label != "telegram" || got.IMs[0].Value != "anak" {
		t.Fatalf("im %+v", got.IMs)
	}
	if strings.Count(c.String(), "X-ANNIVERSARY") != 1 {
		t.Fatal("anniversary for Android missing")
	}
}

func TestFolding75(t *testing.T) {
	c := New("u2", Contact{Given: "X", Note: strings.Repeat("ćevapi ", 40)})
	for _, ln := range strings.Split(c.String(), "\r\n") {
		if len(ln) > 75 {
			t.Fatalf("line longer than 75 octets: %d", len(ln))
		}
	}
	re, err := ParseOne([]byte(c.String()))
	if err != nil || re.Contact().Note != strings.TrimRight(strings.Repeat("ćevapi ", 40), " ") {
		t.Fatalf("fold roundtrip: %v %q", err, re.Contact().Note)
	}
}

func TestMultipleCardsAndGroups(t *testing.T) {
	data := "BEGIN:VCARD\nVERSION:3.0\nFN:A\nN:;A;;;\nUID:a\nEND:VCARD\nBEGIN:VCARD\nVERSION:3.0\nX-ADDRESSBOOKSERVER-KIND:group\nFN:Family\nN:Family;;;;\nX-ADDRESSBOOKSERVER-MEMBER:urn:uuid:a\nUID:g\nEND:VCARD\n"
	cards, err := Parse([]byte(data))
	if err != nil || len(cards) != 2 {
		t.Fatalf("%v %d", err, len(cards))
	}
	g := cards[1].Contact()
	if g.Kind != "group" || len(g.Members) != 1 || g.Members[0] != "a" {
		t.Fatalf("group %+v", g)
	}
}
