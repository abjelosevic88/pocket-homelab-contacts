package server

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"time"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/store"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/vcard"
)

// seedDemo fills an empty box with made-up people (DEMO_DATA=true): about
// forty contacts in the first person's "Contacts" (birthdays around today,
// labels, companies, two obvious duplicates, pictures, an Apple group) and a
// "Family" book shared with everyone.
func (s *Server) seedDemo(ctx context.Context) error {
	if s.store.ContactCount(ctx) > 0 {
		return nil
	}
	var u *store.User
	if n, _ := s.store.CountUsers(ctx); n == 0 {
		var err error
		if u, err = s.firstOrSystemUser(ctx, "admin@localhost", "Admin"); err != nil {
			return err
		}
	} else {
		users, err := s.store.ListUsers(ctx)
		if err != nil || len(users) == 0 {
			return err
		}
		u = &users[0]
	}
	s.seedUser(ctx, u)
	own, err := s.store.GetAddressBookByUID(ctx, u.ID, "contacts")
	if err != nil {
		if own, err = s.store.DefaultAddressBook(ctx, u.ID); err != nil {
			return err
		}
	}
	family := &store.AddressBook{UserID: u.ID, Name: "Family", Color: bookColors[2], Description: "Shared with the household"}
	if err := s.store.CreateAddressBook(ctx, family); err != nil {
		return err
	}
	if err := s.store.UpsertShare(ctx, &store.Share{AddressBookID: family.ID, Everyone: true, Role: store.RoleWrite}, u.ID); err != nil {
		return err
	}
	a := actor{u: u, source: "web"}
	today := time.Now().In(s.loc(ctx, u.ID))
	uids := map[string]string{}
	for i, d := range demoPeople(today) {
		book := own
		if d.family {
			book = family
		}
		ct := d.ct
		ct.Clean()
		card := vcard.New(newUID(), ct)
		if d.photo {
			card.SetPhoto(demoAvatar(i), "image/jpeg")
		}
		e, err := s.createCard(ctx, a, book, card)
		if err != nil {
			return fmt.Errorf("demo contact %s: %w", ct.DisplayName(), err)
		}
		uids[d.key] = e.UID
		if d.starred {
			_ = s.store.SetStarred(ctx, u.ID, e.ID, true)
		}
	}
	group := vcard.New(newUID(), vcard.Contact{Kind: "group", FN: "Kumovi", Members: []string{uids["marko"], uids["nikola"], uids["djordje"]}})
	if _, err := s.createCard(ctx, a, own, group); err != nil {
		return err
	}
	s.log.Info("demo data seeded", "user", u.Email)
	return nil
}

type demoPerson struct {
	key     string
	ct      vcard.Contact
	photo   bool
	starred bool
	family  bool // in the shared "Family" book
}

