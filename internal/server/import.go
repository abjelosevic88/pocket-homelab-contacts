package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"reflect"
	"strconv"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/store"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/vcard"
)

// Import reads .vcf (any version; 2.1 is upgraded to 3.0), .csv (import_csv.go)
// and .zip files of them into one address book (design.md rule 6). A card is
// matched by its UID in that book, then by the duplicate rules; "merge"
// (default) folds it into the match, "skip" leaves the match alone, "add"
// always creates. Re-importing the same file changes nothing. Imported
// contacts get source "import"; an import publishes no Core event per
// contact (a 400-contact import would flood every subscriber).

const (
	importMaxBody  = 64 << 20
	importMaxFiles = 2000
)

type importFile struct {
	name string
	data []byte
}

// importCard is one contact read from the upload.
type importCard struct {
	card  *vcard.Card
	where string // for error messages: "contacts.csv line 31", "card 3"
}

// ImportResult is the answer of POST /import.
type ImportResult struct {
	Total   int             `json:"total"`
	Created int             `json:"created"`
	Merged  int             `json:"merged"`
	Skipped int             `json:"skipped"`
	Failed  int             `json:"failed"`
	Errors  []string        `json:"errors"`
	Preview []importPreview `json:"preview,omitempty"`
}

type importPreview struct {
	FN      string   `json:"fn"`
	Emails  []string `json:"emails"`
	Phones  []string `json:"phones"`
	Action  string   `json:"action"` // create | merge | skip
	MatchID *int64   `json:"match_id"`
}

type importOptions struct {
	book   int64
	mode   string // merge | skip | add
	dryRun bool
}

func truthy(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

// readImport reads the files and options of an import request.
func readImport(r *http.Request) ([]importFile, importOptions, error) {
	opt := importOptions{book: queryInt64(r, "addressbook_id"), mode: r.URL.Query().Get("duplicates"), dryRun: queryBool(r, "dry_run")}
	var files []importFile
	ct := r.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "multipart/"):
		if err := r.ParseMultipartForm(importMaxBody); err != nil {
			return nil, opt, badRequest("the upload is too large (64 MB at most)")
		}
		if v := r.FormValue("addressbook_id"); v != "" {
			opt.book, _ = strconv.ParseInt(v, 10, 64)
		}
		if v := r.FormValue("duplicates"); v != "" {
			opt.mode = v
		}
		if v := r.FormValue("dry_run"); v != "" {
			opt.dryRun = truthy(v)
		}
		for _, fhs := range r.MultipartForm.File {
			for _, fh := range fhs {
				f, err := fh.Open()
				if err != nil {
					return nil, opt, err
				}
				b, err := io.ReadAll(io.LimitReader(f, importMaxBody))
				f.Close()
				if err != nil {
					return nil, opt, err
				}
				files = append(files, importFile{name: fh.Filename, data: b})
			}
		}
		if t := r.FormValue("text"); t != "" {
			files = append(files, importFile{name: "text", data: []byte(t)})
		}
	case strings.Contains(ct, "json"):
		var in struct {
			Text          string          `json:"text"`
			AddressBookID int64           `json:"addressbook_id"`
			Duplicates    string          `json:"duplicates"`
			DryRun        json.RawMessage `json:"dry_run"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, importMaxBody)).Decode(&in); err != nil {
			return nil, opt, badRequest("invalid json")
		}
		files = append(files, importFile{name: "text", data: []byte(in.Text)})
		if in.AddressBookID > 0 {
			opt.book = in.AddressBookID
		}
		if in.Duplicates != "" {
			opt.mode = in.Duplicates
		}
		if len(in.DryRun) > 0 {
			opt.dryRun = truthy(strings.Trim(string(in.DryRun), `"`))
		}
	default:
		b, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, importMaxBody))
		if err != nil {
			return nil, opt, badRequest("the upload is too large (64 MB at most)")
		}
		name := "upload"
		if strings.Contains(ct, "csv") {
			name = "upload.csv"
		}
		files = append(files, importFile{name: name, data: b})
	}
	if opt.mode == "" {
		opt.mode = "merge"
	}
	if opt.mode != "merge" && opt.mode != "skip" && opt.mode != "add" {
		return nil, opt, badRequest("duplicates must be merge, skip or add")
	}
	if len(files) == 0 || (len(files) == 1 && len(bytes.TrimSpace(files[0].data)) == 0) {
		return nil, opt, badRequest("send a .vcf, .csv or .zip file (or paste vCard text)")
	}
	return files, opt, nil
}

