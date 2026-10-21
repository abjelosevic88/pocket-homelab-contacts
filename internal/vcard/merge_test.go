package vcard

import (
	"strings"
	"testing"
)

func TestMerge(t *testing.T) {
	a := Contact{Given: "Marko", Family: "Petrović", FN: "Marko Petrović",
		Emails:     []Field{{Label: "home", Value: "marko@icloud.com", Pref: true}},
		Phones:     []Field{{Label: "mobile", Value: "+387 65 123 456"}},
		Birthday:   "--05-03",
		Note:       "Met at the fair.",
		Categories: []string{"Friends"},
	}
	b := Contact{Given: "marko", Family: "petrovic", Org: "Acme",
		Emails:     []Field{{Label: "work", Value: "Marko@iCloud.com"}, {Label: "work", Value: "m.petrovic@acme.ba", Pref: true}},
		Phones:     []Field{{Label: "work", Value: "065/123-456"}, {Label: "home", Value: "051 222 333"}},
		Addresses:  []Address{{Label: "home", Street: "Kralja Petra 12", City: "Banja Luka"}},
		Birthday:   "1988-05-03",
		Note:       "Plays chess.",
		Categories: []string{"friends", "Work"},
	}
	m := Merge(a, b)
	if m.Given != "Marko" || m.Family != "Petrović" || m.FN != "Marko Petrović" {
		t.Fatalf("name: %+v", m)
	}
	if len(m.Emails) != 2 || m.Emails[1].Value != "m.petrovic@acme.ba" || m.Emails[1].Pref {
		t.Fatalf("emails: %+v", m.Emails)
	}
	if len(m.Phones) != 2 || m.Phones[1].Value != "051 222 333" {
		t.Fatalf("phones: %+v", m.Phones)
	}
	if len(m.Addresses) != 1 || m.Org != "Acme" || m.Birthday != "1988-05-03" {
		t.Fatalf("merged: %+v", m)
	}
	if m.Note != "Met at the fair.\n\nPlays chess." {
		t.Fatalf("note: %q", m.Note)
	}
	if strings.Join(m.Categories, "|") != "Friends|Work" {
		t.Fatalf("categories: %v", m.Categories)
	}
	// merging the same card again changes nothing
	if again := Merge(m, b); again.Note != m.Note || len(again.Phones) != 2 || len(again.Emails) != 2 {
		t.Fatalf("not idempotent: %+v", again)
	}
	if len(a.Emails) != 1 || len(a.Phones) != 1 {
		t.Fatal("Merge changed its argument")
	}
}
