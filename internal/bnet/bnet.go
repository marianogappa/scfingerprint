// Package bnet is a client for the local web-api that a running, logged-in
// StarCraft: Remastered client exposes on loopback. It exists to serve the
// offline registry refresh tool and is never touched by the matching path.
//
// The client is a plain HTTP bridge: the game registers a single catch-all
// route, web-api/(.*), and forwards the tail to Blizzard's classic web-api.
// So there is no route list to read locally, and an unknown route is a 404
// from the far side rather than a local error.
//
// Two things routinely surprise callers:
//
//   - The listening port changes on every launch, so it has to be discovered
//     (see Discover) rather than configured.
//   - An unknown toon is HTTP 200 with aurora_id 0, not a 404. AuroraID == 0
//     is the existence check.
package bnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Gateways are the five official gateways, by the gateway_id the web-api uses.
var Gateways = map[int]string{
	10: "U.S. West",
	11: "U.S. East",
	20: "Europe",
	30: "Korea",
	45: "Asia",
}

// GatewayOrder lists gateway IDs in a stable, human-sensible order for output
// and for probing a toon whose gateway is unknown. Korea first: it is where
// most progamer accounts are registered.
var GatewayOrder = []int{30, 20, 11, 10, 45}

// Client talks to one running SC:R client's local web-api.
type Client struct {
	baseURL string
	http    *http.Client

	// minInterval is the floor between requests. The bridge rate-limits, and
	// a refresh run makes hundreds of calls, so pacing is not optional.
	minInterval time.Duration
	last        time.Time
}

// New builds a client for an already-known base URL, e.g.
// "http://127.0.0.1:56280". Prefer Discover, which finds the port itself.
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		// A local proxy call that has not answered in 12s is stalled, not
		// slow: a 300KB profile fetch normally lands in ~2s. Waiting longer
		// just multiplies the cost of the retries below.
		http:        &http.Client{Timeout: 12 * time.Second},
		minInterval: 350 * time.Millisecond,
	}
}

// SetMinInterval overrides the pacing floor between requests.
func (c *Client) SetMinInterval(d time.Duration) { c.minInterval = d }

// BaseURL reports the endpoint this client is bound to.
func (c *Client) BaseURL() string { return c.baseURL }

// Discover finds the local web-api of a running SC:R client by asking lsof for
// the game's loopback listeners and probing each one. The game opens more than
// one local port and only one of them answers web-api requests, so probing is
// the only reliable way to tell them apart.
func Discover(ctx context.Context) (*Client, error) {
	ports, err := listenPorts(ctx)
	if err != nil {
		return nil, err
	}
	if len(ports) == 0 {
		return nil, fmt.Errorf("bnet: no loopback listeners found — is StarCraft: Remastered running?")
	}
	var tried []string
	for _, p := range ports {
		c := New(fmt.Sprintf("http://127.0.0.1:%d", p))
		// The game's other loopback listeners accept the connection and then
		// never answer, so probing one costs a full timeout. Keep that short:
		// the web-api answers v1/gateways in milliseconds.
		c.http.Timeout = 3 * time.Second
		c.minInterval = 0
		if _, err := c.Gateways(ctx); err != nil {
			tried = append(tried, fmt.Sprintf("%d (%v)", p, err))
			continue
		}
		return c, nil
	}
	return nil, fmt.Errorf("bnet: found listeners on %s but none served the web-api; if the game is running, it is probably not logged in to Battle.net", strings.Join(tried, ", "))
}

var lsofPortRE = regexp.MustCompile(`127\.0\.0\.1:(\d+) \(LISTEN\)`)

// listenPorts shells out to lsof for the loopback ports the SC:R process is
// listening on. macOS-only by construction: this whole package only makes
// sense next to a running desktop client.
func listenPorts(ctx context.Context) ([]int, error) {
	pidOut, err := exec.CommandContext(ctx, "pgrep", "-f", "StarCraft.app/Contents/MacOS/StarCraft").Output()
	if err != nil {
		return nil, fmt.Errorf("bnet: StarCraft: Remastered does not appear to be running: %w", err)
	}
	var ports []int
	seen := map[int]bool{}
	for pidStr := range strings.FieldsSeq(string(pidOut)) {
		out, err := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP", "-a", "-p", pidStr).Output()
		if err != nil {
			continue
		}
		for _, m := range lsofPortRE.FindAllStringSubmatch(string(out), -1) {
			p, err := strconv.Atoi(m[1])
			if err != nil || seen[p] {
				continue
			}
			seen[p] = true
			ports = append(ports, p)
		}
	}
	sort.Ints(ports)
	return ports, nil
}