// expandZips replaces .zip files by the .vcf and .csv files inside.
func expandZips(files []importFile) ([]importFile, []string) {
	var out []importFile
	var errs []string
	for _, f := range files {
		if !bytes.HasPrefix(f.data, []byte("PK\x03\x04")) {
			out = append(out, f)
			continue
		}
		zr, err := zip.NewReader(bytes.NewReader(f.data), int64(len(f.data)))
		if err != nil {
			errs = append(errs, f.name+": not a readable zip file")
			continue
		}
		for _, zf := range zr.File {
			ext := strings.ToLower(path.Ext(zf.Name))
			if zf.FileInfo().IsDir() || strings.HasPrefix(path.Base(zf.Name), ".") || (ext != ".vcf" && ext != ".vcard" && ext != ".csv") {
				continue
			}
			if len(out) >= importMaxFiles {
				errs = append(errs, f.name+": too many files, the rest was left out")
				break
			}
			rc, err := zf.Open()
			if err != nil {
				errs = append(errs, zf.Name+": "+err.Error())
				continue
			}
			b, err := io.ReadAll(io.LimitReader(rc, importMaxBody))
			rc.Close()
			if err != nil {
				errs = append(errs, zf.Name+": "+err.Error())
				continue
			}
			out = append(out, importFile{name: path.Base(zf.Name), data: b})
		}
	}
	return out, errs
}

// isVCard tells a vCard file from a CSV one (by name, else by content).
func isVCard(f importFile) bool {
	switch strings.ToLower(path.Ext(f.name)) {
	case ".vcf", ".vcard":
		return true
	case ".csv", ".tsv", ".txt":
		return false
	}
	head := f.data
	if len(head) > 4096 {
		head = head[:4096]
	}
	return bytes.Contains(bytes.ToUpper(head), []byte("BEGIN:VCARD"))
}

// readCards turns the files into cards ready to store (each with a UID).
func readCards(files []importFile) ([]importCard, []string) {
	files, errs := expandZips(files)
	var out []importCard
	for _, f := range files {
		label := f.name
		if label == "text" || label == "upload" {
			label = ""
		}
		at := func(s string) string {
			if label == "" {
				return s
			}
			return label + " " + s
		}
		if isVCard(f) {
			cards, err := vcard.Parse(f.data)
			if err != nil {
				errs = append(errs, firstNonEmpty(label, "the text")+": no vCard found")
				continue
			}
			for i, c := range cards {
				c.Upgrade()
				if strings.TrimSpace(c.UID()) == "" {
					c.Set("UID", newUID())
				}
				out = append(out, importCard{card: c, where: at(fmt.Sprintf("card %d", i+1))})
			}
			continue
		}
		rows, rowErrs, err := parseCSV(f.data)
		if err != nil {
			errs = append(errs, firstNonEmpty(label, "the CSV")+": "+err.Error())
			continue
		}
		for _, e := range rowErrs {
			errs = append(errs, at(e))
		}
		for _, row := range rows {
			out = append(out, importCard{card: vcard.New(newUID(), row.ct), where: at(fmt.Sprintf("line %d", row.line))})
		}
	}
	return out, errs
}

// importIndex finds the contact of the target book an imported card matches.
type importIndex struct {
	uid, email, phone, name map[string]int64
}

func newImportIndex() *importIndex {
	return &importIndex{uid: map[string]int64{}, email: map[string]int64{}, phone: map[string]int64{}, name: map[string]int64{}}
}

func (x *importIndex) add(id int64, uid string, ct vcard.Contact) {
	x.uid[uid] = id
	if ct.Kind == "group" {
		return
	}
	for _, f := range ct.Emails {
		if k := strings.ToLower(strings.TrimSpace(f.Value)); k != "" {
			if _, ok := x.email[k]; !ok {
				x.email[k] = id
			}
		}
	}
	for _, f := range ct.Phones {
		if k := vcard.PhoneKey(f.Value); len(k) >= 6 {
			if _, ok := x.phone[k]; !ok {
				x.phone[k] = id
			}
		}
	}
	if k := dupNameKey(ct.DisplayName()); k != "" {
		if _, ok := x.name[k]; !ok {
			x.name[k] = id
		}
	}
}

// match: the same UID, else the same email, phone or (multi-word) name.
func (x *importIndex) match(uid string, ct vcard.Contact) (int64, bool) {
	if id, ok := x.uid[uid]; ok {
		return id, true
	}
	if ct.Kind == "group" {
		return 0, false
	}
	for _, f := range ct.Emails {
		if id, ok := x.email[strings.ToLower(strings.TrimSpace(f.Value))]; ok {
			return id, true
		}
	}
	for _, f := range ct.Phones {
		if k := vcard.PhoneKey(f.Value); len(k) >= 6 {
			if id, ok := x.phone[k]; ok {
				return id, true
			}
		}
	}
	if id, ok := x.name[dupNameKey(ct.DisplayName())]; ok {
		return id, true
	}
	return 0, false
}

