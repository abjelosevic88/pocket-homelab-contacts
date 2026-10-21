package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/charmap"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/vcard"
)

const qpCard21 = "BEGIN:VCARD\r\nVERSION:2.1\r\nN;CHARSET=UTF-8;ENCODING=QUOTED-PRINTABLE:=C4=8Cavi=C4=87;Dragana;;;\r\n" +
	"FN;CHARSET=UTF-8;ENCODING=QUOTED-PRINTABLE:Dragana =C4=8Cavi=C4=87\r\nTEL;CELL;PREF:+387 65 000 111\r\nEMAIL;INTERNET:dragana@example.ba\r\n" +
	"NOTE;ENCODING=QUOTED-PRINTABLE:Prvi red=0D=0A=\r\nDrugi red\r\nEND:VCARD\r\n"

const googleCSV = "First Name,Middle Name,Last Name,Nickname,Organization Name,Organization Title,Birthday,Notes,Labels," +
	"E-mail 1 - Label,E-mail 1 - Value,E-mail 2 - Label,E-mail 2 - Value,Phone 1 - Label,Phone 1 - Value," +
	"Address 1 - Label,Address 1 - Street,Address 1 - City,Address 1 - Postal Code,Address 1 - Country\n" +
	"Jovan,,Jovanović,Joca,Acme,Developer,1990-04-12,Met in Novi Sad,* myContacts ::: Friends," +
	"* Home,jovan@example.rs ::: jovan.j@example.rs,Work,jovan@acme.example,Mobile,+381 64 123 4567,Home,Bulevar 1,Novi Sad,21000,Srbija\n"

const googleOldCSV = "Name,Given Name,Family Name,Birthday,Group Membership,E-mail 1 - Type,E-mail 1 - Value,Phone 1 - Type,Phone 1 - Value,Phone 2 - Type,Phone 2 - Value\n" +
	"Mira Simić,Mira,Simić, --07-15,* myContacts ::: Work,* Other,mira@example.ba,Mobile,065 555 777,Work,051 444 555 ::: 051 444 556\n"

const outlookCSV = "\xef\xbb\xbf\"First Name\",\"Middle Name\",\"Last Name\",\"Title\",\"Suffix\",\"E-mail Address\",\"E-mail 2 Address\",\"Mobile Phone\",\"Home Phone\"," +
	"\"Business Phone\",\"Company\",\"Job Title\",\"Birthday\",\"Notes\",\"Home Street\",\"Home City\",\"Home Postal Code\",\"Home Country/Region\",\"Categories\"\r\n" +
	"\"Petra\",\"\",\"Novak\",\"Mrs.\",\"\",\"petra@example.si\",\"p.novak@work.example\",\"+386 41 111 222\",\"01 222 333\",\"01 444 555\",\"Novak d.o.o.\"," +
	"\"Direktorica\",\"5/3/1988\",\"Likes tea\",\"Slovenska 1\",\"Ljubljana\",\"1000\",\"Slovenia\",\"Work;VIP\"\r\n"