// ErrRateLimited is returned when the bridge reports a rate limit. It is
// terminal, not transient: a long run should stop and be resumed later rather
// than back off in a loop.
var ErrRateLimited = errors.New("bnet: rate limited")

// maxAttempts is how many times Get tries a request that failed in a way that
// might be transient. The bridge is a proxy to Blizzard and a single fetch
// occasionally just stalls; a run over hundreds of accounts sees that often
// enough that one stall must not end the pass. Two attempts, not more: a
// stalled request already cost a full timeout, and some routes stall reliably
// rather than transiently.
const maxAttempts = 2

// Get fetches one web-api path (without the "web-api/" prefix) and returns the
// raw body, retrying a stalled or refused request a couple of times.
//
// Payloads are not valid UTF-8 — map titles and lobby names carry raw cp949 —
// so invalid bytes are replaced before the body is handed back, otherwise
// encoding/json rejects the whole document over one bad map name.
func (c *Client) Get(ctx context.Context, path string) ([]byte, error) {
	var err error
	for attempt := 1; ; attempt++ {
		var body []byte
		body, err = c.get(ctx, path)
		if err == nil {
			return body, nil
		}
		// A rate limit or a real HTTP status is the answer, not a stall.
		if errors.Is(err, ErrRateLimited) || errors.Is(err, errNotRetryable) || attempt >= maxAttempts {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt) * time.Second):
		}
	}
}

// errNotRetryable marks a response the far side answered definitively, so
// there is nothing to gain from asking again.
var errNotRetryable = errors.New("bnet: not retryable")

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	c.pace()

	u := c.baseURL + "/web-api/" + strings.TrimLeft(path, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bnet: GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bnet: reading %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(path, resp.StatusCode, body)
	}
	return sanitizeUTF8(body), nil
}

// statusError turns a non-200 into the most actionable message available. 401
// in particular does not mean "bad credentials" — it means the game is running
// but is not logged in to Battle.net, which is the single most common cause of
// a refresh run failing.
func statusError(path string, code int, body []byte) error {
	switch code {
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: GET %s: not authorized — the game is running but not logged in to Battle.net", errNotRetryable, path)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: GET %s: stop and retry later rather than backing off in a loop", ErrRateLimited, path)
	case http.StatusNotFound:
		return fmt.Errorf("%w: GET %s: not found", errNotRetryable, path)
	}
	snippet := strings.TrimSpace(string(sanitizeUTF8(body)))
	if len(snippet) > 200 {
		snippet = snippet[:200]
	}
	// 5xx from the far side is worth one more try; a 4xx is an answer.
	if code >= 500 {
		return fmt.Errorf("bnet: GET %s: HTTP %d: %s", path, code, snippet)
	}
	return fmt.Errorf("%w: GET %s: HTTP %d: %s", errNotRetryable, path, code, snippet)
}

// pace blocks until minInterval has elapsed since the previous request.
func (c *Client) pace() {
	if c.minInterval <= 0 {
		return
	}
	if wait := c.minInterval - time.Since(c.last); wait > 0 && !c.last.IsZero() {
		time.Sleep(wait)
	}
	c.last = time.Now()
}

// sanitizeUTF8 replaces invalid UTF-8 bytes with the replacement rune, leaving
// valid input untouched.
func sanitizeUTF8(b []byte) []byte {
	if utf8.Valid(b) {
		return b
	}
	return []byte(strings.ToValidUTF8(string(b), "�"))
}

// Gateways returns per-gateway liveness. It is the cheapest route that exists
// and needs no toon, which makes it the right probe for "is this the web-api?".
func (c *Client) Gateways(ctx context.Context) (map[string]Gateway, error) {
	body, err := c.Get(ctx, "v1/gateways")
	if err != nil {
		return nil, err
	}
	var out map[string]Gateway
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("bnet: parsing v1/gateways: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("bnet: v1/gateways returned no gateways")
	}
	return out, nil
}

// Gateway is one entry of the v1/gateways response.
type Gateway struct {
	IsOfficial  bool   `json:"is_official"`
	Name        string `json:"name"`
	OnlineUsers int    `json:"online_users"`
	Region      string `json:"region"`
}

// ProfileByToon fetches the full profile for one toon on one gateway.
//
// The gateway must be right. A wrong (or simply unregistered) toon/gateway pair
// still returns HTTP 200, with aurora_id 0 and empty arrays — so the caller
// must check Profile.Exists, not just err.
func (c *Client) ProfileByToon(ctx context.Context, toon string, gateway int) (*Profile, error) {
	path := fmt.Sprintf("v2/aurora-profile-by-toon/%s/%d?request_flags=scr_profile", url.PathEscape(toon), gateway)
	body, err := c.Get(ctx, path)
	if err != nil {
		return nil, err
	}
	var p Profile
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("bnet: parsing profile for %s/%d: %w", toon, gateway, err)
	}
	return &p, nil
}

