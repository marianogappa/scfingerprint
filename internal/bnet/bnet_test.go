package bnet

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient wires a client to a stub server with pacing off, so tests do
// not pay the live rate-limit floor.
func newTestClient(h http.Handler) (*Client, *httptest.Server) {
	srv := httptest.NewServer(h)
	c := New(srv.URL)
	c.SetMinInterval(0)
	return c, srv
}

func TestGetSanitizesInvalidUTF8(t *testing.T) {
	// Real payloads carry raw cp949 in map titles and lobby names. Without
	// sanitizing, one bad map name makes the whole document unparseable.
	body := append([]byte(`{"map_title":"`), 0xB1, 0xE6, '"', '}')
	c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	got, err := c.Get(context.Background(), "v1/whatever")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "�") {
		t.Fatalf("invalid bytes were not replaced: %q", got)
	}
}

func TestStatusErrorsAreActionable(t *testing.T) {
	cases := []struct {
		code int
		want string
	}{
		// 401 is the single most common cause of a failed run and it does
		// not mean "bad credentials".
		{http.StatusUnauthorized, "not logged in to Battle.net"},
		{http.StatusNotFound, "not found"},
		{http.StatusTooManyRequests, "rate limited"},
	}
	for _, tc := range cases {
		c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.code)
		}))
		_, err := c.Get(context.Background(), "v1/x")
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("HTTP %d gave err %v, want it to mention %q", tc.code, err, tc.want)
		}
	}
}

func TestRateLimitIsTerminalAndNotRetried(t *testing.T) {
	var calls int32
	c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	_, err := c.Get(context.Background(), "v1/x")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	// Retrying into a rate limit makes it worse, so it must be one attempt.
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("rate limit was requested %d times, want 1", n)
	}
}

func TestFourOhFourIsNotRetried(t *testing.T) {
	// A 404 is a definitive answer from the far side; asking again is waste,
	// and gateway probing does it a lot.
	var calls int32
	c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := c.Get(context.Background(), "v1/x"); err == nil {
		t.Fatal("expected an error")
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("404 was requested %d times, want 1", n)
	}
}

func TestGetRetriesAFlakyServerErrorAndSucceeds(t *testing.T) {
	// The bridge is a proxy and a single profile fetch occasionally stalls or
	// 5xxs; one such blip must not end a 150-account pass.
	var calls int32
	c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	got, err := c.Get(context.Background(), "v1/x")
	if err != nil {
		t.Fatalf("retry did not recover: %v", err)
	}
	if !strings.Contains(string(got), "ok") {
		t.Fatalf("body = %q", got)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("server saw %d calls, want 2", n)
	}
}

const queenProfile = `{
 "aurora_id": 18242965,
 "battle_tag": "kmw",
 "country_code": "KR",
 "toons": [
  {"toon": "Queennnnnn", "gateway_id": 30, "guid": 3, "games_last_week": 0},
  {"toon": "lllIIllIIllIII", "gateway_id": 11, "guid": 2, "games_last_week": 18}
 ],
 "replays": [
  {"link": "MM-DEAD-BEEF", "create_time": 1788520793,
   "attributes": {"replay_humans": "2", "replay_player_names": "Queennnnnn,someone", "replay_player_races": "Z,T"}},
  {"link": "1713934189", "create_time": 1788520793,
   "attributes": {"game_name": "practice", "replay_humans": "2",
                  "replay_player_names": "kimsabuho,Queennnnnn", "replay_player_races": "T,Z"}},
  {"link": "1713934190", "create_time": 1788520999,
   "attributes": {"replay_humans": "1", "replay_player_names": "Queennnnnn,오메가 분대"}}
 ]
}`

const emptyProfile = `{"aurora_id": 0, "toons": [], "replays": []}`