// demoPeople are the made-up contacts; birthdays are placed around today.
func demoPeople(today time.Time) []demoPerson {
	// on is the date `days` from today in the given year ("--MM-DD" for year 0)
	on := func(days, year int) string {
		d := today.AddDate(0, 0, days)
		if year == 0 {
			return d.Format("--01-02")
		}
		if d.Month() == 2 && d.Day() == 29 && !isLeap(year) {
			year = 1992
		}
		return fmt.Sprintf("%04d-%s", year, d.Format("01-02"))
	}
	adr := func(city string) []vcard.Address {
		switch city {
		case "bl":
			return []vcard.Address{{Label: "home", Street: "Kralja Petra I Karađorđevića 85", City: "Banja Luka", Postcode: "78000", Country: "Bosna i Hercegovina"}}
		case "sa":
			return []vcard.Address{{Label: "home", Street: "Ferhadija 12", City: "Sarajevo", Postcode: "71000", Country: "Bosna i Hercegovina"}}
		case "bg":
			return []vcard.Address{{Label: "home", Street: "Knez Mihailova 22", City: "Beograd", Postcode: "11000", Country: "Srbija"}}
		case "be":
			return []vcard.Address{{Label: "work", Street: "Torstraße 140", City: "Berlin", Postcode: "10115", Country: "Deutschland"}}
		}
		return nil
	}
	email := func(label, v string) []vcard.Field { return []vcard.Field{{Label: label, Value: v}} }
	mobile := func(v string) []vcard.Field { return []vcard.Field{{Label: "mobile", Value: v}} }
	labels := func(l ...string) []string { return l }
	return []demoPerson{
		{key: "marko", photo: true, starred: true, ct: vcard.Contact{Given: "Marko", Family: "Petrović", Org: "Mtel", Title: "Inženjer mreže",
			Emails: email("home", "marko.petrovic@icloud.com"), Phones: mobile("+387 65 123 456"), Addresses: adr("bl"),
			Birthday: on(0, 1988), Anniversary: on(5, 2014), Related: []vcard.Field{{Label: "spouse", Value: "Jelena Petrović"}},
			Categories: labels("Friends"), Note: "Kum. Plays chess on Thursdays.\nFrom iCloud."}},
		{key: "marko-gmail", ct: vcard.Contact{Given: "marko", Family: "petrovic",
			Emails: email("other", "marko.petrovic@gmail.com"), Phones: []vcard.Field{{Label: "work", Value: "065/123-456"}},
			Note: "From Gmail."}},
		{key: "jelena", photo: true, starred: true, ct: vcard.Contact{Given: "Jelena", Family: "Petrović", Emails: email("home", "jelena.p@icloud.com"),
			Phones: mobile("+387 65 765 432"), Addresses: adr("bl"), Birthday: on(40, 1990), Categories: labels("Friends")}},
		{key: "djordje", photo: true, ct: vcard.Contact{Given: "Đorđe", Family: "Ćosić", Emails: email("home", "djordje.cosic@example.rs"),
			Phones: mobile("+381 63 222 333"), Addresses: adr("bg"), Birthday: on(3, 1985), Categories: labels("Friends")}},
		{key: "jovana", ct: vcard.Contact{Given: "Jovana", Family: "Đukić", Org: "Pocket d.o.o.", Title: "Dizajnerka",
			Emails: email("work", "jovana.djukic@example.ba"), Phones: mobile("+387 66 345 678"), Addresses: adr("sa"), Categories: labels("Work")}},
		{key: "jelena-cyr", ct: vcard.Contact{Given: "Јелена", Family: "Јовановић", Emails: email("home", "jelena.jovanovic@example.rs"),
			Phones: mobile("+381 64 111 2233"), Addresses: adr("bg"), Birthday: on(12, 1979), Categories: labels("Friends")}},
		{key: "milos-cyr", ct: vcard.Contact{Given: "Милош", Family: "Николић", Org: "Телеком Србија", Title: "Менаџер",
			Emails: email("work", "milos.nikolic@example.rs"), Phones: mobile("+381 64 555 1234"), Categories: labels("Work")}},
		{key: "ana", ct: vcard.Contact{Prefix: "dr", Given: "Ana", Family: "Kovačević", Org: "Opšta bolnica Sarajevo", Title: "Pedijatar",
			Phones: []vcard.Field{{Label: "work", Value: "+387 33 444 555"}}, Addresses: adr("sa"), Categories: labels("Doctor"),
			Note: "Ordinacija utorkom i četvrtkom 8–14h."}},
		{key: "nikola", starred: true, ct: vcard.Contact{Given: "Nikola", Family: "Šarić", Nickname: "Šare", Emails: email("home", "nikola.saric@example.ba"),
			Phones: mobile("+387 65 987 654"), Addresses: adr("bl"), Birthday: on(1, 1992), Categories: labels("Friends")}},
		{key: "ivana", photo: true, ct: vcard.Contact{Given: "Ivana", Family: "Babić", Org: "Pocket d.o.o.", Title: "Računovođa",
			Emails: email("work", "ivana.babic@example.ba"), Phones: mobile("+387 61 234 567"), Addresses: adr("sa"), Birthday: on(7, 1987), Categories: labels("Work")}},
		{key: "zoran-maric", ct: vcard.Contact{Given: "Zoran", Family: "Marić", Phones: mobile("+387 65 444 111"), Categories: labels("Friends")}},
		{key: "zoran-1", ct: vcard.Contact{Given: "Zoran", Phones: mobile("+387 66 111 222"), Note: "Vodoinstalater."}},
		{key: "zoran-2", ct: vcard.Contact{Given: "Zoran", Phones: mobile("+387 65 999 000"), Note: "Taxi."}},
		{key: "snezana", ct: vcard.Contact{Given: "Snežana", Family: "Tomić", Phones: mobile("+387 65 300 300"), Addresses: adr("bl"),
			Birthday: on(25, 1962), Categories: labels("Family")}},
		{key: "dragan", ct: vcard.Contact{Given: "Dragan", Family: "Tomić", Phones: mobile("+387 65 300 301"), Addresses: adr("bl"),
			Anniversary: on(18, 1985), Related: []vcard.Field{{Label: "spouse", Value: "Snežana Tomić"}}, Categories: labels("Family")}},
		{key: "mirjana", ct: vcard.Contact{Given: "Mirjana", Family: "Vuković", Org: "Dental Studio Smile", Title: "Stomatolog",
			Phones: []vcard.Field{{Label: "work", Value: "051 222 333"}}, Addresses: adr("bl"), Categories: labels("Doctor")}},
		{key: "emir", photo: true, ct: vcard.Contact{Given: "Emir", Family: "Hodžić", Org: "Pocket d.o.o.", Title: "Backend developer",
			Emails: email("work", "emir.hodzic@example.ba"), Phones: mobile("+387 62 101 202"), Addresses: adr("sa"), Birthday: on(30, 1983), Categories: labels("Work")}},
		{key: "amra", ct: vcard.Contact{Given: "Amra", Family: "Begić", Emails: email("home", "amra.begic@example.ba"), Phones: mobile("+387 61 777 888"),
			Addresses: adr("sa"), Birthday: on(9, 0), Categories: labels("Friends")}},
		{key: "lejla", ct: vcard.Contact{Given: "Lejla", Family: "Mehmedović", Org: "BH Telecom", Emails: email("work", "lejla.mehmedovic@example.ba"), Categories: labels("Work")}},
		{key: "haris", ct: vcard.Contact{Given: "Haris", Family: "Delić", Phones: mobile("+387 62 333 444"), Categories: labels("Friends")}},
		{key: "tamara", photo: true, ct: vcard.Contact{Given: "Tamara", Family: "Stanković", Emails: email("home", "tamara.s@example.rs"),
			Phones: mobile("+381 65 432 1098"), Addresses: adr("bg"), Birthday: on(45, 1994), Categories: labels("Friends")}},
		{key: "stefan", ct: vcard.Contact{Given: "Stefan", Family: "Ilić", Org: "Nordlicht GmbH", Title: "QA", Emails: email("work", "stefan.ilic@example.rs"),
			Addresses: adr("bg"), Categories: labels("Work")}},
		{key: "lukas", photo: true, ct: vcard.Contact{Given: "Lukas", Family: "Becker", Org: "Nordlicht GmbH", Title: "Product Manager",
			Emails: email("work", "lukas.becker@nordlicht.example"), Phones: []vcard.Field{{Label: "work", Value: "+49 30 1234567"}}, Addresses: adr("be"),
			Birthday: on(60, 1981), Categories: labels("Work")}},
		{key: "anna", photo: true, ct: vcard.Contact{Given: "Anna", Family: "Schmidt", Emails: email("home", "anna.schmidt@example.de"),
			Phones: mobile("+49 151 2345 6789"), Addresses: adr("be"), Birthday: on(21, 1990), Categories: labels("Friends")}},
		{key: "sophie", ct: vcard.Contact{Given: "Sophie", Family: "Martin", Emails: email("home", "sophie.martin@example.fr"), Categories: labels("Friends")}},
		{key: "james", ct: vcard.Contact{Given: "James", Family: "O'Connor", Org: "Fjord Labs", Emails: email("work", "james@fjordlabs.example"), Categories: labels("Work")}},
		{key: "maria", ct: vcard.Contact{Given: "María", Family: "García", Phones: mobile("+34 600 123 456"), Birthday: on(75, 1991), Categories: labels("Friends")}},
		{key: "kenji", ct: vcard.Contact{Given: "Kenji", Family: "Tanaka", Org: "Fjord Labs", Emails: email("work", "kenji@fjordlabs.example"), Categories: labels("Work")}},
		{key: "elif", ct: vcard.Contact{Given: "Elif", Family: "Yılmaz", Emails: email("home", "elif.yilmaz@example.com.tr"), Categories: labels("Friends")}},
		{key: "ognjen", ct: vcard.Contact{Given: "Ognjen", Family: "Lukić", Phones: mobile("+387 65 010 203"), Addresses: adr("bl"), Birthday: on(100, 1990), Categories: labels("Friends")}},
		{key: "vesna", ct: vcard.Contact{Given: "Vesna", Family: "Radić", Phones: []vcard.Field{{Label: "home", Value: "051 300 400"}}, Addresses: adr("bl"),
			Birthday: on(200, 1950), Categories: labels("Family")}},
		{key: "bojan", starred: true, ct: vcard.Contact{Given: "Bojan", Family: "Jurić", Org: "Pocket d.o.o.", Title: "CTO", Emails: email("work", "bojan.juric@example.ba"),
			Phones: mobile("+387 66 900 800"), Categories: labels("Work")}},
		{key: "senad", ct: vcard.Contact{Prefix: "dr", Given: "Senad", Family: "Hadžić", Org: "Dom zdravlja Banja Luka", Title: "Porodični ljekar",
			Phones: []vcard.Field{{Label: "work", Value: "051 333 444"}}, Categories: labels("Doctor")}},
		{key: "mtel", ct: vcard.Contact{Kind: "org", Org: "m:tel", Phones: []vcard.Field{{Label: "main", Value: "0800 50 000"}},
			URLs: []vcard.Field{{Label: "homepage", Value: "https://mtel.ba"}}}},
		{key: "ers", ct: vcard.Contact{Kind: "org", Org: "Elektroprivreda Republike Srpske", Phones: []vcard.Field{{Label: "main", Value: "051 246 300"}}}},
		{key: "pekara", ct: vcard.Contact{Kind: "org", Org: "Pekara Mlinar", Phones: []vcard.Field{{Label: "main", Value: "051 215 215"}}, Addresses: adr("bl"),
			Note: "Najbolji kifli u gradu."}},
		{key: "nordlicht", ct: vcard.Contact{Kind: "org", Org: "Nordlicht GmbH", Emails: email("work", "hello@nordlicht.example"), Addresses: adr("be"), Categories: labels("Work")}},
		{key: "servis", ct: vcard.Contact{Kind: "org", Org: "Auto servis Kovač", Phones: []vcard.Field{{Label: "main", Value: "065 400 500"}}, Note: "Mali servis svakih 15.000 km."}},
		{key: "milan", ct: vcard.Contact{Given: "Milan", Family: "Savić", Emails: email("home", "milan.savic@example.ba"), Phones: mobile("+387 65 818 181"), Categories: labels("Friends")}},

		{key: "mama", family: true, ct: vcard.Contact{Given: "Radmila", Family: "Kovač", Nickname: "Mama", Phones: mobile("+387 65 100 200"), Addresses: adr("bl"),
			Birthday: on(14, 1961), Categories: labels("Family")}},
		{key: "tata", family: true, ct: vcard.Contact{Given: "Petar", Family: "Kovač", Nickname: "Tata", Phones: mobile("+387 65 100 201"), Addresses: adr("bl"),
			Birthday: on(52, 1958), Categories: labels("Family")}},
		{key: "milica", family: true, ct: vcard.Contact{Given: "Milica", Family: "Kovač", Emails: email("home", "milica.kovac@example.ba"), Phones: mobile("+387 66 100 202"),
			Birthday: on(2, 1995), Categories: labels("Family")}},
		{key: "baka", family: true, ct: vcard.Contact{Given: "Vesna", Family: "Kovač", Nickname: "Baka", Phones: []vcard.Field{{Label: "home", Value: "051 100 203"}},
			Birthday: on(33, 0), Categories: labels("Family")}},
		{key: "goran", family: true, ct: vcard.Contact{Prefix: "dr", Given: "Goran", Family: "Popović", Org: "Dom zdravlja", Title: "Porodični ljekar",
			Phones: []vcard.Field{{Label: "work", Value: "051 100 900"}}, Categories: labels("Doctor")}},
	}
}

