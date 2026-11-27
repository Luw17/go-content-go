package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Luw17/go-content-go/internal/auth"
)

func (h *handler) updateUserRole(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	err = auth.UpdateUserRole(r.Context(), h.db, id, r.FormValue("role"))
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/users?status=updated", http.StatusSeeOther)
	case errors.Is(err, auth.ErrLastAdmin):
		http.Redirect(w, r, "/admin/users?status=last_admin", http.StatusSeeOther)
	case errors.Is(err, auth.ErrInvalidRole):
		http.Redirect(w, r, "/admin/users?status=invalid", http.StatusSeeOther)
	case errors.Is(err, auth.ErrUserNotFound):
		http.NotFound(w, r)
	default:
		slog.Error("update user role", "user_id", id, "error", err)
		http.Error(w, "failed to update user role", http.StatusInternalServerError)
	}
}

func (h *handler) updateUserPassword(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	err = auth.UpdateUserPassword(r.Context(), h.db, id, r.FormValue("password"))
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/users?status=password_updated", http.StatusSeeOther)
	case errors.Is(err, auth.ErrInvalidPassword):
		http.Redirect(w, r, "/admin/users?status=invalid", http.StatusSeeOther)
	case errors.Is(err, auth.ErrUserNotFound):
		http.NotFound(w, r)
	default:
		slog.Error("update user password", "user_id", id, "error", err)
		http.Error(w, "failed to update password", http.StatusInternalServerError)
	}
}

func (h *handler) toggleUserActive(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	active := r.FormValue("active") == "1"
	err = auth.SetUserActive(r.Context(), h.db, id, active)
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/users?status=updated", http.StatusSeeOther)
	case errors.Is(err, auth.ErrLastAdmin):
		http.Redirect(w, r, "/admin/users?status=last_admin", http.StatusSeeOther)
	case errors.Is(err, auth.ErrUserNotFound):
		http.NotFound(w, r)
	default:
		slog.Error("toggle user active", "user_id", id, "error", err)
		http.Error(w, "failed to update user status", http.StatusInternalServerError)
	}
}

func (h *handler) revokeUserSessions(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	if err := auth.RevokeUserSessions(r.Context(), h.db, id); err != nil {
		slog.Error("revoke user sessions", "user_id", id, "error", err)
		http.Error(w, "failed to revoke sessions", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/users?status=sessions_revoked", http.StatusSeeOther)
}

func (h *handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	err = auth.DeleteUser(r.Context(), h.db, id)
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/users?status=deleted", http.StatusSeeOther)
	case errors.Is(err, auth.ErrLastAdmin):
		http.Redirect(w, r, "/admin/users?status=last_admin", http.StatusSeeOther)
	case errors.Is(err, auth.ErrUserNotFound):
		http.NotFound(w, r)
	default:
		slog.Error("delete user", "user_id", id, "error", err)
		http.Error(w, "failed to delete user", http.StatusInternalServerError)
	}
}