func TestProfileExistenceIsAuroraZeroNotA404(t *testing.T) {
	// An unknown toon/gateway pair answers 200 with aurora_id 0. Callers that
	// only check err would treat a stranger as a hit.
	c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/Queennnnnn/30") {
			_, _ = w.Write([]byte(queenProfile))
			return
		}
		_, _ = w.Write([]byte(emptyProfile))
	}))
	defer srv.Close()

	p, err := c.ProfileByToon(context.Background(), "nobody", 30)
	if err != nil {
		t.Fatal(err)
	}
	if p.Exists() {
		t.Fatal("an aurora_id 0 profile reported itself as existing")
	}

	p, err = c.ProfileByToon(context.Background(), "Queennnnnn", 30)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Exists() || p.AuroraID != 18242965 || p.BattleTag != "kmw" {
		t.Fatalf("profile = %+v", p)
	}
	// One call returns every alt across every gateway — the whole point of
	// entering through a single toon.
	if len(p.Toons) != 2 || p.Toons[1].GatewayID != 11 {
		t.Fatalf("toons = %+v", p.Toons)
	}
}

func TestFindProfileProbesGatewaysUntilOneExists(t *testing.T) {
	// There is no by-aurora-id route, so a toon whose gateway was never
	// recorded can only be found by probing.
	var seen []string
	c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/lllIIllIIllIII/11") {
			_, _ = w.Write([]byte(queenProfile))
			return
		}
		_, _ = w.Write([]byte(emptyProfile))
	}))
	defer srv.Close()

	p, gw, err := c.FindProfile(context.Background(), "lllIIllIIllIII")
	if err != nil {
		t.Fatal(err)
	}
	if p == nil || gw != 11 {
		t.Fatalf("FindProfile = %v, gateway %d, want gateway 11", p, gw)
	}
	// Korea is tried first because that is where most pro accounts live, and
	// probing stops at the first hit rather than sweeping all five.
	if len(seen) != 3 || !strings.HasSuffix(seen[0], "/30") {
		t.Fatalf("probe order = %v", seen)
	}

	seen = nil
	p, gw, err = c.FindProfile(context.Background(), "ghost")
	if err != nil {
		t.Fatal(err)
	}
	if p != nil || gw != 0 {
		t.Fatalf("FindProfile for an unknown toon = %v, %d; want nil, 0", p, gw)
	}
	// A toon on no gateway costs the full sweep and then reports nothing,
	// rather than reporting the last empty profile as a hit.
	if len(seen) != len(GatewayOrder) {
		t.Fatalf("unknown toon probed %d gateways, want %d", len(seen), len(GatewayOrder))
	}
}

func TestLadderDiscriminationAndGameKey(t *testing.T) {
	// `link` is the only ladder-vs-private discriminator in the payload, and
	// the discovery loop depends entirely on getting it right.
	ladder := Replay{Link: "MM-00096C84-8EB6-11F1"}
	private := Replay{Link: "1713934189"}
	if !ladder.IsLadder() || private.IsLadder() {
		t.Fatal("ladder discrimination is wrong")
	}
	if ladder.GameKey() != "MM-00096C84-8EB6-11F1" || private.GameKey() != "1713934189" {
		t.Fatalf("GameKey = %q / %q", ladder.GameKey(), private.GameKey())
	}
}

func TestReplayAttributes(t *testing.T) {
	a := ReplayAttributes{Humans: "2", PlayerNames: "kimsabuho,Queennnnnn", PlayerRaces: "T,Z"}
	if a.HumanCount() != 2 {
		t.Fatalf("HumanCount = %d", a.HumanCount())
	}
	if got := a.Names(); len(got) != 2 || got[1] != "Queennnnnn" {
		t.Fatalf("Names = %v", got)
	}
	if got := a.Races(); len(got) != 2 || got[0] != "T" {
		t.Fatalf("Races = %v", got)
	}

	// replay_humans == 1 means the opponents were computers — solo build
	// practice, and nothing to fingerprint.
	solo := ReplayAttributes{Humans: "1", PlayerNames: "Queennnnnn,오메가 분대"}
	if solo.HumanCount() != 1 {
		t.Fatalf("solo HumanCount = %d", solo.HumanCount())
	}

	// A missing or junk count must read as 0, not panic and not as 2.
	if (ReplayAttributes{}).HumanCount() != 0 {
		t.Fatal("empty HumanCount is not 0")
	}
	if (ReplayAttributes{Humans: "n/a"}).HumanCount() != 0 {
		t.Fatal("junk HumanCount is not 0")
	}
	if got := (ReplayAttributes{PlayerNames: "a,,b\n"}).Names(); len(got) != 2 {
		t.Fatalf("Names dropped or kept blanks wrongly: %v", got)
	}
}

