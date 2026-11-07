package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

var ctx = context.Background()

const goodPassword = "correct horse battery"

func newSvc() (*Service, *MemoryStore, *clockT) {
	store := NewMemoryStore()
	clk := &clockT{t: time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)}
	return NewService(store, time.Hour).WithClock(clk.now), store, clk
}

type clockT struct{ t time.Time }

func (c *clockT) now() time.Time { return c.t }

func TestPasswordHashingAndChecks(t *testing.T) {
	h, err := HashPassword(goodPassword)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "argon2id$") || strings.Contains(h, goodPassword) {
		t.Fatalf("hash = %q", h)
	}
	if !VerifyPassword(h, goodPassword) || VerifyPassword(h, goodPassword+"x") {
		t.Fatal("verify must accept only the right password")
	}
	h2, _ := HashPassword(goodPassword)
	if h == h2 {
		t.Fatal("salts must differ")
	}
	for _, bad := range []string{"", "argon2id$m=0,t=0,p=0$a$b", "bcrypt$x$y$z", "argon2id$m=1,t=1,p=1$!!$!!", "plain"} {
		if VerifyPassword(bad, goodPassword) {
			t.Fatalf("a malformed hash %q must never verify", bad)
		}
	}
	if CheckPassword("short") == nil || CheckPassword(strings.Repeat("a", 300)) == nil || CheckPassword(goodPassword) != nil {
		t.Fatal("password length rules")
	}
}

func TestBootstrapCreatesOnlyTheFirstAdmin(t *testing.T) {
	svc, _, _ := newSvc()
	if made, err := svc.Bootstrap(ctx, "Admin@Example.com", goodPassword); err != nil || !made {
		t.Fatalf("bootstrap = %v, %v", made, err)
	}
	if made, _ := svc.Bootstrap(ctx, "other@example.com", "another long password"); made {
		t.Fatal("a second bootstrap must do nothing, or a restart could reset a password")
	}
	users, _ := svc.Users(ctx)
	if len(users) != 1 || users[0].Email != "admin@example.com" || users[0].Role != RoleAdmin {
		t.Fatalf("users = %+v", users)
	}
	if _, err := svc.Bootstrap(ctx, "x", "y"); err != nil {
		t.Fatalf("with users present the arguments are not even read: %v", err)
	}
	empty, _, _ := newSvc()
	if _, err := empty.Bootstrap(ctx, "not-an-email", goodPassword); err == nil {
		t.Fatal("a bad bootstrap email must be rejected")
	}
}

func TestLoginStartsAndEndsASession(t *testing.T) {
	svc, store, clk := newSvc()
	svc.Bootstrap(ctx, "a@example.com", goodPassword)

	token, sess, user, err := svc.Login(ctx, " A@Example.com ", goodPassword, "1.2.3.4")
	if err != nil || user.Email != "a@example.com" || sess.CSRF == "" {
		t.Fatalf("login = %v %+v", err, user)
	}
	if _, err := store.SessionByHash(ctx, token); err == nil {
		t.Fatal("the raw token must not be what is stored")
	}
	if got, gotSess, err := svc.Authenticate(ctx, token); err != nil || got.ID != user.ID || gotSess.CSRF != sess.CSRF {
		t.Fatalf("authenticate = %v", err)
	}

	clk.t = clk.t.Add(2 * time.Hour)
	if _, _, err := svc.Authenticate(ctx, token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("an expired session must be refused: %v", err)
	}

	clk.t = clk.t.Add(-2 * time.Hour)
	svc.Logout(ctx, token)
	if _, _, err := svc.Authenticate(ctx, token); err == nil {
		t.Fatal("a logged-out session must be refused")
	}
	if _, _, err := svc.Authenticate(ctx, ""); err == nil {
		t.Fatal("no token")
	}
	if svc.Logout(ctx, "") != nil {
		t.Fatal("logging out without a session is not an error")
	}
}

