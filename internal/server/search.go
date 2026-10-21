package server

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/store"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/vcard"
)

// nameKey is the form names are compared in for ranking: folded, with "dj"
// read as đ ("Đorđe" and "Djordje" both become "dorde").
func nameKey(s string) string {
	return strings.ReplaceAll(vcard.Fold(s), "dj", "d")
}

// matchRank orders search hits: the display name (or given name first)
// starts with the query (0), the family name first or the nickname or
// company does (1), a later word does (2), anything else (3).
func matchRank(e *store.ContactRow, q string) int {
	q = nameKey(q)
	if q == "" {
		return 3
	}
	best := 3
	for i, name := range []string{e.FN, e.Given + " " + e.Family, e.Family + " " + e.Given, e.Nickname, e.Org} {
		n := nameKey(name)
		if n == "" {
			continue
		}
		if strings.HasPrefix(n, q) {
			if i < 2 {
				return 0
			}
			best = min(best, 1)
		}
		for _, w := range strings.Fields(n) {
			if strings.HasPrefix(w, q) {
				best = min(best, 2)
			}
		}
	}
	return best
}

// searchContacts finds contacts in the person's visible books, best matches first.
func (s *Server) searchContacts(ctx context.Context, uid int64, q string, limit int) ([]*store.ContactRow, error) {
	books, err := s.store.VisibleBookIDs(ctx, uid)
	if err != nil {
		return nil, err
	}
	st, _ := s.store.GetSettings(ctx, uid)
	rows, _, err := s.store.FindContacts(ctx, store.Query{BookIDs: books, UserID: uid, Text: q, SortBy: st.SortBy, Limit: 500})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(rows, func(i, j int) bool { return matchRank(rows[i], q) < matchRank(rows[j], q) })
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func clampLimit(n, def, most int) int {
	if n <= 0 {
		return def
	}
	return min(n, most)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{"contacts": []Summary{}})
		return
	}
	rows, err := s.searchContacts(ctx, u.ID, q, clampLimit(queryInt(r, "limit"), 20, 200))
	if err != nil {
		s.fail(w, err, "search")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"contacts": s.summaries(ctx, u.ID, rows)})
}

// handleSuggest is the recipient picker of other apps: one row per email
// (best contacts first), then one per phone number.
func (s *Server) handleSuggest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := clampLimit(queryInt(r, "limit"), 10, 100)
	out := []map[string]any{}
	if q == "" {
		writeJSON(w, http.StatusOK, out)
		return
	}
	rows, err := s.searchContacts(ctx, u.ID, q, 50)
	if err != nil {
		s.fail(w, err, "suggest")
		return
	}
	var phones []map[string]any
	for _, e := range rows {
		sm := s.summary(e, false)
		for _, m := range sm.Emails {
			out = append(out, map[string]any{"id": e.ID, "name": e.FN, "email": m, "phone": "", "photo_url": sm.PhotoURL})
		}
		for _, p := range sm.Phones {
			phones = append(phones, map[string]any{"id": e.ID, "name": e.FN, "email": "", "phone": p, "photo_url": sm.PhotoURL})
		}
	}
	out = append(out, phones...)
	if len(out) > limit {
		out = out[:limit]
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleLookup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	books, err := s.store.ReadableBookIDs(ctx, u.ID)
	if err != nil {
		s.fail(w, err, "lookup")
		return
	}
	var rows []*store.ContactRow
	switch email, phone := r.URL.Query().Get("email"), r.URL.Query().Get("phone"); {
	case email != "":
		rows, err = s.store.LookupEmail(ctx, books, email)
	case phone != "":
		rows, err = s.store.LookupPhone(ctx, books, phone)
	default:
		writeErr(w, http.StatusBadRequest, "look up an email= or a phone=")
		return
	}
	if err != nil {
		s.fail(w, err, "lookup")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"contacts": s.summaries(ctx, u.ID, rows)})
}

func (s *Server) handleCategories(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	books, err := s.store.VisibleBookIDs(ctx, u.ID)
	if err != nil {
		s.fail(w, err, "labels")
		return
	}
	counts, err := s.store.Categories(ctx, books)
	if err != nil {
		s.fail(w, err, "labels")
		return
	}
	var names []string
	for k := range counts {
		names = append(names, k)
	}
	store.SortStrings(names)
	labels := []map[string]any{}
	for _, n := range names {
		labels = append(labels, map[string]any{"name": n, "count": counts[n]})
	}
	groups := []map[string]any{}
	rows, _, err := s.store.FindContacts(ctx, store.Query{BookIDs: books, Kind: "group", Limit: 5000})
	if err != nil {
		s.fail(w, err, "groups")
		return
	}
	for _, g := range rows {
		groups = append(groups, map[string]any{"uid": g.UID, "name": g.FN, "count": len(g.Members), "addressbook_id": g.AddressBookID})
	}
	writeJSON(w, http.StatusOK, map[string]any{"labels": labels, "groups": groups})
}

// relabel changes a label on every contact the person may write.
func (s *Server) relabel(ctx context.Context, a actor, from string, edit func([]string) []string) (int, error) {
	rows, _, err := s.store.FindContacts(ctx, store.Query{BookIDs: s.writableBookIDs(ctx, a.u.ID), Category: from, Limit: 5000})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range rows {
		_, ab, err := s.writableContact(ctx, a, e.ID)
		if err != nil {
			continue
		}
		_, changed, err := s.editCard(ctx, a, e, ab, e.ETag, func(ct *vcard.Contact) { ct.Categories = edit(ct.Categories) })
		if err != nil {
			s.log.Warn("relabel", "contact", e.ID, "err", err)
			continue
		}
		if changed {
			n++
		}
	}
	return n, nil
}

func (s *Server) handleRenameCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a := s.actorOf(r)
	var in struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	from, to := strings.TrimSpace(in.From), strings.TrimSpace(in.To)
	if from == "" || to == "" || strings.HasPrefix(from, "group:") || from == "-" {
		writeErr(w, http.StatusBadRequest, "rename which label to what?")
		return
	}
	n, err := s.relabel(ctx, a, from, func(cats []string) []string {
		out := make([]string, 0, len(cats))
		for _, c := range cats {
			if strings.EqualFold(c, from) {
				c = to
			}
			out = append(out, c)
		}
		return editLabels(out, nil, nil)
	})
	if err != nil {
		s.fail(w, err, "rename label")
		return
	}
	s.store.AddActivity(ctx, a.u.ID, nil, nil, "label_renamed", from+" → "+to, "")
	writeJSON(w, http.StatusOK, map[string]any{"changed": n})
}

func (s *Server) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a := s.actorOf(r)
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeErr(w, http.StatusBadRequest, "remove which label?")
		return
	}
	name := strings.TrimSpace(in.Name)
	n, err := s.relabel(ctx, a, name, func(cats []string) []string { return editLabels(cats, nil, []string{name}) })
	if err != nil {
		s.fail(w, err, "remove label")
		return
	}
	s.store.AddActivity(ctx, a.u.ID, nil, nil, "label_deleted", name, "")
	writeJSON(w, http.StatusOK, map[string]any{"changed": n})
}
