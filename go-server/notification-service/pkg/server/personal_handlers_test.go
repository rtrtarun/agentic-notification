package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func digestCount(t *testing.T, body string) int {
	t.Helper()
	var out struct {
		Generated bool `json:"generated"`
		Digest    struct {
			Count  int    `json:"count"`
			UserID string `json:"userId"`
		} `json:"digest"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Generated {
		return -1
	}
	return out.Digest.Count
}

func TestEachPersonGetsADigestShapedByTheirOwnPreferences(t *testing.T) {
	s := newAuthServer(t, nil)
	starEvents(s, 3) // one star in each of acme/web, acme/api and acme/docs, all held for the digest
	alice := addMember(t, s, "alice@corp.com")
	bob := addMember(t, s, "bob@corp.com")

	// Before saving anything, a person sees the workspace's preferences, flagged as not their own.
	if rr := call(s, http.MethodGet, "/api/me/preferences", "", alice.apply(false)); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"personal":false`) {
		t.Fatalf("fallback = %d %s", rr.Code, rr.Body.String())
	}
	put := `{"quietHours":{"enabled":false,"start":"22:00","end":"07:00","timezone":"UTC"},"mutedSources":[],"mutedRepositories":["acme/docs"]}`
	if rr := call(s, http.MethodPut, "/api/me/preferences", put, alice.apply(true)); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"personal":true`) {
		t.Fatalf("save = %d %s", rr.Code, rr.Body.String())
	}
	if rr := call(s, http.MethodGet, "/api/me/preferences", "", bob.apply(false)); !strings.Contains(rr.Body.String(), `"personal":false`) || strings.Contains(rr.Body.String(), "acme/docs") {
		t.Fatalf("alice's choices must not reach bob: %s", rr.Body.String())
	}

	a := call(s, http.MethodPost, "/api/digests/generate?scope=me", "", alice.apply(true))
	b := call(s, http.MethodPost, "/api/digests/generate?scope=me", "", bob.apply(true))
	if a.Code != http.StatusCreated || b.Code != http.StatusCreated {
		t.Fatalf("generate = %d %d (%s %s)", a.Code, b.Code, a.Body.String(), b.Body.String())
	}
	if digestCount(t, a.Body.String()) != 2 || digestCount(t, b.Body.String()) != 3 {
		t.Fatalf("alice muted one repository so she gets 2 and bob gets 3: %s | %s", a.Body.String(), b.Body.String())
	}

	// Each person lists only their own; the workspace list holds neither.
	mine := call(s, http.MethodGet, "/api/digests?scope=me", "", alice.apply(false)).Body.String()
	if strings.Count(mine, `"userId"`) != 1 {
		t.Fatalf("alice should see exactly her own digest: %s", mine)
	}
	if ws := call(s, http.MethodGet, "/api/digests", "", withKey).Body.String(); strings.Contains(ws, `"userId"`) {
		t.Fatalf("the workspace list must not include personal digests: %s", ws)
	}
	// A second request has nothing new for either of them.
	if again := call(s, http.MethodPost, "/api/digests/generate?scope=me", "", alice.apply(true)); digestCount(t, again.Body.String()) != -1 {
		t.Fatalf("nothing new since alice's last digest: %s", again.Body.String())
	}
	// The workspace digest is unaffected and still admin only.
	if rr := call(s, http.MethodPost, "/api/digests/generate", "", alice.apply(true)); rr.Code != http.StatusForbidden {
		t.Fatalf("a member making the workspace digest = %d, want 403", rr.Code)
	}
	if ws := call(s, http.MethodPost, "/api/digests/generate", "", withKey); digestCount(t, ws.Body.String()) != 3 {
		t.Fatalf("the workspace digest still covers everything: %s", ws.Body.String())
	}
}

func TestANeverChoiceShapesOnlyThatPersonsDigest(t *testing.T) {
	s := newAuthServer(t, nil)
	starEvents(s, 3)
	alice := addMember(t, s, "alice@corp.com")
	bob := addMember(t, s, "bob@corp.com")
	rr := call(s, http.MethodPost, "/feedback", `{"eventId":"github-star-star-a","action":"never","scope":"type"}`, alice.apply(true))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("feedback = %d %s", rr.Code, rr.Body.String())
	}
	a := call(s, http.MethodPost, "/api/digests/generate?scope=me", "", alice.apply(true))
	b := call(s, http.MethodPost, "/api/digests/generate?scope=me", "", bob.apply(true))
	if got := digestCount(t, a.Body.String()); got != -1 {
		t.Fatalf("alice said never about stars, so she has nothing: %s", a.Body.String())
	}
	if digestCount(t, b.Body.String()) != 3 {
		t.Fatalf("bob's digest must be unaffected: %s", b.Body.String())
	}
}

func TestPersonalViewsNeedAPersonAndValidInput(t *testing.T) {
	s := newAuthServer(t, nil)
	alice := addMember(t, s, "alice@corp.com")
	for _, c := range []struct {
		method, path, body string
		edit               func(*http.Request)
		want               int
	}{
		{http.MethodGet, "/api/me/preferences", "", withKey, http.StatusBadRequest},
		{http.MethodPut, "/api/me/preferences", `{}`, withKey, http.StatusBadRequest},
		{http.MethodGet, "/api/digests?scope=me", "", withKey, http.StatusBadRequest},
		{http.MethodPost, "/api/digests/generate?scope=me", "", withKey, http.StatusBadRequest},
		{http.MethodGet, "/api/me/preferences", "", nil, http.StatusUnauthorized},
		{http.MethodDelete, "/api/me/preferences", "", alice.apply(true), http.StatusMethodNotAllowed},
		{http.MethodPut, "/api/me/preferences", `{"quietHours":{"enabled":true,"start":"late","end":"07:00","timezone":"UTC"}}`, alice.apply(true), http.StatusBadRequest},
	} {
		if rr := call(s, c.method, c.path, c.body, c.edit); rr.Code != c.want {
			t.Errorf("%s %s = %d, want %d (%s)", c.method, c.path, rr.Code, c.want, rr.Body.String())
		}
	}
}

func TestACriticalEventIsNeverHiddenFromAPersonalDigestByMutes(t *testing.T) {
	s := newAuthServer(t, nil)
	starEvents(s, 1)
	webhook(s, "security_advisory", "sec-1", securityPayload)
	alice := addMember(t, s, "alice@corp.com")
	put := `{"quietHours":{"enabled":false,"start":"22:00","end":"07:00","timezone":"UTC"},"mutedSources":["github"],"mutedRepositories":[]}`
	call(s, http.MethodPut, "/api/me/preferences", put, alice.apply(true))
	rr := call(s, http.MethodPost, "/api/digests/generate?scope=me", "", alice.apply(true))
	if strings.Contains(rr.Body.String(), "security") {
		t.Fatalf("a critical event is never in a digest, held or not: %s", rr.Body.String())
	}
	if got := call(s, http.MethodGet, "/api/explain/"+eventIDFromList(t, s), "", withKey); !strings.Contains(got.Body.String(), "critical") {
		t.Fatalf("the critical event is still explained as sent at once: %s", got.Body.String())
	}
}

func eventIDFromList(t *testing.T, s *Server) string {
	t.Helper()
	rr := webhook(s, "security_advisory", "sec-2", securityPayload)
	return eventID(t, rr)
}