// importCards stores (or, on a dry run, only plans) the cards in a book.
func (s *Server) importCards(ctx context.Context, a actor, ab *store.AddressBook, cards []importCard, opt importOptions) (*ImportResult, error) {
	res := &ImportResult{Total: len(cards), Errors: []string{}}
	existing, err := s.store.ContactsInBooks(ctx, []int64{ab.ID}, true)
	if err != nil {
		return nil, err
	}
	idx := newImportIndex()
	for _, e := range existing {
		if c, err := vcard.ParseOne([]byte(e.VCF)); err == nil {
			idx.add(e.ID, e.UID, c.Contact())
		}
	}
	a.source = "import"
	planned := int64(0) // ids for contacts a dry run would create
	note := func(c importCard, err error) {
		res.Failed++
		if len(res.Errors) < 100 {
			res.Errors = append(res.Errors, c.where+": "+errText(err))
		}
	}
	for _, c := range cards {
		ct := c.card.Contact()
		uid := c.card.UID()
		action, matchID := "create", int64(0)
		if opt.mode == "add" {
			if _, taken := idx.uid[uid]; taken {
				uid = newUID()
				c.card.Set("UID", uid)
			}
		} else if id, ok := idx.match(uid, ct); ok {
			matchID = id
			action = opt.mode // merge | skip
		}
		if action == "merge" && matchID > 0 {
			changed, err := s.importMerge(ctx, a, ab, matchID, c.card, opt.dryRun)
			if err != nil {
				note(c, err)
				continue
			}
			if !changed {
				action = "skip"
			}
		}
		switch action {
		case "create":
			id := int64(0)
			if opt.dryRun {
				planned--
				id = planned
			} else {
				e, err := s.createCard(ctx, a, ab, c.card)
				if err != nil {
					note(c, err)
					continue
				}
				id = e.ID
			}
			idx.add(id, uid, ct)
			res.Created++
		case "merge":
			res.Merged++
		default:
			res.Skipped++
		}
		if opt.dryRun && len(res.Preview) < 200 {
			p := importPreview{FN: ct.DisplayName(), Emails: []string{}, Phones: []string{}, Action: action}
			for _, f := range ct.Emails {
				p.Emails = append(p.Emails, f.Value)
			}
			for _, f := range ct.Phones {
				p.Phones = append(p.Phones, f.Value)
			}
			if matchID > 0 {
				m := matchID
				p.MatchID = &m
			}
			res.Preview = append(res.Preview, p)
		}
	}
	if opt.dryRun && res.Preview == nil {
		res.Preview = []importPreview{}
	}
	return res, nil
}

// importMerge folds an imported card into the contact it matched (false when
// that adds nothing). A match made earlier in the same dry run has a
// negative id and counts as a merge.
func (s *Server) importMerge(ctx context.Context, a actor, ab *store.AddressBook, id int64, in *vcard.Card, dryRun bool) (bool, error) {
	if id < 0 {
		return true, nil
	}
	e, err := s.store.GetContact(ctx, id)
	if err != nil {
		return false, err
	}
	card, err := vcard.ParseOne([]byte(e.VCF))
	if err != nil {
		return false, err
	}
	prev := card.Contact()
	prev.Clean()
	merged := vcard.Merge(prev, in.Contact())
	photo := false
	if have, _, _ := card.Photo(); len(have) == 0 {
		if data, mime, _ := in.Photo(); len(data) > 0 {
			card.SetPhoto(data, mime)
			photo = true
		}
	}
	if reflect.DeepEqual(prev, merged) && !photo {
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	card.Apply(prev, merged)
	_, _, err = s.store.SaveContact(ctx, store.ContactWrite{AddressBookID: e.AddressBookID, Href: e.Href, VCF: card.String(), Source: a.source, Actor: a.u.ID, IfMatch: e.ETag})
	if err == nil {
		s.touch()
	}
	return true, err
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a := s.actorOf(r)
	files, opt, err := readImport(r)
	if err != nil {
		s.fail(w, err, "import")
		return
	}
	ab, err := s.targetBook(ctx, a.u, opt.book)
	if err != nil {
		s.fail(w, err, "address book")
		return
	}
	cards, errs := readCards(files)
	if len(cards) == 0 && len(errs) > 0 {
		writeErr(w, http.StatusBadRequest, strings.Join(errs, "; "))
		return
	}
	res, err := s.importCards(ctx, a, ab, cards, opt)
	if err != nil {
		s.fail(w, err, "import")
		return
	}
	res.Errors = append(append([]string{}, errs...), res.Errors...)
	if !opt.dryRun && (res.Created+res.Merged) > 0 {
		abID := ab.ID
		s.store.AddActivity(ctx, a.u.ID, &abID, nil, "imported", plural(res.Created+res.Merged, "contact")+" into "+ab.Name,
			fmt.Sprintf("%d new, %d merged, %d skipped, %d failed", res.Created, res.Merged, res.Skipped, res.Failed))
	}
	writeJSON(w, http.StatusOK, res)
}

// plural is "1 contact", "3 contacts".
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