func TestLoginErrorsDoNotRevealWhichAccountsExist(t *testing.T) {
	svc, _, _ := newSvc()
	svc.Bootstrap(ctx, "a@example.com", goodPassword)
	svc.CreateUser(ctx, "a", "sso@example.com", "", RoleMember, "") // no password
	off := true
	u, _ := svc.CreateUser(ctx, "a", "off@example.com", "", RoleMember, goodPassword)
	svc.UpdateUser(ctx, "a", u.ID, UserPatch{Disabled: &off})

	for _, c := range []struct{ email, pw string }{
		{"a@example.com", "wrong password here"}, {"nobody@example.com", goodPassword},
		{"sso@example.com", goodPassword}, {"off@example.com", goodPassword},
	} {
		if _, _, _, err := svc.Login(ctx, c.email, c.pw, "ip"); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("%s: err = %v, want the same generic error", c.email, err)
		}
	}
}

func TestRepeatedFailuresLockTheAccountAndTheAddress(t *testing.T) {
	svc, _, clk := newSvc()
	svc.Bootstrap(ctx, "a@example.com", goodPassword)
	for i := 0; i < DefaultMaxFailures; i++ {
		svc.Login(ctx, "a@example.com", "wrong password here", "9.9.9.9")
	}
	if _, _, _, err := svc.Login(ctx, "a@example.com", goodPassword, "1.1.1.1"); !errors.Is(err, ErrLocked) {
		t.Fatalf("the right password must not get in while locked: %v", err)
	}
	if _, _, _, err := svc.Login(ctx, "other@example.com", goodPassword, "9.9.9.9"); errors.Is(err, ErrLocked) {
		t.Fatal("one account's typos must not lock a shared address for everyone")
	}
	// One address trying many accounts (spraying) is locked.
	for i := 0; i < DefaultMaxFailures*ipFactor; i++ {
		svc.Login(ctx, fmt.Sprintf("user%d@example.com", i), "wrong password here", "6.6.6.6")
	}
	if _, _, _, err := svc.Login(ctx, "fresh@example.com", goodPassword, "6.6.6.6"); !errors.Is(err, ErrLocked) {
		t.Fatalf("a spraying address must be refused for every email: %v", err)
	}
	clk.t = clk.t.Add(DefaultLockout + time.Second)
	if _, _, _, err := svc.Login(ctx, "a@example.com", goodPassword, "1.1.1.1"); err != nil {
		t.Fatalf("the lock must expire: %v", err)
	}
}

