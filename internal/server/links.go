package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-contacts/internal/store"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// Pocket Core app links (docs/links.md in pocket-homelab-core): another
// Pocket app may act here for a person with a pcx_ token, only within these
// scopes. Every route not listed in linkRoutes stays closed to it.

var linkOffers = []core.Offer{
	{Scope: "contacts:read", Name: "See your contacts", Description: "Search and read your contacts, their photos and birthdays (Pocket Calendar's birthdays and guest suggestions, Pocket Notes' \"link to a person\", Pocket Day's \"birthdays today\")."},
	{Scope: "contacts:write", Name: "Add contacts", Description: "Create contacts, and change or delete only the ones that app created itself."},
}

// serviceUses are the third-party services reached through Core for a person.
var serviceUses = []core.ServiceUse{
	{Type: "nextcloud", Reason: "Bring your Nextcloud contacts here and keep them in sync both ways"},
}

type linkRoute struct {
	methods string
	path    *regexp.Regexp
	scope   string
}

var linkRoutes = []linkRoute{
	{"GET", regexp.MustCompile(`^/api/v1/auth/me$`), ""},
	{"GET", regexp.MustCompile(`^/api/v1/(contacts|addressbooks|birthdays|search|suggest|categories|changes|lookup)$`), "contacts:read"},
	{"GET", regexp.MustCompile(`^/api/v1/contacts/[0-9]+(/vcf|/photo)?$`), "contacts:read"},
	{"POST", regexp.MustCompile(`^/api/v1/contacts$`), "contacts:write"},
	{"PUT PATCH DELETE", regexp.MustCompile(`^/api/v1/contacts/[0-9]+$`), "contacts:write"},
}

var errLinkScope = errors.New("outside the link's scope")

func linkAllows(scopes []string, r *http.Request) bool {
	for _, lr := range linkRoutes {
		if !strings.Contains(" "+lr.methods+" ", " "+r.Method+" ") || !lr.path.MatchString(r.URL.Path) {
			continue
		}
		if lr.scope == "" {
			return true
		}
		for _, sc := range scopes {
			if sc == lr.scope {
				return true
			}
		}
	}
	return false
}

// linkUser validates a pcx_ token and maps it to the person it acts for.
func (s *Server) linkUser(ctx context.Context, tok string, r *http.Request) (*store.User, error) {
	in, err := s.core.Introspect(ctx, tok)
	if err != nil {
		return nil, err
	}
	if !in.Active || !in.Delegated() || in.User == nil || in.User.Email == "" {
		return nil, http.ErrNoCookie
	}
	if !linkAllows(strings.Fields(in.Scope), r) {
		return nil, errLinkScope
	}
	s.linkApps.Store(tok, in.App)
	return s.coreIdentityUser(ctx, in)
}

// linkApp is the Pocket app acting through a link token ("" otherwise).
func (s *Server) linkApp(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return ""
	}
	tok := strings.TrimSpace(h[7:])
	if !core.IsLinkToken(tok) {
		return ""
	}
	if v, ok := s.linkApps.Load(tok); ok {
		return v.(string)
	}
	return "link"
}

// source names who wrote a change: the acting app, the web app or the API.
func (s *Server) source(r *http.Request) string {
	if app := s.linkApp(r); app != "" {
		return app
	}
	if r.Header.Get("X-Pocket-Source") == "web" {
		return "web"
	}
	return "api"
}

// reportModes tells Core how each person reaches the Pocket apps used from
// a contact and Nextcloud (Core, Direct or off).
func (s *Server) reportModes(ctx context.Context) {
	if !s.core.Configured() {
		return
	}
	var modes []core.ConnMode
	for _, uid := range s.store.UserIDs(ctx) {
		email := s.userEmail(ctx, uid)
		if email == "" {
			continue
		}
		st, err := s.store.GetSettings(ctx, uid)
		if err != nil {
			continue
		}
		for _, app := range pocketApps {
			modes = append(modes, core.ConnMode{User: email, Target: app.Slug, Mode: s.appMode(st, app)})
		}
		mode := "off"
		if accs, err := s.store.ListAccounts(ctx, uid); err == nil {
			for _, a := range accs {
				if a.Kind == "nextcloud" && a.Enabled && a.Mode != "off" {
					mode = a.Via
				}
			}
		}
		modes = append(modes, core.ConnMode{User: email, Target: "service:nextcloud", Mode: mode})
	}
	if err := s.core.ReportModes(ctx, modes); err != nil {
		s.log.Debug("pocket core: report modes", "err", err)
	}
}

func (s *Server) userEmail(ctx context.Context, uid int64) string {
	if u, err := s.store.GetUser(ctx, uid); err == nil {
		return u.Email
	}
	return ""
}
