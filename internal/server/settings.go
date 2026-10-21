package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-contacts/internal/store"
)

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	st, err := s.store.GetSettings(ctx, u.ID)
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	writeJSON(w, http.StatusOK, s.publicSettings(ctx, u, st))
}

func (s *Server) publicSettings(ctx context.Context, u *store.User, st store.Settings) map[string]any {
	return map[string]any{
		"settings":         st,
		"box_tz":           s.cfg.Timezone,
		"notify_has_token": st.NotifyToken != "",
		"notify_box_route": s.cfg.NotifyURL != "" && s.isOwner(ctx, u.ID),
		"core":             s.coreStatus(),
	}
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	st, err := s.store.GetSettings(ctx, u.ID)
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	var in struct {
		Title                *string `json:"title"`
		SortBy               *string `json:"sort_by"`
		DisplayOrder         *string `json:"display_order"`
		DefaultAddressBookID *int64  `json:"default_addressbook_id"`
		Density              *string `json:"density"`
		PhoneRegion          *string `json:"phone_region"`
		NotifyKind           *string `json:"notify_kind"`
		NotifyURL            *string `json:"notify_url"`
		NotifyToken          *string `json:"notify_token"`
		BirthdayNotify       *bool   `json:"birthday_notify"`
		BirthdayAhead        *int    `json:"birthday_ahead"`
		BirthdayTime         *string `json:"birthday_time"`
		AnniversaryNote      *bool   `json:"anniversary_note"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Title != nil && u.Role == "admin" && strings.TrimSpace(*in.Title) != "" {
		box, _ := s.store.GetSettings(ctx, 0)
		box.Title = strings.TrimSpace(*in.Title)
		_ = s.store.SaveBoxSettings(ctx, box)
		st.Title = box.Title
	}
	if in.SortBy != nil && (*in.SortBy == "given" || *in.SortBy == "family") {
		st.SortBy = *in.SortBy
	}
	if in.DisplayOrder != nil && (*in.DisplayOrder == "given_first" || *in.DisplayOrder == "family_first") {
		st.DisplayOrder = *in.DisplayOrder
	}
	if in.DefaultAddressBookID != nil {
		if *in.DefaultAddressBookID != 0 {
			role, _, err := s.store.BookRole(ctx, u.ID, *in.DefaultAddressBookID)
			if err != nil || !store.RoleAtLeast(role, store.RoleWrite) {
				writeErr(w, http.StatusBadRequest, "new contacts can only go to an address book you may write to")
				return
			}
		}
		st.DefaultAddressBookID = *in.DefaultAddressBookID
	}
	if in.Density != nil && (*in.Density == "comfortable" || *in.Density == "compact") {
		st.Density = *in.Density
	}
	if in.PhoneRegion != nil {
		st.PhoneRegion = strings.ToUpper(strings.TrimSpace(*in.PhoneRegion))
	}
	if in.NotifyKind != nil {
		switch *in.NotifyKind {
		case "apprise", "ntfy", "webhook":
			st.NotifyKind = *in.NotifyKind
		}
	}
	if in.NotifyURL != nil {
		st.NotifyURL = strings.TrimSpace(*in.NotifyURL)
	}
	if in.BirthdayNotify != nil {
		st.BirthdayNotify = *in.BirthdayNotify
	}
	if in.AnniversaryNote != nil {
		st.AnniversaryNote = *in.AnniversaryNote
	}
	if in.BirthdayAhead != nil && *in.BirthdayAhead >= 0 && *in.BirthdayAhead <= 30 {
		st.BirthdayAhead = *in.BirthdayAhead
	}
	if in.BirthdayTime != nil {
		v := strings.TrimSpace(*in.BirthdayTime)
		if _, err := time.Parse("15:04", v); err != nil {
			writeErr(w, http.StatusBadRequest, "the reminder time looks like 08:00")
			return
		}
		st.BirthdayTime = v
	}
	if err := s.store.SaveSettings(ctx, u.ID, st); err != nil {
		s.fail(w, err, "save settings")
		return
	}
	if in.NotifyToken != nil {
		_ = s.store.SetUserSetting(ctx, u.ID, "notify_token", strings.TrimSpace(*in.NotifyToken))
	}
	s.touch()
	st, _ = s.store.GetSettings(ctx, u.ID)
	writeJSON(w, http.StatusOK, s.publicSettings(ctx, u, st))
}

// notifyRoute is where a person's notifications go: their own route, else
// (for the owner) the box route from the environment.
func (s *Server) notifyRoute(ctx context.Context, uid int64, st store.Settings) (kind, url, token string) {
	if st.NotifyURL != "" {
		return st.NotifyKind, st.NotifyURL, st.NotifyToken
	}
	if s.cfg.NotifyURL != "" && s.isOwner(ctx, uid) {
		return s.cfg.NotifyKind, s.cfg.NotifyURL, ""
	}
	return "", "", ""
}

// notifyUser sends one message to a person ("birthdays", "sync").
func (s *Server) notifyUser(ctx context.Context, uid int64, what, title, body, link string) {
	st, err := s.store.GetSettings(ctx, uid)
	if err != nil {
		return
	}
	if what == "birthdays" && !st.BirthdayNotify {
		return
	}
	kind, url, token := s.notifyRoute(ctx, uid, st)
	if url == "" {
		return
	}
	go func() {
		c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := notify.Send(c, kind, url, token, title, body, link); err != nil {
			s.log.Warn("notification failed", "user", uid, "what", what, "err", err)
		}
	}()
}

func (s *Server) handleTestNotify(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	st, _ := s.store.GetSettings(ctx, u.ID)
	kind, url, token := s.notifyRoute(ctx, u.ID, st)
	if url == "" {
		writeErr(w, http.StatusBadRequest, "set a notification address first")
		return
	}
	if err := notify.Send(ctx, kind, url, token, "Pocket Contacts", "Notifications work. Birthday reminders will look like this.", s.cfg.BaseURL); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	info := map[string]any{
		"version": Version, "go": runtime.Version(), "uptime": time.Since(s.started).Round(time.Second).String(),
		"counts": s.store.UserCounts(ctx, u.ID), "core": s.coreStatus(), "auth_mode": s.cfg.AuthMode,
		"base_url": s.cfg.BaseURL, "timezone": s.cfg.Timezone,
	}
	if u.Role == "admin" {
		if fi, err := os.Stat(s.cfg.DBPath); err == nil {
			info["db_size"] = fi.Size()
		}
		info["users"], _ = s.store.CountUsers(ctx)
	}
	writeJSON(w, http.StatusOK, info)
}

// handleBackup streams a consistent copy of the whole database; only an
// admin, and only while they are the only person on the box.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	if u.Role != "admin" {
		writeErr(w, http.StatusForbidden, "only an admin can download the database")
		return
	}
	if n, _ := s.store.CountUsers(ctx); n > 1 {
		writeErr(w, http.StatusForbidden, "the database holds other people's contacts; back it up on the server (pocket-contacts backup)")
		return
	}
	tmp := fmt.Sprintf("%s/backups/download-%d.db", s.cfg.DataDir, time.Now().UnixNano())
	if err := s.store.Backup(ctx, tmp); err != nil {
		s.fail(w, err, "backup")
		return
	}
	defer os.Remove(tmp)
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-contacts-`+time.Now().Format("20060102")+`.db"`)
	http.ServeFile(w, r, tmp)
}
