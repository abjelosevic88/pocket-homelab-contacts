package server

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// Birthday reminders: every minute, each person whose birthday_time has come
// (in their zone) gets one message about today's birthdays (and
// anniversaries when they want those) and the ones birthday_ahead days away.
// reminder_log makes sure each goes out once; a time more than an hour past
// (the server was down) is skipped rather than sent late. Pocket Core hears
// contacts.birthday once per contact and day.

func (s *Server) reminderLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		now := time.Now()
		for _, uid := range s.store.UserIDs(ctx) {
			if title, body, link := s.remindPerson(ctx, uid, now); title != "" {
				s.notifyUser(ctx, uid, "birthdays", title, body, link)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// clockOf reads "HH:MM" (08:00 when unreadable).
func clockOf(v string) (int, int) {
	h, m, ok := strings.Cut(strings.TrimSpace(v), ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 8, 0
	}
	return hh, mm
}

// remindPerson works out the message one person is due at now ("" when
// none) and records it as sent; it also queues the Core events.
func (s *Server) remindPerson(ctx context.Context, uid int64, now time.Time) (title, body, link string) {
	st, err := s.store.GetSettings(ctx, uid)
	if err != nil {
		return "", "", ""
	}
	loc := s.loc(ctx, uid)
	local := now.In(loc)
	hh, mm := clockOf(st.BirthdayTime)
	at := time.Date(local.Year(), local.Month(), local.Day(), hh, mm, 0, 0, loc)
	if local.Before(at) {
		return "", "", ""
	}
	ahead := max(st.BirthdayAhead, 0)
	list, err := s.upcoming(ctx, uid, local, ahead+1)
	if err != nil {
		return "", "", ""
	}
	if s.core.Configured() {
		if u, err := s.store.GetUser(ctx, uid); err == nil {
			for _, it := range list {
				if it.Days != 0 || s.store.ReminderFired(ctx, uid, it.row.ID, it.Date, "core-"+it.Kind) {
					continue
				}
				_ = core.QueueEvent(ctx, s.store.DB, "contacts.birthday", fmt.Sprintf("contact:%d:%s:%s", it.row.ID, it.Kind, it.Date), map[string]any{
					"id": it.row.ID, "fn": it.row.FN, "kind": it.Kind, "age": it.Age, "user_email": u.Email,
				})
			}
		}
	}
	if !st.BirthdayNotify || local.Sub(at) > time.Hour {
		return "", "", ""
	}
	var today, later []Upcoming
	for _, it := range list {
		if it.Kind == "anniversary" && !st.AnniversaryNote {
			continue
		}
		kind := it.Kind
		switch {
		case it.Days == 0:
		case ahead > 0 && it.Days == ahead:
			kind += "-ahead"
		default:
			continue
		}
		if s.store.ReminderFired(ctx, uid, it.row.ID, it.Date, kind) {
			continue
		}
		if it.Days == 0 {
			today = append(today, it)
		} else {
			later = append(later, it)
		}
	}
	if len(today)+len(later) == 0 {
		return "", "", ""
	}
	var lines []string
	for _, it := range today {
		lines = append(lines, reminderLine(it))
	}
	if len(later) > 0 {
		if len(today) > 0 {
			lines = append(lines, "")
		}
		d, _ := time.Parse("2006-01-02", later[0].Date)
		lines = append(lines, fmt.Sprintf("In %d days (%s):", ahead, d.Format("Mon 2 Jan")))
		for _, it := range later {
			lines = append(lines, reminderLine(it))
		}
	}
	all := append(append([]Upcoming(nil), today...), later...)
	link = s.cfg.BaseURL + "/birthdays"
	if len(all) == 1 {
		link = s.cfg.BaseURL + "/contacts/" + strconv.FormatInt(all[0].row.ID, 10)
	}
	icon := "🎂 "
	if all[0].Kind == "anniversary" && len(all) == 1 {
		icon = "💍 "
	}
	switch {
	case len(today) == 1 && len(later) == 0:
		return icon + reminderLine(today[0]) + " today", local.Format("Monday, 2 January"), link
	case len(today) > 0:
		title = icon + "Birthdays today"
	default:
		title = icon + fmt.Sprintf("Birthdays in %d days", ahead)
	}
	return title, strings.Join(lines, "\n"), link
}

// reminderLine says one birthday or anniversary in plain words.
func reminderLine(it Upcoming) string {
	name := it.Contact.FN
	if it.Kind == "anniversary" {
		if it.Age != nil {
			return fmt.Sprintf("%s: %d years together", name, *it.Age)
		}
		return name + "'s anniversary"
	}
	if it.Age != nil {
		return fmt.Sprintf("%s turns %d", name, *it.Age)
	}
	return name + "'s birthday"
}
