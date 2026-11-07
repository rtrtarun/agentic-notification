package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type eventsPage struct {
	Events []struct {
		ID string `json:"id"`
	} `json:"events"`
	NextCursor string `json:"nextCursor"`
}

type inboxPage struct {
	Items []struct {
		ID string `json:"id"`
	} `json:"items"`
	Total      int    `json:"total"`
	NextCursor string `json:"nextCursor"`
}

func seedPullRequests(s *Server, from, to int) {
	for i := from; i <= to; i++ {
		body := fmt.Sprintf(`{"action":"opened","repository":{"full_name":"acme/web"},"pull_request":{"number":%d},"sender":{"login":"alice"}}`, i)
		webhook(s, "pull_request", fmt.Sprintf("pr-%d", i), body)
	}
}

func pagedEvents(t *testing.T, s *Server, query string) (int, eventsPage) {
	t.Helper()
	rr := call(s, http.MethodGet, "/events?"+query, "", withKey)
	var p eventsPage
	_ = json.Unmarshal(rr.Body.Bytes(), &p)
	return rr.Code, p
}

func TestEventsPageByCursorWithoutRepeatsOrGapsEvenWhenNewEventsArrive(t *testing.T) {
	s := newAuthServer(t, nil)
	seedPullRequests(s, 1, 5)

	_, first := pagedEvents(t, s, "limit=2")
	if len(first.Events) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %+v", first)
	}
	// New events arrive while the client is paging. Numbered pages would now shift.
	seedPullRequests(s, 6, 7)
	pages := []eventsPage{first}
	for cursor := first.NextCursor; cursor != ""; {
		_, next := pagedEvents(t, s, "limit=2&cursor="+url.QueryEscape(cursor))
		pages = append(pages, next)
		cursor = next.NextCursor
	}

	var got []string
	for _, p := range pages {
		for _, e := range p.Events {
			got = append(got, e.ID)
		}
	}
	want := []string{"github-pull_request-pr-5", "github-pull_request-pr-4", "github-pull_request-pr-3", "github-pull_request-pr-2", "github-pull_request-pr-1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("paging by cursor must give each older event exactly once, newest first and unaffected by new arrivals:\n got %v\nwant %v", got, want)
	}
	if len(pages) != 3 || pages[len(pages)-1].NextCursor != "" {
		t.Fatalf("five events in pages of two make three pages, the last without a cursor: %d", len(pages))
	}
}

func TestEventsWithoutACursorBehaveAsBeforeAndRefuseABadOne(t *testing.T) {
	s := newAuthServer(t, nil)
	seedPullRequests(s, 1, 3)
	code, all := pagedEvents(t, s, "")
	if code != http.StatusOK || len(all.Events) != 3 || all.NextCursor != "" || all.Events[0].ID != "github-pull_request-pr-3" {
		t.Fatalf("legacy call = %d %+v", code, all)
	}
	if code, _ := pagedEvents(t, s, "cursor=not-a-cursor"); code != http.StatusBadRequest {
		t.Fatalf("a bad cursor = %d, want 400", code)
	}
	if code, one := pagedEvents(t, s, "limit=1"); code != http.StatusOK || one.NextCursor == "" {
		t.Fatalf("limit=1 must offer a cursor: %d %+v", code, one)
	}
}

func TestNotificationsPageByCursorAndKeepNumberedPagesWorking(t *testing.T) {
	s := newAuthServer(t, nil)
	seedPullRequests(s, 1, 5)
	page := func(q string) (int, inboxPage) {
		rr := call(s, http.MethodGet, "/api/notifications?"+q, "", withKey)
		var p inboxPage
		_ = json.Unmarshal(rr.Body.Bytes(), &p)
		return rr.Code, p
	}
	_, first := page("pageSize=2")
	if len(first.Items) != 2 || first.NextCursor == "" || first.Total != 5 {
		t.Fatalf("numbered first page = %+v", first)
	}
	// The cursor from a numbered page continues exactly where it stopped.
	_, second := page("pageSize=2&cursor=" + url.QueryEscape(first.NextCursor))
	_, third := page("pageSize=2&cursor=" + url.QueryEscape(second.NextCursor))
	seen := map[string]bool{}
	var order []string
	for _, p := range []inboxPage{first, second, third} {
		for _, it := range p.Items {
			if seen[it.ID] {
				t.Fatalf("%s appeared twice", it.ID)
			}
			seen[it.ID] = true
			order = append(order, it.ID)
		}
	}
	if len(order) != 5 || order[0] != "github-pull_request-pr-5" || order[4] != "github-pull_request-pr-1" || third.NextCursor != "" {
		t.Fatalf("order = %v, last cursor %q", order, third.NextCursor)
	}
	if _, numbered := page("pageSize=2&page=3"); len(numbered.Items) != 1 {
		t.Fatalf("page=3 must still work: %+v", numbered)
	}
	for name, q := range map[string]string{
		"bad cursor":         "cursor=nope",
		"cursor with oldest": "sort=oldest&cursor=" + url.QueryEscape(first.NextCursor),
		"cursor with prio":   "sort=priority&cursor=" + url.QueryEscape(first.NextCursor),
	} {
		if code, _ := page(q); code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", name, code)
		}
	}
	if _, oldest := page("sort=oldest&pageSize=2"); oldest.NextCursor != "" {
		t.Fatalf("other sorts must not offer a cursor: %+v", oldest)
	}
}