func TestASuccessfulLoginClearsTheFailureCount(t *testing.T) {
	svc, _, _ := newSvc()
	svc.Bootstrap(ctx, "a@example.com", goodPassword)
	for round := 0; round < 3; round++ {
		for i := 0; i < DefaultMaxFailures-1; i++ {
			svc.Login(ctx, "a@example.com", "wrong password here", "ip")
		}
		if _, _, _, err := svc.Login(ctx, "a@example.com", goodPassword, "ip"); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
}

func TestUserManagementRules(t *testing.T) {
	svc, _, _ := newSvc()
	svc.Bootstrap(ctx, "root@example.com", goodPassword)
	users, _ := svc.Users(ctx)
	root := users[0]

	member, err := svc.CreateUser(ctx, "root", "m@example.com", "Mo", RoleMember, goodPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateUser(ctx, "root", "M@example.com", "", RoleMember, ""); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("duplicate email = %v", err)
	}
	for _, bad := range []struct{ email, role, pw string }{
		{"nope", RoleMember, ""}, {"x@example.com", "owner", ""}, {"y@example.com", RoleMember, "short"},
	} {
		if _, err := svc.CreateUser(ctx, "root", bad.email, "", bad.role, bad.pw); err == nil {
			t.Errorf("%+v must be rejected", bad)
		}
	}

	member2, _ := svc.CreateUser(ctx, "root", "m2@example.com", "", RoleMember, "")
	_ = member2
	demote := RoleMember
	if _, err := svc.UpdateUser(ctx, "root", root.ID, UserPatch{Role: &demote}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demoting the last admin = %v", err)
	}
	off := true
	if _, err := svc.UpdateUser(ctx, "root", root.ID, UserPatch{Disabled: &off}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("disabling the last admin = %v", err)
	}
	admin := RoleAdmin
	if _, err := svc.UpdateUser(ctx, "root", member.ID, UserPatch{Role: &admin}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateUser(ctx, "root", root.ID, UserPatch{Role: &demote}); err != nil {
		t.Fatalf("with a second admin the first can step down: %v", err)
	}
	if _, err := svc.UpdateUser(ctx, "root", "missing", UserPatch{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user = %v", err)
	}
	bogus := "owner"
	if _, err := svc.UpdateUser(ctx, "root", member.ID, UserPatch{Role: &bogus}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad role = %v", err)
	}
}

func TestDisablingOrChangingAPasswordEndsSessions(t *testing.T) {
	svc, _, _ := newSvc()
	svc.Bootstrap(ctx, "root@example.com", goodPassword)
	u, _ := svc.CreateUser(ctx, "root", "m@example.com", "", RoleMember, goodPassword)

	token, _, _, _ := svc.Login(ctx, "m@example.com", goodPassword, "ip")
	off := true
	svc.UpdateUser(ctx, "root", u.ID, UserPatch{Disabled: &off})
	if _, _, err := svc.Authenticate(ctx, token); err == nil {
		t.Fatal("a disabled account's session must end")
	}

	on := false
	svc.UpdateUser(ctx, "root", u.ID, UserPatch{Disabled: &on})
	token, _, _, _ = svc.Login(ctx, "m@example.com", goodPassword, "ip")
	pw := "a brand new password"
	svc.UpdateUser(ctx, "root", u.ID, UserPatch{Password: &pw})
	if _, _, err := svc.Authenticate(ctx, token); err == nil {
		t.Fatal("an admin password reset must end the account's sessions")
	}
	if _, _, _, err := svc.Login(ctx, "m@example.com", pw, "ip"); err != nil {
		t.Fatalf("the new password must work: %v", err)
	}
}

func TestChangePassword(t *testing.T) {
	svc, _, _ := newSvc()
	svc.Bootstrap(ctx, "root@example.com", goodPassword)
	users, _ := svc.Users(ctx)
	id := users[0].ID
	token, _, _, _ := svc.Login(ctx, "root@example.com", goodPassword, "ip")

	if err := svc.ChangePassword(ctx, id, "wrong current password", "a brand new password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong current = %v", err)
	}
	if err := svc.ChangePassword(ctx, id, goodPassword, "short"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("weak new = %v", err)
	}
	if err := svc.ChangePassword(ctx, id, goodPassword, "a brand new password"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Authenticate(ctx, token); err == nil {
		t.Fatal("changing the password must end existing sessions")
	}
	if err := svc.ChangePassword(ctx, "missing", "x", "y"); err == nil {
		t.Fatal("unknown user")
	}
}

func TestFederatedLoginLinksProvisionsAndRefuses(t *testing.T) {
	svc, _, _ := newSvc()
	svc.Bootstrap(ctx, "root@corp.com", goodPassword)
	allowed := []string{"corp.com"}

	// An existing account is found by email and linked.
	_, _, u, err := svc.LoginFederated(ctx, "iss", "sub-1", "Root@Corp.com", "Root", nil, "ip")
	if err != nil || u.Email != "root@corp.com" || u.Role != RoleAdmin {
		t.Fatalf("link existing = %v %+v", err, u)
	}
	// After linking, the email may change at the provider: the subject still finds the account.
	if _, _, u2, err := svc.LoginFederated(ctx, "iss", "sub-1", "renamed@corp.com", "", nil, "ip"); err != nil || u2.ID != u.ID {
		t.Fatalf("by subject = %v", err)
	}
	// A new person is provisioned as a member only for an allowed domain.
	_, _, n, err := svc.LoginFederated(ctx, "iss", "sub-2", "new@corp.com", "New", allowed, "ip")
	if err != nil || n.Role != RoleMember {
		t.Fatalf("provision = %v %+v", err, n)
	}
	for _, c := range []struct {
		name, email string
		domains     []string
	}{
		{"domain not allowed", "evil@other.com", allowed}, {"empty allow-list", "new2@corp.com", nil},
		{"no email", "", allowed}, {"look-alike domain", "x@corp.com.evil.io", allowed},
	} {
		if _, _, _, err := svc.LoginFederated(ctx, "iss", "sub-"+c.name, c.email, "", c.domains, "ip"); !errors.Is(err, ErrNotAllowed) {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
	if _, _, _, err := svc.LoginFederated(ctx, "", "", "a@corp.com", "", allowed, "ip"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("no issuer = %v", err)
	}
	off := true
	svc.UpdateUser(ctx, "root", n.ID, UserPatch{Disabled: &off})
	if _, _, _, err := svc.LoginFederated(ctx, "iss", "sub-2", "new@corp.com", "", allowed, "ip"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("a disabled account must not sign in through SSO either: %v", err)
	}
}

func TestAuditRecordsActionsWithoutSecrets(t *testing.T) {
	svc, _, _ := newSvc()
	svc.Bootstrap(ctx, "root@example.com", goodPassword)
	svc.Login(ctx, "root@example.com", "wrong password here", "7.7.7.7")
	svc.Login(ctx, "root@example.com", goodPassword, "7.7.7.7")
	pw := "a brand new password"
	users, _ := svc.Users(ctx)
	svc.UpdateUser(ctx, "root", users[0].ID, UserPatch{Password: &pw})

	entries, _ := svc.Audit(ctx, 50)
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Action] = true
		for _, secret := range []string{goodPassword, pw, "wrong password here", "argon2id"} {
			if strings.Contains(e.Detail+e.Target+e.Actor, secret) {
				t.Fatalf("audit entry leaks a secret: %+v", e)
			}
		}
	}
	for _, want := range []string{"user.created", "login.failed", "login", "user.updated"} {
		if !seen[want] {
			t.Errorf("missing audit action %q in %v", want, seen)
		}
	}
	if limited, _ := svc.Audit(ctx, 1); len(limited) != 1 {
		t.Fatal("audit limit")
	}
}

func TestValidCSRF(t *testing.T) {
	s := Session{CSRF: "abc"}
	if !ValidCSRF(s, "abc") || ValidCSRF(s, "abd") || ValidCSRF(s, "") || ValidCSRF(Session{}, "") {
		t.Fatal("csrf comparison")
	}
}

func TestLimiterBoundsMemory(t *testing.T) {
	l := NewLimiter(3, time.Minute)
	for i := 0; i < maxTracked+10; i++ {
		l.Fail("k" + string(rune(i%1000)) + strings.Repeat("x", i%7) + time.Duration(i).String())
	}
	if len(l.failures) > maxTracked {
		t.Fatalf("tracked %d keys", len(l.failures))
	}
}

func TestMemoryStoreDetails(t *testing.T) {
	st := NewMemoryStore()
	u := User{ID: "1", Email: "a@x.io", Role: RoleAdmin}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser(ctx, User{ID: "2", Email: "a@x.io"}); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("dup = %v", err)
	}
	if err := st.LinkIdentity(ctx, "nope", "i", "s"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("link unknown = %v", err)
	}
	if _, err := st.UserByIdentity(ctx, "i", "s"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("identity = %v", err)
	}
	if err := st.UpdateUser(ctx, User{ID: "zzz"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update unknown = %v", err)
	}
	if n, _ := st.CountActiveAdmins(ctx); n != 1 {
		t.Fatalf("admins = %d", n)
	}
	for i := 0; i < maxMemoryAudit+5; i++ {
		st.AddAudit(ctx, AuditEntry{Action: "x"})
	}
	if got, _ := st.ListAudit(ctx, 0); len(got) != maxMemoryAudit {
		t.Fatalf("audit kept %d", len(got))
	}
}