func TestNameSearchParsesAndToleratesEmpty(t *testing.T) {
	c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/nobody") {
			return // the route answers 200 with an empty body
		}
		_, _ = w.Write([]byte(`[{"battletag":"kmw","gateway_id":45,"name":"botongnom","rank":341435}]`))
	}))
	defer srv.Close()

	hits, err := c.NameSearch(context.Background(), 1, "kmw")
	if err != nil {
		t.Fatal(err)
	}
	// The row's account name arrives under the key "name", not "toon".
	if len(hits) != 1 || hits[0].Toon != "botongnom" || hits[0].GatewayID != 45 {
		t.Fatalf("hits = %+v", hits)
	}

	hits, err = c.NameSearch(context.Background(), 1, "nobody")
	if err != nil || len(hits) != 0 {
		t.Fatalf("empty body gave %v, %v", hits, err)
	}
}

func TestGameInfoReturnsPublicReplayURLs(t *testing.T) {
	c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"replays":[{"url":"https://storage.googleapis.com/x.replay","md5":"abc"}]}`))
	}))
	defer srv.Close()

	gi, err := c.GameInfo(context.Background(), "1713934189")
	if err != nil {
		t.Fatal(err)
	}
	if len(gi.Replays) != 1 || gi.Replays[0].MD5 != "abc" {
		t.Fatalf("gameinfo = %+v", gi)
	}
}

func TestPaceEnforcesTheFloor(t *testing.T) {
	c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c.SetMinInterval(80 * time.Millisecond)

	start := time.Now()
	for range 3 {
		if _, err := c.Get(context.Background(), "v1/x"); err != nil {
			t.Fatal(err)
		}
	}
	// The first request is not delayed, so three requests cost two gaps.
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("three paced requests took %v, want at least two 80ms gaps", elapsed)
	}
}

func TestGatewaysRejectsAnEmptyAnswer(t *testing.T) {
	// Gateways is the probe Discover uses to tell the web-api port from the
	// game's other loopback listeners, so an empty body must not pass.
	c, srv := newTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	if _, err := c.Gateways(context.Background()); err == nil {
		t.Fatal("an empty gateway map was accepted as a valid web-api")
	}
}

func TestUploaderAuroraIDFromReplayURL(t *testing.T) {
	// A player whose toon has since been renamed is unreachable by any route,
	// so the aurora id in the upload path is the only way left to identify
	// their account.
	r := GameInfoReplay{URL: "https://storage.googleapis.com/starcraft-user-uploads-prod/S1-replays/14926205/112273536/abc.replay"}
	if got := r.UploaderAuroraID(); got != 14926205 {
		t.Fatalf("UploaderAuroraID = %d, want 14926205", got)
	}
	for _, url := range []string{"", "https://example.com/nope.replay", "https://x/S1-replays/notanumber/1/a.replay"} {
		if got := (GameInfoReplay{URL: url}).UploaderAuroraID(); got != 0 {
			t.Fatalf("UploaderAuroraID(%q) = %d, want 0", url, got)
		}
	}
}

func TestGameInfoAuroraIDsNamesBothPlayers(t *testing.T) {
	// Every player uploads their own copy, so a 1v1's uploads name both
	// accounts. Duplicates and id-less URLs must not appear.
	gi := &GameInfo{Replays: []GameInfoReplay{
		{URL: "https://x/S1-replays/14926205/1/a.replay"},
		{URL: "https://x/S1-replays/18242965/2/b.replay"},
		{URL: "https://x/S1-replays/14926205/3/c.replay"},
		{URL: "https://x/no-id.replay"},
	}}
	got := gi.AuroraIDs()
	if len(got) != 2 || got[0] != 14926205 || got[1] != 18242965 {
		t.Fatalf("AuroraIDs = %v, want [14926205 18242965]", got)
	}
	if got := (&GameInfo{}).AuroraIDs(); got != nil {
		t.Fatalf("AuroraIDs on an empty game = %v", got)
	}
}
