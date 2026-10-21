package server

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/store"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/vcard"
)

// Duplicates (design.md rule 5) are found, never merged on their own: the
// same name of two or more words (folded, any order), the same email or the
// same phone number (last 8 digits). A one-word name ("Zoran") only matches
// through a shared email or phone. Pairs a person said are different people
// are left apart. Only books the person may write are looked at.

// DupGroup is one set of contacts that look like the same person.
type DupGroup struct {
	Key      string    `json:"key"`
	Reason   string    `json:"reason"`
	Contacts []Summary `json:"contacts"`
}

var dupReasons = []string{"same email", "same phone", "same name"}

// dupNameKey is the name a duplicate is matched by ("" for one word).
func dupNameKey(fn string) string {
	words := strings.Fields(vcard.Fold(fn))
	if len(words) < 2 {
		return ""
	}
	sort.Strings(words)
	return strings.Join(words, " ")
}

// dupKeys are the keys a contact is matched by, with the reason of each.
func dupKeys(e *store.ContactRow) map[string]int {
	keys := map[string]int{}
	for _, m := range strings.Fields(e.Emails) {
		keys["e:"+m] = 0
	}
	for _, p := range strings.Fields(e.Phones) {
		if k := vcard.PhoneKey(p); len(k) >= 6 {
			keys["p:"+k] = 1
		}
	}
	if n := dupNameKey(e.FN); n != "" {
		keys["n:"+n] = 2
	}
	return keys
}

func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

// richness ranks the contact to keep first: the one with the most in it.
func richness(e *store.ContactRow) int {
	n := len(strings.Fields(e.Emails)) + len(strings.Fields(e.Phones)) + len(e.Categories)
	for _, v := range []string{e.Bday, e.Anniversary, e.Org, e.Title, e.Nickname} {
		if v != "" {
			n++
		}
	}
	if e.HasPhoto {
		n += 2
	}
	return n
}

// findDuplicates groups the person's writable contacts that look alike (union-find).
func (s *Server) findDuplicates(ctx context.Context, uid int64) ([]DupGroup, error) {
	rows, err := s.store.ContactsInBooks(ctx, s.writableBookIDs(ctx, uid), false)
	if err != nil {
		return nil, err
	}
	ignored := s.store.NotDuplicates(ctx, uid)
	parent := make([]int, len(rows))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	type edge struct{ a, b, reason int }
	var edges []edge
	buckets := map[string][]int{}
	reasonOf := map[string]int{}
	for i, e := range rows {
		for k, reason := range dupKeys(e) {
			buckets[k] = append(buckets[k], i)
			reasonOf[k] = reason
		}
	}
	for k, members := range buckets {
		if len(members) < 2 || len(members) > 50 {
			continue // a number fifty people share is a switchboard, not a person
		}
		for x := 0; x < len(members); x++ {
			for y := x + 1; y < len(members); y++ {
				a, b := members[x], members[y]
				if ignored[pairKey(rows[a].UID, rows[b].UID)] {
					continue
				}
				edges = append(edges, edge{a, b, reasonOf[k]})
				parent[find(a)] = find(b)
			}
		}
	}
	groups := map[int][]int{}
	best := map[int]int{}
	for i := range rows {
		groups[find(i)] = append(groups[find(i)], i)
	}
	for _, e := range edges {
		root := find(e.a)
		if r, ok := best[root]; !ok || e.reason < r {
			best[root] = e.reason
		}
	}
	starred := s.store.Starred(ctx, uid)
	out := []DupGroup{}
	for root, members := range groups {
		if len(members) < 2 {
			continue
		}
		sort.SliceStable(members, func(i, j int) bool {
			ri, rj := richness(rows[members[i]]), richness(rows[members[j]])
			if ri != rj {
				return ri > rj
			}
			return rows[members[i]].ID < rows[members[j]].ID
		})
		g := DupGroup{Reason: dupReasons[best[root]]}
		var ids []string
		for _, m := range members {
			g.Contacts = append(g.Contacts, s.summary(rows[m], starred[rows[m].ID]))
			ids = append(ids, strconv.FormatInt(rows[m].ID, 10))
		}
		g.Key = strings.Join(ids, "-")
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return nameKey(out[i].Contacts[0].FN) < nameKey(out[j].Contacts[0].FN) })
	return out, nil
}

