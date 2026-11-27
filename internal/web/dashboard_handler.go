package web

import (
	"context"
	"os"
	"sort"
	"strings"

	"github.com/Luw17/go-content-go/internal/auth"
	"github.com/Luw17/go-content-go/internal/siteconfig"
)

type dashboardKPIs struct {
	PublishedPosts  int
	DraftPosts      int
	ScheduledPosts  int
	PublishedPages  int
	MediaCount      int
	FormSubmissions int
}

type healthCheckItem struct {
	Label   string
	OK      bool
	Message string
	Action  string
}

func (h *handler) loadDashboardOverview(ctx context.Context, user auth.User) (dashboardKPIs, []healthCheckItem) {
	var kpis dashboardKPIs
	_ = h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM posts WHERE content_type='post' AND status='published' AND deleted_at IS NULL`).Scan(&kpis.PublishedPosts)
	_ = h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM posts WHERE content_type='post' AND status='draft' AND deleted_at IS NULL`).Scan(&kpis.DraftPosts)
	_ = h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM posts WHERE status='draft' AND scheduled_at != '' AND deleted_at IS NULL`).Scan(&kpis.ScheduledPosts)
	_ = h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM posts WHERE content_type='page' AND status='published' AND deleted_at IS NULL`).Scan(&kpis.PublishedPages)
	_ = h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media`).Scan(&kpis.MediaCount)
	_ = h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM form_submissions`).Scan(&kpis.FormSubmissions)

	settings, _ := siteconfig.Load(ctx, h.db)
	var checks []healthCheckItem

	settingsAction := ""
	if user.Role == "admin" {
		settingsAction = "/admin/settings"
	}

	if settings.SiteURL != "" {
		checks = append(checks, healthCheckItem{Label: "URL Canônica do Site", OK: true, Message: settings.SiteURL})
	} else {
		checks = append(checks, healthCheckItem{Label: "URL Canônica do Site", OK: false, Message: "Preencha a URL do site para canonicals e sitemap consistentes.", Action: settingsAction})
	}

	if settings.SEORobotsIndexing == siteconfig.RobotsNoindex {
		checks = append(checks, healthCheckItem{Label: "Indexação (Google / Robots)", OK: false, Message: "O site está marcado como noindex (oculto de buscadores).", Action: settingsAction})
	} else {
		checks = append(checks, healthCheckItem{Label: "Indexação (Google / Robots)", OK: true, Message: "Indexação ativa (index, follow) com /sitemap.xml e /robots.txt."})
	}

	if user.TOTPEnabled {
		checks = append(checks, healthCheckItem{Label: "Autenticação 2FA", OK: true, Message: "2FA ativo na sua conta."})
	} else {
		checks = append(checks, healthCheckItem{Label: "Autenticação 2FA", OK: false, Message: "Ative 2FA para proteger o acesso administrativo.", Action: "/admin/security"})
	}

	if latest, ok := latestBackupName(h.backupDir); ok {
		checks = append(checks, healthCheckItem{Label: "Último Backup Local", OK: true, Message: latest})
	} else {
		checks = append(checks, healthCheckItem{Label: "Último Backup Local", OK: false, Message: "Nenhum backup local encontrado em backups/.", Action: settingsAction})
	}

	return kpis, checks
}

func latestBackupName(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "go-content-go-") && strings.HasSuffix(e.Name(), ".db") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return "", false
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	return names[0], true
}