// demoAvatar draws a made-up profile picture: a soft gradient circle with a
// light head-and-shoulders shape on it.
func demoAvatar(seed int) []byte {
	const n = 192
	pairs := [][2]color.RGBA{
		{{0xfb, 0x71, 0x85, 255}, {0xf9, 0xa8, 0xd4, 255}}, {{0x60, 0xa5, 0xfa, 255}, {0xa5, 0xb4, 0xfc, 255}},
		{{0x34, 0xd3, 0x99, 255}, {0x99, 0xf6, 0xe4, 255}}, {{0xfb, 0xbf, 0x24, 255}, {0xfd, 0xba, 0x74, 255}},
		{{0xc0, 0x84, 0xfc, 255}, {0xf0, 0xab, 0xfc, 255}}, {{0x38, 0xbd, 0xf8, 255}, {0x5e, 0xea, 0xd4, 255}},
		{{0xf8, 0x71, 0x71, 255}, {0xfc, 0xd3, 0x4d, 255}}, {{0x81, 0x8c, 0xf8, 255}, {0x67, 0xe8, 0xf9, 255}},
	}
	p := pairs[seed%len(pairs)]
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	mix := func(a, b uint8, t float64) uint8 { return uint8(float64(a)*(1-t) + float64(b)*t) }
	c := float64(n) / 2
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			t := (float64(x) + float64(y)) / float64(2*n)
			col := color.RGBA{mix(p[0].R, p[1].R, t), mix(p[0].G, p[1].G, t), mix(p[0].B, p[1].B, t), 255}
			dx, dy := float64(x)-c, float64(y)-c
			if math.Hypot(dx, dy) > c {
				// outside the circle: a pale wash of the same colours
				col = color.RGBA{mix(col.R, 255, 0.7), mix(col.G, 255, 0.7), mix(col.B, 255, 0.7), 255}
			} else {
				head := math.Hypot(float64(x)-c, float64(y)-n*0.40) < n*0.16
				ex, ey := (float64(x)-c)/(n*0.30), (float64(y)-n*0.92)/(n*0.24)
				if head || ex*ex+ey*ey < 1 {
					col = color.RGBA{mix(col.R, 255, 0.78), mix(col.G, 255, 0.78), mix(col.B, 255, 0.78), 255}
				}
			}
			img.SetRGBA(x, y, col)
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: 85})
	return b.Bytes()
}
