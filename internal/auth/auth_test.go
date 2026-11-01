package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/Luw17/go-content-go/internal/database"
)

func TestInitialAdminAuthenticationAndSessions(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}

	available, err := SetupAvailable(ctx, db)
	if err != nil || !available {
		t.Fatalf("SetupAvailable() = %v, %v; want true, nil", available, err)
	}
	user, err := CreateInitialAdmin(ctx, db, " Admin_User ", "a-secure-password")
	if err != nil {
		t.Fatal(err)
	}
	if user.Username != "admin_user" || user.Role != "admin" {
		t.Fatalf("created user = %+v, want normalized admin user", user)
	}
	if _, err := CreateInitialAdmin(ctx, db, "second", "another-secure-password"); !errors.Is(err, ErrSetupComplete) {
		t.Fatalf("second initial administrator error = %v, want ErrSetupComplete", err)
	}

	authenticated, err := Authenticate(ctx, db, "ADMIN_USER", "a-secure-password")
	if err != nil || authenticated.ID != user.ID {
		t.Fatalf("Authenticate() = %+v, %v; want user %d", authenticated, err, user.ID)
	}
	if _, err := Authenticate(ctx, db, "admin_user", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("invalid password error = %v, want ErrInvalidCredentials", err)
	}
	if _, err := Authenticate(ctx, db, "missing", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("missing username error = %v, want ErrInvalidCredentials", err)
	}

	token, expiresAt, err := CreateSession(ctx, db, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if expiresAt.IsZero() || len(token) < 40 {
		t.Fatalf("session token/expiry not initialized: token length %d, expiry %v", len(token), expiresAt)
	}
	sessionUser, valid, err := SessionUser(ctx, db, token)
	if err != nil || !valid || sessionUser.ID != user.ID {
		t.Fatalf("SessionUser() = %+v, %v, %v; want valid user %d", sessionUser, valid, err, user.ID)
	}

	if err := DeleteSession(ctx, db, token); err != nil {
		t.Fatal(err)
	}
	if _, valid, err := SessionUser(ctx, db, token); err != nil || valid {
		t.Fatalf("session remained valid after logout: valid=%v err=%v", valid, err)
	}
}

func TestCreateInitialAdminValidatesInput(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}

	if _, err := CreateInitialAdmin(ctx, db, "invalid username", "a-secure-password"); !errors.Is(err, ErrInvalidUsername) {
		t.Errorf("invalid username error = %v, want ErrInvalidUsername", err)
	}
	if _, err := CreateInitialAdmin(ctx, db, "valid_user", "short"); !errors.Is(err, ErrInvalidPassword) {
		t.Errorf("short password error = %v, want ErrInvalidPassword", err)
	}
}

func TestRolesCanAuthenticateAndAreStoredInSessions(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateInitialAdmin(ctx, db, "role_admin", "a-secure-password"); err != nil {
		t.Fatal(err)
	}
	author, err := CreateUser(ctx, db, "writer", "another-secure-password", "author")
	if err != nil {
		t.Fatal(err)
	}
	authenticated, err := Authenticate(ctx, db, "writer", "another-secure-password")
	if err != nil || authenticated.ID != author.ID || authenticated.Role != "author" {
		t.Fatalf("Authenticate() = %+v, %v; want author %d", authenticated, err, author.ID)
	}
	token, _, err := CreateSession(ctx, db, author.ID)
	if err != nil {
		t.Fatal(err)
	}
	sessionUser, valid, err := SessionUser(ctx, db, token)
	if err != nil || !valid || sessionUser.Role != "author" {
		t.Fatalf("SessionUser() = %+v, %v, %v; want valid author", sessionUser, valid, err)
	}
	if _, err := CreateUser(ctx, db, "bad_role", "another-secure-password", "superuser"); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("invalid role error = %v, want ErrInvalidRole", err)
	}
}