func (s *Server) handleDuplicates(w http.ResponseWriter, r *http.Request) {
	groups, err := s.findDuplicates(r.Context(), me(r))
	if err != nil {
		s.fail(w, err, "duplicates")
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

// mergeContacts folds the others into the primary contact and moves them to
// Recently deleted.
func (s *Server) mergeContacts(ctx context.Context, a actor, primaryID int64, ids []int64) (*store.ContactRow, error) {
	p, pab, err := s.writableContact(ctx, a, primaryID)
	if err != nil {
		return nil, err
	}
	type other struct {
		e  *store.ContactRow
		ab *store.AddressBook
	}
	var others []other
	seen := map[int64]bool{primaryID: true}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		e, ab, err := s.writableContact(ctx, a, id)
		if err != nil {
			return nil, err
		}
		others = append(others, other{e, ab})
	}
	if len(others) == 0 {
		return nil, badRequest("pick at least two contacts to merge")
	}
	card, err := vcard.ParseOne([]byte(p.VCF))
	if err != nil {
		return nil, err
	}
	prev := card.Contact()
	prev.Clean()
	merged := prev
	photo, _, _ := card.Photo()
	var names []string
	for _, o := range others {
		oc, err := vcard.ParseOne([]byte(o.e.VCF))
		if err != nil {
			return nil, err
		}
		merged = vcard.Merge(merged, oc.Contact())
		if len(photo) == 0 {
			if data, mime, _ := oc.Photo(); len(data) > 0 {
				photo = data
				card.SetPhoto(data, mime)
			}
		}
		names = append(names, o.e.FN)
	}
	card.Apply(prev, merged)
	saved, err := s.saveCard(ctx, a, p, pab, card, p.ETag)
	if err != nil {
		return nil, err
	}
	for _, o := range others {
		stars := s.store.StarredBy(ctx, o.e.ID)
		if err := s.store.DeleteContact(ctx, o.e, store.DeleteContactOpts{Actor: a.u.ID, KeepCopy: true},
			s.contactHook(ctx, "deleted", o.ab, o.e.ID, o.e.UID, o.e.FN, a.source)); err != nil {
			return nil, err
		}
		for _, uid := range stars {
			_ = s.store.SetStarred(ctx, uid, saved.ID, true)
		}
	}
	s.touch()
	s.logContact(ctx, a, pab, saved.ID, "merged", saved.FN, "with "+strings.Join(names, ", "))
	return saved, nil
}

func (s *Server) handleMerge(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs       []int64 `json:"ids"`
		PrimaryID int64   `json:"primary_id"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(in.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, "pick at least two contacts to merge")
		return
	}
	primary := in.PrimaryID
	if primary == 0 {
		primary = in.IDs[0]
	}
	e, err := s.mergeContacts(r.Context(), s.actorOf(r), primary, in.IDs)
	if err != nil {
		s.fail(w, err, "merge")
		return
	}
	s.respondContact(w, r, http.StatusOK, e.ID)
}

func (s *Server) handleIgnoreDuplicates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := decode(r, &in); err != nil || len(in.IDs) < 2 {
		writeErr(w, http.StatusBadRequest, "pick at least two contacts")
		return
	}
	var uids []string
	for _, id := range in.IDs {
		e, _, _, err := s.loadContact(ctx, me(r), id)
		if err != nil {
			s.fail(w, err, "contact")
			return
		}
		uids = append(uids, e.UID)
	}
	for i := range uids {
		for j := i + 1; j < len(uids); j++ {
			if err := s.store.NotDuplicate(ctx, me(r), uids[i], uids[j]); err != nil {
				s.fail(w, err, "not duplicates")
				return
			}
		}
	}
	s.touch()
	w.WriteHeader(http.StatusNoContent)
}
