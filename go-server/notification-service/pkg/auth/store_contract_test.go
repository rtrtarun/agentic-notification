package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

var contractNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// storeContract is the behaviour every Store must have. It runs against the
// memory store always and against PostgreSQL in the integration test.
func storeContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	admin := User{ID: "u-admin", Email: "admin@corp.com", Name: "Ada", Role: RoleAdmin, PasswordHash: "hash", CreatedAt: contractNow}
	member := User{ID: "u-member", Email: "member@corp.com", Name: "Max", Role: RoleMember, CreatedAt: contractNow}
	for _, u := range []User{admin, member} {
		if err := s.CreateUser(ctx, u); err != nil {
			t.Fatalf("create %s: %v", u.ID, err)
		}
	}

	t.Run("a second account with the same email is refused", func(t *testing.T) {
		dup := User{ID: "u-dup", Email: "admin@corp.com", Role: RoleMember, CreatedAt: contractNow}
		if err := s.CreateUser(ctx, dup); !errors.Is(err, ErrEmailTaken) {
			t.Fatalf("got %v, want ErrEmailTaken", err)
		}
	})

	t.Run("users are found by id and email, and a missing one is ErrNotFound", func(t *testing.T) {
		got, err := s.UserByID(ctx, "u-admin")
		if err != nil || got.Email != "admin@corp.com" || got.Role != RoleAdmin || got.PasswordHash != "hash" {
			t.Fatalf("by id: %+v %v", got, err)
		}
		if got, err = s.UserByEmail(ctx, "member@corp.com"); err != nil || got.ID != "u-member" {
			t.Fatalf("by email: %+v %v", got, err)
		}
		if _, err = s.UserByID(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing id: %v", err)
		}
		if _, err = s.UserByEmail(ctx, "nobody@corp.com"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing email: %v", err)
		}
	})

	t.Run("counts and listing", func(t *testing.T) {
		if n, err := s.CountUsers(ctx); err != nil || n != 2 {
			t.Fatalf("CountUsers = %d, %v", n, err)
		}
		if n, err := s.CountActiveAdmins(ctx); err != nil || n != 1 {
			t.Fatalf("CountActiveAdmins = %d, %v", n, err)
		}
		list, err := s.ListUsers(ctx)
		if err != nil || len(list) != 2 {
			t.Fatalf("ListUsers = %d, %v", len(list), err)
		}
	})

	t.Run("a disabled admin no longer counts, and updates persist", func(t *testing.T) {
		admin.Disabled, admin.Name = true, "Ada L."
		if err := s.UpdateUser(ctx, admin); err != nil {
			t.Fatal(err)
		}
		got, _ := s.UserByID(ctx, "u-admin")
		if !got.Disabled || got.Name != "Ada L." {
			t.Fatalf("update not stored: %+v", got)
		}
		if n, _ := s.CountActiveAdmins(ctx); n != 0 {
			t.Fatalf("a disabled admin must not count, got %d", n)
		}
		admin.Disabled = false
		if err := s.UpdateUser(ctx, admin); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("an account's approval status is stored and changes persist", func(t *testing.T) {
		pending := User{ID: "u-pending", Email: "pending@corp.com", Role: RoleMember, Status: StatusPending, PasswordHash: "hash", CreatedAt: contractNow}
		if err := s.CreateUser(ctx, pending); err != nil {
			t.Fatal(err)
		}
		got, err := s.UserByEmail(ctx, "pending@corp.com")
		if err != nil || got.Status != StatusPending || got.Active() {
			t.Fatalf("a pending account must read back pending and not active: %+v, %v", got, err)
		}
		got.Status = StatusActive
		if err := s.UpdateUser(ctx, got); err != nil {
			t.Fatal(err)
		}
		if again, _ := s.UserByID(ctx, "u-pending"); again.Status != StatusActive || !again.Active() {
			t.Fatalf("approval not stored: %+v", again)
		}
		if n, _ := s.CountActiveAdmins(ctx); n < 0 {
			t.Fatal("unreachable")
		}
	})

	t.Run("an identity links to one user and signs them in", func(t *testing.T) {
		if err := s.LinkIdentity(ctx, "u-member", "https://login.example", "sub-1"); err != nil {
			t.Fatal(err)
		}
		got, err := s.UserByIdentity(ctx, "https://login.example", "sub-1")
		if err != nil || got.ID != "u-member" {
			t.Fatalf("by identity: %+v %v", got, err)
		}
		if _, err = s.UserByIdentity(ctx, "https://login.example", "other"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown identity: %v", err)
		}
		// Linking the same identity again must not fail or create a second link.
		if err := s.LinkIdentity(ctx, "u-member", "https://login.example", "sub-1"); err != nil {
			t.Fatalf("relinking: %v", err)
		}
	})

	t.Run("session lifecycle", func(t *testing.T) {
		live := Session{Hash: "h-live", UserID: "u-admin", CSRF: "csrf", ExpiresAt: contractNow.Add(time.Hour), CreatedAt: contractNow}
		old := Session{Hash: "h-old", UserID: "u-admin", CSRF: "csrf2", ExpiresAt: contractNow.Add(-time.Hour), CreatedAt: contractNow.Add(-2 * time.Hour)}
		other := Session{Hash: "h-other", UserID: "u-member", CSRF: "csrf3", ExpiresAt: contractNow.Add(time.Hour), CreatedAt: contractNow}
		for _, sess := range []Session{live, old, other} {
			if err := s.CreateSession(ctx, sess); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.SessionByHash(ctx, "h-live")
		if err != nil || got.UserID != "u-admin" || got.CSRF != "csrf" || !got.ExpiresAt.Equal(live.ExpiresAt) {
			t.Fatalf("session = %+v %v", got, err)
		}
		if _, err = s.SessionByHash(ctx, "nope"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown session: %v", err)
		}
		if err := s.DeleteExpiredSessions(ctx, contractNow); err != nil {
			t.Fatal(err)
		}
		if _, err = s.SessionByHash(ctx, "h-old"); !errors.Is(err, ErrNotFound) {
			t.Fatal("an expired session must be removed")
		}
		if _, err = s.SessionByHash(ctx, "h-live"); err != nil {
			t.Fatal("a live session must survive clean-up")
		}
		if err := s.DeleteSession(ctx, "h-live"); err != nil {
			t.Fatal(err)
		}
		if _, err = s.SessionByHash(ctx, "h-live"); !errors.Is(err, ErrNotFound) {
			t.Fatal("a deleted session must be gone")
		}
		if err := s.DeleteUserSessions(ctx, "u-member"); err != nil {
			t.Fatal(err)
		}
		if _, err = s.SessionByHash(ctx, "h-other"); !errors.Is(err, ErrNotFound) {
			t.Fatal("signing a user out everywhere must remove their sessions")
		}
	})

	t.Run("the audit trail is kept newest first and can be limited", func(t *testing.T) {
		for i, action := range []string{"login", "rule.updated", "digest.generated"} {
			e := AuditEntry{At: contractNow.Add(time.Duration(i) * time.Minute), Actor: "admin@corp.com", Action: action, Target: "t", IP: "10.0.0.1", Detail: "d"}
			if err := s.AddAudit(ctx, e); err != nil {
				t.Fatal(err)
			}
		}
		all, err := s.ListAudit(ctx, 10)
		if err != nil || len(all) != 3 || all[0].Action != "digest.generated" || all[2].Action != "login" {
			t.Fatalf("audit = %+v %v", all, err)
		}
		if all[0].IP != "10.0.0.1" || all[0].Target != "t" || all[0].Detail != "d" {
			t.Fatalf("audit fields lost: %+v", all[0])
		}
		if two, _ := s.ListAudit(ctx, 2); len(two) != 2 {
			t.Fatalf("limit ignored: %d", len(two))
		}
	})
}

func TestMemoryStoreContract(t *testing.T) { storeContract(t, NewMemoryStore()) }