// FindProfile locates a toon whose gateway is unknown by probing gateways in
// GatewayOrder until one exists. Returns the profile and the gateway that
// answered.
//
// There is no by-aurora-id route — every variant 404s — so entering through a
// toon is the only way in, and probing is the only way to enter when the
// gateway was not recorded.
func (c *Client) FindProfile(ctx context.Context, toon string) (*Profile, int, error) {
	for _, gw := range GatewayOrder {
		p, err := c.ProfileByToon(ctx, toon, gw)
		if err != nil {
			return nil, 0, err
		}
		if p.Exists() {
			return p, gw, nil
		}
	}
	return nil, 0, nil
}

// Profile is the part of the v2/aurora-profile-by-toon payload the registry
// cares about. The full response also carries per-season matchmaking stats,
// lifetime aggregates, avatars and profiles, none of which are identity data.
type Profile struct {
	AuroraID    int64         `json:"aurora_id"`
	BattleTag   string        `json:"battle_tag"`
	CountryCode string        `json:"country_code"`
	Toons       []ProfileToon `json:"toons"`
	Replays     []Replay      `json:"replays"`
	GameResults []GameResult  `json:"game_results"`
}

// Exists reports whether the queried toon/gateway pair is a real account. An
// unknown pair is HTTP 200 with aurora_id 0, so this is the existence check.
func (p *Profile) Exists() bool { return p != nil && p.AuroraID != 0 }

// ProfileToon is one alt on an account. The account's toons span every gateway,
// not just the one queried, which is what makes a single profile call worth so
// much: query any one known toon and the whole account comes back.
type ProfileToon struct {
	Toon          string `json:"toon"`
	GatewayID     int    `json:"gateway_id"`
	GUID          int    `json:"guid"`
	GamesLastWeek int    `json:"games_last_week"`
}

// Replay is one entry of the profile's replays[] — the richest object in the
// payload, and the only place a private lobby's roster shows up. Note that
// Attributes.GameID is a per-account sequence number, not the global game id;
// the global id is Link.
type Replay struct {
	// Link discriminates ladder from custom: "MM-<uuid>" is matchmaking,
	// a bare numeric id is a private or custom lobby. Either way it is the
	// key v1/matchmaker-gameinfo-playerinfo wants — see GameKey.
	Link       string           `json:"link"`
	CreateTime int64            `json:"create_time"`
	Attributes ReplayAttributes `json:"attributes"`
}

// IsLadder reports whether this replay came from matchmaking.
func (r Replay) IsLadder() bool { return strings.HasPrefix(r.Link, "MM-") }

// GameKey is the key v1/matchmaker-gameinfo-playerinfo wants for this replay:
// the match GUID for ladder games, the numeric game id for private ones.
// Both are carried in Link, so this is Link — but the distinction matters,
// because passing the wrong one returns zero replays rather than an error.
func (r Replay) GameKey() string { return r.Link }

// ReplayAttributes are the string-typed attributes hung off a replay entry.
// Everything here arrives as a string, including the counts.
type ReplayAttributes struct {
	GameName    string `json:"game_name"` // the human-typed lobby title
	GameCreator string `json:"game_creator"`
	MapTitle    string `json:"map_title"`
	GameType    string `json:"game_type"`
	Humans      string `json:"replay_humans"`
	PlayerNames string `json:"replay_player_names"`
	PlayerRaces string `json:"replay_player_races"`
	PlayerTypes string `json:"replay_player_types"`
}

// HumanCount parses replay_humans. 1 means the opponents were computer players
// — solo build practice — and 2 is the 1v1 the discovery loop wants.
func (a ReplayAttributes) HumanCount() int {
	n, err := strconv.Atoi(strings.TrimSpace(a.Humans))
	if err != nil {
		return 0
	}
	return n
}

// Names splits replay_player_names into its per-slot toons.
func (a ReplayAttributes) Names() []string {
	return splitAttrList(a.PlayerNames)
}

// Races splits replay_player_races into its per-slot race codes.
func (a ReplayAttributes) Races() []string {
	return splitAttrList(a.PlayerRaces)
}