func serbianCSV(t *testing.T) []byte {
	b, err := charmap.Windows1250.NewEncoder().Bytes([]byte("Ime;Prezime;Mobitel;Email;Grad;Rođendan\r\nŽeljko;Đurić;065 321 654;zeljko@example.ba;Banja Luka;3.5.1980.\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestImportFormats(t *testing.T) {
	s, h := apiServer(t)
	alice := apiPerson(t, s, h, "alice@example.com", "Alice")
	files := map[string][]byte{
		"old-phone.vcf": []byte(qpCard21), "google.csv": []byte(googleCSV), "google-old.csv": []byte(googleOldCSV),
		"outlook.csv": []byte(outlookCSV), "imenik.csv": serbianCSV(t),
	}
	ctype, body := multipartBody(t, map[string]string{"dry_run": "1"}, files)
	var res ImportResult
	code, out, _ := alice.raw("POST", "/api/v1/import", ctype, body)
	if code != 200 || !strings.Contains(string(out), `"preview"`) {
		t.Fatalf("dry run: %d %s", code, out)
	}
	_ = json.Unmarshal(out, &res)
	if res.Created != 5 || len(res.Preview) != 5 || len(res.Errors) != 0 {
		t.Fatalf("dry run: %s", out)
	}
	var list struct {
		Contacts []contactJSON `json:"contacts"`
	}
	alice.json("GET", "/api/v1/contacts", nil, 200, &list)
	if len(list.Contacts) != 0 {
		t.Fatal("a dry run wrote contacts")
	}

	ctype, body = multipartBody(t, nil, files)
	code, out, _ = alice.raw("POST", "/api/v1/import", ctype, body)
	_ = json.Unmarshal(out, &res)
	if code != 200 || res.Created != 5 || res.Failed != 0 {
		t.Fatalf("import: %d %s", code, out)
	}
	byName := map[string]contactJSON{}
	alice.json("GET", "/api/v1/contacts", nil, 200, &list)
	for _, c := range list.Contacts {
		var full contactJSON
		alice.json("GET", fmt.Sprintf("/api/v1/contacts/%d", c.ID), nil, 200, &full)
		byName[c.FN] = full
	}

	d := byName["Dragana Čavić"]
	if len(d.Phones) != 1 || d.Card.Note != "Prvi red\r\nDrugi red" && d.Card.Note != "Prvi red\nDrugi red" || d.Source != "import" {
		t.Fatalf("vCard 2.1: %+v", d)
	}
	if _, vcf, _ := alice.raw("GET", fmt.Sprintf("/api/v1/contacts/%d/vcf", d.ID), "", nil); !strings.Contains(string(vcf), "VERSION:3.0") {
		t.Fatalf("not upgraded: %s", vcf)
	}

	j := byName["Jovan Jovanović"]
	if len(j.Emails) != 3 || !j.Card.Emails[0].Pref || j.Card.Emails[2].Label != "work" || j.Birthday != "1990-04-12" ||
		strings.Join(j.Categories, ",") != "Friends" || j.Card.Nickname != "Joca" || j.Card.Org != "Acme" ||
		len(j.Card.Addresses) != 1 || j.Card.Addresses[0].City != "Novi Sad" || j.Card.Phones[0].Label != "mobile" {
		t.Fatalf("google csv: %+v", j)
	}

	m := byName["Mira Simić"]
	if len(m.Phones) != 3 || m.Birthday != "--07-15" || strings.Join(m.Categories, ",") != "Work" || m.Card.Phones[1].Label != "work" {
		t.Fatalf("old google csv: %+v", m)
	}

	p := byName["Mrs. Petra Novak"]
	if p.ID == 0 {
		t.Fatalf("outlook csv not found among %v", names(list.Contacts))
	}
	if p.Card.Prefix != "Mrs." || p.Card.Title != "Direktorica" || p.Birthday != "1988-05-03" || len(p.Emails) != 2 ||
		len(p.Card.Phones) != 3 || p.Card.Phones[0].Label != "mobile" || p.Card.Phones[1].Label != "home" || p.Card.Phones[2].Label != "work" ||
		strings.Join(p.Categories, ",") != "Work,VIP" || len(p.Card.Addresses) != 1 || p.Card.Addresses[0].Country != "Slovenia" {
		t.Fatalf("outlook csv: %+v", p.Card)
	}

	z := byName["Željko Đurić"]
	if z.Birthday != "1980-05-03" || len(z.Phones) != 1 || len(z.Emails) != 1 || len(z.Card.Addresses) != 1 || z.Card.Addresses[0].City != "Banja Luka" {
		t.Fatalf("serbian csv: %+v", z)
	}

	// the same files again change nothing
	ctype, body = multipartBody(t, nil, files)
	_, out, _ = alice.raw("POST", "/api/v1/import", ctype, body)
	_ = json.Unmarshal(out, &res)
	if res.Created != 0 || res.Skipped != 5 || res.Merged != 0 {
		t.Fatalf("re-import: %s", out)
	}
	// a new phone number for a known person merges
	more := "First Name,Last Name,E-mail 1 - Value,Phone 1 - Label,Phone 1 - Value\nJovan,Jovanović,jovan@example.rs,Work,+381 11 222 333\n"
	ctype, body = multipartBody(t, nil, map[string][]byte{"more.csv": []byte(more)})
	_, out, _ = alice.raw("POST", "/api/v1/import", ctype, body)
	_ = json.Unmarshal(out, &res)
	if res.Merged != 1 || res.Created != 0 {
		t.Fatalf("merge import: %s", out)
	}
	alice.json("GET", fmt.Sprintf("/api/v1/contacts/%d", j.ID), nil, 200, &j)
	if len(j.Phones) != 2 || j.Card.Nickname != "Joca" {
		t.Fatalf("merged: %+v", j)
	}
}

func TestDuplicatesAndMerge(t *testing.T) {
	s, h := apiServer(t)
	alice := apiPerson(t, s, h, "alice@example.com", "Alice")
	alice.create(vcard.Contact{Given: "Zoran", Phones: []vcard.Field{{Value: "+387 66 111 222"}}})
	alice.create(vcard.Contact{Given: "Zoran", Phones: []vcard.Field{{Value: "+387 65 999 000"}}})
	a := alice.create(vcard.Contact{Given: "Marko", Family: "Petrović", Emails: []vcard.Field{{Label: "home", Value: "marko@icloud.com"}},
		Phones: []vcard.Field{{Label: "mobile", Value: "+387 65 123 456"}}, Categories: []string{"Friends"}, Note: "From iCloud."})
	b := alice.create(vcard.Contact{Given: "marko", Family: "petrovic", Org: "Mtel", Emails: []vcard.Field{{Label: "work", Value: "marko@gmail.com"}},
		Birthday: "1988-05-03", Note: "From Gmail."})
	var groups []DupGroup
	alice.json("GET", "/api/v1/duplicates", nil, 200, &groups)
	if len(groups) != 1 || len(groups[0].Contacts) != 2 || groups[0].Reason != "same name" {
		t.Fatalf("duplicates: %+v", groups)
	}
	var merged contactJSON
	alice.json("POST", "/api/v1/duplicates/merge", map[string]any{"ids": []int64{a.ID, b.ID}, "primary_id": a.ID}, 200, &merged)
	if merged.ID != a.ID || merged.FN != "Marko Petrović" || merged.UID != a.UID || len(merged.Emails) != 2 || len(merged.Phones) != 1 ||
		merged.Birthday != "1988-05-03" || merged.Card.Org != "Mtel" || merged.Card.Note != "From iCloud.\n\nFrom Gmail." {
		t.Fatalf("merged: %+v", merged)
	}
	alice.json("GET", fmt.Sprintf("/api/v1/contacts/%d", b.ID), nil, 404, nil)
	var deleted []map[string]any
	alice.json("GET", "/api/v1/deleted", nil, 200, &deleted)
	if len(deleted) != 1 {
		t.Fatalf("the other one is not in Recently deleted: %+v", deleted)
	}
	alice.json("GET", "/api/v1/duplicates", nil, 200, &groups)
	if len(groups) != 0 {
		t.Fatalf("after merge: %+v", groups)
	}
	// "not duplicates" is remembered
	c := alice.create(vcard.Contact{Given: "Ana", Family: "Kovač"})
	d := alice.create(vcard.Contact{Given: "ana", Family: "kovac"})
	alice.json("GET", "/api/v1/duplicates", nil, 200, &groups)
	if len(groups) != 1 {
		t.Fatalf("ana: %+v", groups)
	}
	alice.json("POST", "/api/v1/duplicates/ignore", map[string]any{"ids": []int64{c.ID, d.ID}}, 204, nil)
	alice.json("GET", "/api/v1/duplicates", nil, 200, &groups)
	if len(groups) != 0 {
		t.Fatalf("ignored pair still listed: %+v", groups)
	}
}

func TestBirthdays(t *testing.T) {
	s, h := apiServer(t)
	alice := apiPerson(t, s, h, "alice@example.com", "Alice")
	alice.create(vcard.Contact{Given: "Leap", Family: "Day", Birthday: "2000-02-29"})
	alice.create(vcard.Contact{Given: "No", Family: "Year", Birthday: "--03-01"})
	alice.create(vcard.Contact{Given: "Birthday", Family: "Today", Birthday: "1990-06-15"})
	alice.create(vcard.Contact{Given: "Wed", Family: "Ding", Anniversary: "2010-06-20", Related: []vcard.Field{{Label: "spouse", Value: "Ana Ding"}}})
	type entry struct {
		Contact  contactJSON `json:"contact"`
		Kind     string      `json:"kind"`
		Date     string      `json:"date"`
		Original string      `json:"original"`
		Age      *int        `json:"age"`
		Days     int         `json:"days"`
	}
	var list []entry
	alice.json("GET", "/api/v1/birthdays?from=2027-02-01&days=60", nil, 200, &list)
	if len(list) != 2 || list[0].Contact.FN != "Leap Day" || list[0].Date != "2027-02-28" || list[0].Age == nil || *list[0].Age != 27 || list[0].Days != 27 {
		t.Fatalf("leap day: %+v", list)
	}
	if list[1].Contact.FN != "No Year" || list[1].Age != nil || list[1].Date != "2027-03-01" || list[1].Original != "--03-01" {
		t.Fatalf("no year: %+v", list[1])
	}
	alice.json("GET", "/api/v1/birthdays?from=2028-02-01&days=29", nil, 200, &list)
	if len(list) != 1 || list[0].Date != "2028-02-29" {
		t.Fatalf("leap year: %+v", list)
	}
	alice.json("GET", "/api/v1/birthdays?from=2026-06-15&days=7", nil, 200, &list)
	if len(list) != 2 || list[0].Days != 0 || *list[0].Age != 36 || list[1].Kind != "anniversary" || *list[1].Age != 16 || list[1].Days != 5 {
		t.Fatalf("today: %+v", list)
	}

	// the feed
	var feeds map[string]string
	alice.json("GET", "/api/v1/feeds", nil, 200, &feeds)
	path := strings.TrimPrefix(feeds["birthdays_url"], "https://contacts.example")
	if !strings.HasPrefix(path, "/feed/birthdays/") || !strings.HasPrefix(feeds["birthdays_webcal"], "webcal://") {
		t.Fatalf("feeds: %+v", feeds)
	}
	anon := &apiClient{t: t, h: h}
	code, ics, hdr := anon.raw("GET", path, "", nil)
	if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/calendar") {
		t.Fatalf("feed: %d %s", code, hdr.Get("Content-Type"))
	}
	body := string(ics)
	for _, want := range []string{"BEGIN:VCALENDAR", "RRULE:FREQ=YEARLY\r\n", "RRULE:FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=-1", "SUMMARY:🎂 Leap Day",
		"SUMMARY:💍 Wed Ding & Ana", "DTSTART;VALUE=DATE:20000301", "DTSTART;VALUE=DATE:19900615", "END:VCALENDAR"} {
		if !strings.Contains(body, want) {
			t.Errorf("feed lacks %q:\n%s", want, body)
		}
	}
	if strings.Count(body, "BEGIN:VEVENT") != 4 || strings.Count(body, "BEGIN:VEVENT") != strings.Count(body, "END:VEVENT") {
		t.Errorf("feed events:\n%s", body)
	}
	// a rotated secret retires the old address
	var rotated map[string]string
	alice.json("POST", "/api/v1/feeds/rotate", nil, 200, &rotated)
	if code, _, _ := anon.raw("GET", path, "", nil); code != 404 || rotated["birthdays_url"] == feeds["birthdays_url"] {
		t.Fatalf("old feed still answers: %d", code)
	}
}

func TestBirthdayReminders(t *testing.T) {
	s, h := apiServer(t)
	alice := apiPerson(t, s, h, "alice@example.com", "Alice")
	ctx := context.Background()
	_ = s.store.SetUserSetting(ctx, alice.user.ID, "birthday_time", "08:00")
	_ = s.store.SetUserSetting(ctx, alice.user.ID, "birthday_ahead", "3")
	alice.create(vcard.Contact{Given: "Marko", Family: "Petrović", Birthday: "1988-06-15"})
	alice.create(vcard.Contact{Given: "Ana", Family: "Kovač", Birthday: "--06-18"})
	loc := s.loc(ctx, alice.user.ID)
	if title, _, _ := s.remindPerson(ctx, alice.user.ID, time.Date(2026, 6, 15, 7, 59, 0, 0, loc)); title != "" {
		t.Fatalf("too early: %q", title)
	}
	title, body, _ := s.remindPerson(ctx, alice.user.ID, time.Date(2026, 6, 15, 8, 1, 0, 0, loc))
	if title != "🎂 Birthdays today" || !strings.Contains(body, "Marko Petrović turns 38") || !strings.Contains(body, "In 3 days") || !strings.Contains(body, "Ana Kovač's birthday") {
		t.Fatalf("reminder: %q %q", title, body)
	}
	if title, _, _ := s.remindPerson(ctx, alice.user.ID, time.Date(2026, 6, 15, 8, 2, 0, 0, loc)); title != "" {
		t.Fatalf("sent twice: %q", title)
	}
	// a server that was down at 08:00 does not send at noon
	if title, _, _ := s.remindPerson(ctx, alice.user.ID, time.Date(2026, 6, 16, 12, 0, 0, 0, loc)); title != "" {
		t.Fatalf("sent late: %q", title)
	}
}

func TestSearchFolding(t *testing.T) {
	s, h := apiServer(t)
	alice := apiPerson(t, s, h, "alice@example.com", "Alice")
	alice.create(vcard.Contact{Given: "Đorđe", Family: "Ćosić"})
	alice.create(vcard.Contact{Given: "Djordje", Family: "Marković"})
	alice.create(vcard.Contact{Given: "Јелена", Family: "Јовановић"})
	alice.create(vcard.Contact{Given: "Phone", Family: "Person", Phones: []vcard.Field{{Value: "+38765123456"}}})
	alice.create(vcard.Contact{Given: "Ana", Family: "Djokić"})
	var res struct {
		Contacts []contactJSON `json:"contacts"`
	}
	for q, want := range map[string]string{
		"djordje": "Djordje Marković, Đorđe Ćosić", "Đorđe": "Djordje Marković, Đorđe Ćosić", "dorde": "Đorđe Ćosić",
		"jelena": "Јелена Јовановић", "Јелена": "Јелена Јовановић", "065 123": "Phone Person", "123456": "Phone Person",
		"cosic": "Đorđe Ćosić",
	} {
		alice.json("GET", "/api/v1/search?q="+strings.ReplaceAll(q, " ", "%20"), nil, 200, &res)
		got := []string{}
		for _, c := range res.Contacts {
			got = append(got, c.FN)
		}
		if len(got) == 2 && got[0] > got[1] {
			got[0], got[1] = got[1], got[0]
		}
		if strings.Join(got, ", ") != want {
			t.Errorf("search %q: got %q, want %q", q, strings.Join(got, ", "), want)
		}
	}
	// a name that starts with the query comes before one that only has it in a later word
	alice.json("GET", "/api/v1/search?q=dj", nil, 200, &res)
	if len(res.Contacts) != 3 || res.Contacts[2].FN != "Ana Djokić" {
		t.Fatalf("ranking: %s", names(res.Contacts))
	}
	var sug []map[string]any
	alice.json("GET", "/api/v1/suggest?q=phone", nil, 200, &sug)
	if len(sug) != 1 || sug[0]["phone"] != "+38765123456" {
		t.Fatalf("suggest: %+v", sug)
	}
	var look struct {
		Contacts []contactJSON `json:"contacts"`
	}
	alice.json("GET", "/api/v1/lookup?phone=065%2F123-456", nil, 200, &look)
	if len(look.Contacts) != 1 {
		t.Fatalf("lookup: %+v", look)
	}
}

func TestPhotoUploadAndThumbnail(t *testing.T) {
	s, h := apiServer(t)
	alice := apiPerson(t, s, h, "alice@example.com", "Alice")
	ct := alice.create(vcard.Contact{Given: "Pic", Family: "Ture"})
	src := image.NewRGBA(image.Rect(0, 0, 900, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 900; x++ {
			src.Set(x, y, color.RGBA{uint8(x), uint8(y), 120, 255})
		}
	}
	var pngData bytes.Buffer
	_ = png.Encode(&pngData, src)
	path := fmt.Sprintf("/api/v1/contacts/%d/photo", ct.ID)
	code, out, _ := alice.raw("PUT", path, "image/png", pngData.Bytes())
	var got contactJSON
	_ = json.Unmarshal(out, &got)
	if code != 200 || !got.HasPhoto || !strings.HasPrefix(got.PhotoURL, path+"?v=") {
		t.Fatalf("upload: %d %s", code, out)
	}
	code, thumb, hdr := alice.raw("GET", path+"?s=64", "", nil)
	if code != 200 || hdr.Get("Content-Type") != "image/jpeg" || hdr.Get("ETag") == "" {
		t.Fatalf("thumbnail: %d %v", code, hdr)
	}
	img, err := jpeg.Decode(bytes.NewReader(thumb))
	if err != nil || img.Bounds().Dx() != 64 || img.Bounds().Dy() != 64 {
		t.Fatalf("thumbnail is not a 64 px JPEG: %v %v", err, img)
	}
	_, orig, _ := alice.raw("GET", path+"?s=0", "", nil)
	if img, err := jpeg.Decode(bytes.NewReader(orig)); err != nil || img.Bounds().Dx() != 512 || img.Bounds().Dy() != 341 {
		t.Fatalf("stored picture: %v %v", err, img.Bounds())
	}
	req := httptest.NewRequest("GET", path+"?s=64", nil)
	req.Header.Set("Authorization", "Bearer "+alice.token)
	req.Header.Set("If-None-Match", hdr.Get("ETag"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 304 {
		t.Fatalf("If-None-Match: %d", w.Code)
	}
	alice.json("PUT", path, nil, 400, nil)
	alice.json("DELETE", path, nil, 200, &got)
	if got.HasPhoto {
		t.Fatal("photo not removed")
	}
	alice.json("GET", path, nil, 404, nil)
}

func TestDemoData(t *testing.T) {
	s, h := apiServer(t)
	if err := s.seedDemo(context.Background()); err != nil {
		t.Fatal(err)
	}
	admin := &apiClient{t: t, h: h}
	u, _ := s.store.GetUserByEmail(context.Background(), "admin@localhost")
	tok, _, _ := s.store.CreateAPIToken(context.Background(), u.ID, "t")
	admin.token = tok
	var books []bookJSON
	admin.json("GET", "/api/v1/addressbooks", nil, 200, &books)
	if len(books) != 2 {
		t.Fatalf("books: %+v", books)
	}
	var list struct {
		Contacts []contactJSON `json:"contacts"`
		Total    int           `json:"total"`
	}
	admin.json("GET", "/api/v1/contacts", nil, 200, &list)
	photos := 0
	for _, c := range list.Contacts {
		if c.PhotoURL != "" {
			photos++
		}
	}
	if list.Total < 40 || photos != 8 {
		t.Fatalf("demo: %d contacts, %d photos", list.Total, photos)
	}
	var groups []DupGroup
	admin.json("GET", "/api/v1/duplicates", nil, 200, &groups)
	if len(groups) != 1 {
		t.Fatalf("demo duplicates: %+v", groups)
	}
	// a second run leaves a box with contacts alone
	if err := s.seedDemo(context.Background()); err != nil {
		t.Fatal(err)
	}
	admin.json("GET", "/api/v1/contacts", nil, 200, &list)
	if list.Total < 40 || list.Total > 50 {
		t.Fatalf("demo seeded twice: %d", list.Total)
	}
}

func TestRelatedApps(t *testing.T) {
	s, h := apiServer(t)
	alice := apiPerson(t, s, h, "alice@example.com", "Alice")
	ct := alice.create(vcard.Contact{Given: "Marko", Family: "Petrović", Emails: []vcard.Field{{Value: "marko@example.com"}}})
	page := fmt.Sprintf("https://contacts.example/contacts/%d", ct.ID)
	var posted []map[string]any
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/search": // Pocket Calendar
			fmt.Fprint(w, `[{"id":"7:20991021T080000Z","title":"Lunch","start":"2099-10-21T10:00:00+02:00","end":"2099-10-21T11:00:00+02:00","all_day":false,
				"attendees":[{"email":"Marko@example.com"}]},
				{"id":"3","title":"Kickoff","start":"2020-01-02","end":"2020-01-03","all_day":true,"organizer":{"email":"marko@example.com"}},
				{"id":"9","title":"Marko's talk","start":"2099-01-01T10:00:00Z","end":"2099-01-01T11:00:00Z","attendees":[{"email":"other@example.com"}]}]`)
		case "GET /api/v1/mentions": // Pocket Notes
			if r.URL.Query().Get("url") != page {
				t.Errorf("mentions asked for %q", r.URL.Query().Get("url"))
			}
			fmt.Fprint(w, `[{"id":401450,"title":"Kickoff notes","excerpt":"Marko said…","updated_at":"2026-10-20T10:00:00Z"}]`)
		case "GET /api/v1/tasks": // Pocket Tasks (without the url filter yet)
			fmt.Fprintf(w, `{"tasks":[{"id":9,"title":"Call Marko back","due":"2026-10-23","status":"open","url":%q},
				{"id":10,"title":"Unrelated","status":"open","notes":"see https://contacts.example/contacts/999"},
				{"id":11,"title":"Send offer","status":"open","notes":"About [Marko](%s)."}],"total":3}`, page, page)
		case "POST /api/v1/tasks", "POST /api/v1/events":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			posted = append(posted, body)
			w.WriteHeader(201)
			if strings.HasSuffix(r.URL.Path, "tasks") {
				fmt.Fprint(w, `{"id":12,"title":"Call Marko","due":"2026-10-24","status":"open"}`)
			} else {
				fmt.Fprint(w, `{"id":"44","title":"Coffee","start":"2026-10-22T10:00:00+02:00","end":"2026-10-22T11:00:00+02:00","all_day":false}`)
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer fake.Close()
	ctx := context.Background()
	for _, app := range []string{"calendar", "notes", "tasks"} {
		_ = s.store.SetUserSetting(ctx, alice.user.ID, "app.mode."+app, "direct")
		_ = s.store.SetUserSetting(ctx, alice.user.ID, "app.url."+app, fake.URL)
		_ = s.store.SetUserSetting(ctx, alice.user.ID, "app.token."+app, "tok")
	}
	var rel map[string]RelatedSection
	alice.json("GET", fmt.Sprintf("/api/v1/contacts/%d/related", ct.ID), nil, 200, &rel)
	cal := rel["calendar"]
	if cal.Status != "ok" || len(cal.Items) != 2 || cal.Items[0]["title"] != "Lunch" || cal.Items[0]["past"] != false ||
		cal.Items[1]["past"] != true || cal.Items[0]["url"] != fake.URL+"/event/7:20991021T080000Z" {
		t.Fatalf("calendar: %+v", cal)
	}
	notes := rel["notes"]
	if notes.Status != "ok" || len(notes.Items) != 1 || notes.Items[0]["id"] != "401450" || notes.Items[0]["url"] != fake.URL+"/note/401450" ||
		!strings.Contains(notes.NewURL, "folder=People") || !strings.Contains(notes.NewURL, "url=https%3A%2F%2Fcontacts.example%2Fcontacts%2F") {
		t.Fatalf("notes: %+v", notes)
	}
	tasks := rel["tasks"]
	if tasks.Status != "ok" || len(tasks.Items) != 2 || tasks.Items[0]["title"] != "Call Marko back" || tasks.Items[1]["title"] != "Send offer" {
		t.Fatalf("tasks: %+v", tasks)
	}
	var item struct {
		Item map[string]any `json:"item"`
	}
	alice.json("POST", fmt.Sprintf("/api/v1/contacts/%d/task", ct.ID), map[string]any{"title": "Call Marko", "due": "2026-10-24"}, 201, &item)
	if item.Item["url"] != fake.URL+"/today?task=12" || posted[0]["url"] != page {
		t.Fatalf("task: %+v %+v", item, posted)
	}
	alice.json("POST", fmt.Sprintf("/api/v1/contacts/%d/event", ct.ID), map[string]any{"title": "Coffee", "start": "2026-10-22T10:00", "duration_min": 45}, 201, &item)
	att, _ := posted[1]["attendees"].([]any)
	if item.Item["id"] != "44" || posted[1]["end"] != "2026-10-22T10:45" || len(att) != 1 {
		t.Fatalf("event: %+v %+v", item, posted[1])
	}
	// one app off, one broken: the others still answer
	_ = s.store.SetUserSetting(ctx, alice.user.ID, "app.mode.notes", "off")
	_ = s.store.SetUserSetting(ctx, alice.user.ID, "app.token.tasks", "wrong")
	alice.json("GET", fmt.Sprintf("/api/v1/contacts/%d/related", ct.ID), nil, 200, &rel)
	if rel["notes"].Status != "off" || rel["tasks"].Status != "error" || rel["tasks"].Detail == "" || rel["calendar"].Status != "ok" {
		t.Fatalf("independent sections: %+v", rel)
	}
}
