package carddav_test

import (
	"context"
	"errors"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/carddav"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/carddav/carddavtest"
)

func TestDiscoveryAndByteExactFetch(t *testing.T) {
	ctx := context.Background()
	f := carddavtest.New("ann", "pw")
	f.Root = "/remote.php/dav"
	defer f.Close()
	f.AddBook("contacts", "Contacts", false)
	f.AddBook("z-server-generated--system", "System", true)
	card := carddavtest.Card("u1", "Marko Petrović", "item1.TEL;type=CELL:+387 65 123 456", "item1.X-ABLabel:_$!<Mobile>!$_", "NOTE:line one\\nline two")
	f.PutObject("contacts", "u1.vcf", card)

	// the server root only: discovery goes through /.well-known/carddav
	cl := carddav.New(f.URL, "ann", "pw")
	home, err := cl.Home(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if home != "/remote.php/dav/addressbooks/users/ann/" {
		t.Fatalf("home = %q", home)
	}
	books, err := cl.AddressBooks(ctx, home)
	if err != nil || len(books) != 2 {
		t.Fatalf("books = %+v, %v", books, err)
	}
	if books[0].Name != "Contacts" || books[0].ReadOnly || !books[1].ReadOnly || books[0].SyncToken == "" || books[0].CTag == "" {
		t.Fatalf("books = %+v", books)
	}
	list, err := cl.ListETags(ctx, books[0].Href)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	res, err := cl.Multiget(ctx, books[0].Href, []string{list[0].Href})
	if err != nil || len(res) != 1 {
		t.Fatalf("multiget = %+v, %v", res, err)
	}
	if res[0].Data != card {
		t.Fatalf("the vCard changed on the way:\n%q\n%q", res[0].Data, card)
	}
	if res[0].ETag != list[0].ETag || res[0].LastModified.IsZero() {
		t.Fatalf("resource = %+v", res[0])
	}

	// conditional writes
	if _, err := cl.Put(ctx, list[0].Href, card, `"stale"`, false); !errors.Is(err, carddav.ErrPrecondition) {
		t.Fatalf("stale If-Match: %v", err)
	}
	if _, err := cl.Put(ctx, list[0].Href, card, "", true); !errors.Is(err, carddav.ErrPrecondition) {
		t.Fatalf("If-None-Match on an existing card: %v", err)
	}
	f.NoETag = true
	et, err := cl.Put(ctx, list[0].Href, card, list[0].ETag, false)
	if err != nil || et == "" || et == list[0].ETag {
		t.Fatalf("put without an ETag answer: %q, %v", et, err)
	}

	// sync-collection
	_, tok, err := cl.Sync(ctx, books[0].Href, "")
	if err != nil || tok == "" {
		t.Fatalf("sync: %q %v", tok, err)
	}
	f.DeleteObject("contacts", "u1.vcf")
	ch, _, err := cl.Sync(ctx, books[0].Href, tok)
	if err != nil || len(ch) != 1 || !ch[0].Deleted {
		t.Fatalf("changes = %+v, %v", ch, err)
	}
	if _, _, err := cl.Sync(ctx, books[0].Href, "http://fake.example/sync/99999"); !errors.Is(err, carddav.ErrInvalidToken) {
		t.Fatalf("unknown token: %v", err)
	}

	bad := carddav.New(f.URL+"/remote.php/dav", "ann", "wrong")
	if _, err := bad.Home(ctx); !errors.Is(err, carddav.ErrAuth) {
		t.Fatalf("wrong password: %v", err)
	}
}