// splitAttrList splits a comma-or-newline separated attribute list, dropping
// blanks. The bridge is inconsistent about which separator it uses.
func splitAttrList(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// GameResult is one entry of game_results[]. It carries the full roster, so a
// single profile call yields many player observations — but it skips many
// custom games, so replays[] has to be read too.
type GameResult struct {
	GameID     string            `json:"game_id"`
	MatchGUID  string            `json:"match_guid"`
	GatewayID  int               `json:"gateway_id"`
	CreateTime string            `json:"create_time"`
	Players    []GameResultEntry `json:"players"`
}

// GameKey is the key v1/matchmaker-gameinfo-playerinfo wants for this game:
// the match GUID when there is one, the numeric game id otherwise.
func (g GameResult) GameKey() string {
	if g.MatchGUID != "" {
		return g.MatchGUID
	}
	return g.GameID
}

// GameResultEntry is one player slot in a game result.
type GameResultEntry struct {
	Toon   string `json:"toon"`
	Result string `json:"result"`
}

// GameInfo is the v1/matchmaker-gameinfo-playerinfo response: one replay upload
// per player, keyed by their aurora id in the path. The URLs are public GCS
// objects and need no auth.
type GameInfo struct {
	Replays []GameInfoReplay `json:"replays"`
}

// GameInfoReplay is one player's replay upload for a game. The object is a
// public GCS blob that needs no auth, and the uploading player's aurora id is
// embedded in its URL path.
type GameInfoReplay struct {
	URL        string           `json:"url"`
	MD5        string           `json:"md5"`
	CreateTime int64            `json:"create_time"`
	Attributes ReplayAttributes `json:"attributes"`
}

// uploaderRE picks the uploader's aurora id out of a replay URL, which looks
// like .../S1-replays/{auroraId}/{seq}/{md5}.replay.
var uploaderRE = regexp.MustCompile(`/S1-replays/(\d+)/`)

// UploaderAuroraID is the aurora id of the player who uploaded this copy, read
// out of the URL path. Zero when the URL does not carry one.
//
// This is the only way to learn an account id for a player whose toon has
// since been renamed: every player uploads their own copy, so a 1v1's two
// uploads name both accounts even when neither toon still resolves.
func (r GameInfoReplay) UploaderAuroraID() int64 {
	m := uploaderRE.FindStringSubmatch(r.URL)
	if m == nil {
		return 0
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// AuroraIDs returns the accounts that uploaded a copy of this game, in URL
// order and without duplicates.
func (g *GameInfo) AuroraIDs() []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, r := range g.Replays {
		id := r.UploaderAuroraID()
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// GameInfo fetches the download URLs for one game. Pass Replay.GameKey as the
// key: the match GUID for ladder games, the numeric game id for private ones.
func (c *Client) GameInfo(ctx context.Context, key string) (*GameInfo, error) {
	body, err := c.Get(ctx, "v1/matchmaker-gameinfo-playerinfo/"+url.PathEscape(key))
	if err != nil {
		return nil, err
	}
	var gi GameInfo
	if err := json.Unmarshal(body, &gi); err != nil {
		return nil, fmt.Errorf("bnet: parsing gameinfo for %s: %w", key, err)
	}
	return &gi, nil
}

// NameSearchHit is one row of a leaderboard name search.
type NameSearchHit struct {
	Toon      string `json:"name"` // the account name, confusingly keyed "name"
	BattleTag string `json:"battletag"`
	GatewayID int    `json:"gateway_id"`
	Rank      int    `json:"rank"`
	Points    int    `json:"points"`
}

// SearchLeaderboards are the leaderboard IDs worth searching by name. The
// route needs one, and this is the deliberately short list: name search is by
// far the slowest route on the bridge — it stalls for seconds where a profile
// fetch takes two — so every extra leaderboard is paid for on every account
// that has no known toon.
var SearchLeaderboards = []int{1}

// NameSearch searches a leaderboard by account name OR battle tag — it matches
// both, which is what makes it the only way into an account whose toons are
// unknown. There is no by-aurora-id route, so a battle tag search plus an
// aurora-id check on each hit is the sole remaining entrance.
//
// The hits are candidates, not answers: a Korean real name is shared by many
// players, so every hit must be resolved with ProfileByToon and accepted only
// when its aurora id is the one being looked for. The route also 500s on some
// queries, which is reported as an error rather than as no results.
func (c *Client) NameSearch(ctx context.Context, leaderboard int, query string) ([]NameSearchHit, error) {
	path := fmt.Sprintf("v1/leaderboard-name-search/%d/%s", leaderboard, url.PathEscape(query))
	body, err := c.Get(ctx, path)
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, nil
	}
	var hits []NameSearchHit
	if err := json.Unmarshal(body, &hits); err != nil {
		return nil, fmt.Errorf("bnet: parsing name search for %q: %w", query, err)
	}
	return hits, nil
}
