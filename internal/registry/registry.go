// Package registry holds the built-in progamer identity map: pro name ↔
// aurora id ↔ battle tag ↔ toons, per gateway. It is embedded like the model
// and the dataset, so a lookup costs nothing and works offline.
//
// # This is not evidence
//
// The registry is a name lookup and it is wrong by nature. Accounts get
// shared, sold, lent to a friend, renamed and abandoned, and the snapshot goes
// stale the moment it ships. It is a second, independent opinion — cheap and
// instant — and it must never outrank a fingerprint.
//
// The disagreements are the useful part. When the fingerprint says one player
// and the registry says another, that is a signal worth surfacing (account
// sharing, a sold account, a stale entry), not a discrepancy to smooth over.
// Every account carries Source and LastVerified so staleness stays visible.
package registry

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed registry.json
var registryJSON []byte

// Toon is one account name on one gateway.
type Toon struct {
	Name    string `json:"toon"`
	Gateway int    `json:"gateway"`

	// GamesLastWeek is how active this toon was at LastVerified time. Zero
	// on a toon that was never seen live, so it distinguishes "idle" from
	// "unknown" only in combination with the account's Source.
	GamesLastWeek int `json:"games_last_week,omitempty"`
}

// Account is one Battle.net account: one aurora id, one battle tag, and every
// toon it owns across every gateway.
//
// A player usually has several accounts, so Name is not unique across the
// registry — LookupName returns all of them.
type Account struct {
	// Name is the progamer's handle, e.g. "Queen". Empty for an account
	// that is known to exist but has not been attributed to anyone.
	Name string `json:"name,omitempty"`

	AuroraID  int64  `json:"aurora_id"`
	BattleTag string `json:"battle_tag,omitempty"`
	Country   string `json:"country,omitempty"`

	// Toons spans every gateway the account has played on, not just the one
	// it was looked up by.
	Toons []Toon `json:"toons"`

	// Source records where this entry came from, e.g. "cwal-2026-08-08" or
	// "bnet-webapi".
	Source string `json:"source"`

	// LastVerified is the YYYY-MM-DD on which the entry was last confirmed
	// against a live source.
	LastVerified string `json:"last_verified"`
}

// PrimaryToon returns the account's most active toon, falling back to the
// first one. It is the toon to use when a single name has to stand in for the
// whole account.
func (a Account) PrimaryToon() (Toon, bool) {
	if len(a.Toons) == 0 {
		return Toon{}, false
	}
	best := a.Toons[0]
	for _, t := range a.Toons[1:] {
		if t.GamesLastWeek > best.GamesLastWeek {
			best = t
		}
	}
	return best, true
}

// Registry is the loaded identity map, with lookup indexes.
type Registry struct {
	Version   int       `json:"version"`
	Generated string    `json:"generated"`
	Accounts  []Account `json:"accounts"`

	byToon   map[string]int   // lower(toon) → index into Accounts
	byAurora map[int64]int    // aurora id → index into Accounts
	byName   map[string][]int // lower(name) → indexes into Accounts
}

// LoadEmbedded parses the registry shipped with this build.
func LoadEmbedded() (*Registry, error) {
	return Parse(registryJSON)
}

// Parse reads a registry document and builds its lookup indexes.
func Parse(data []byte) (*Registry, error) {
	var r Registry
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("registry: parsing: %w", err)
	}
	if err := r.index(); err != nil {
		return nil, err
	}
	return &r, nil
}

// index builds the lookup maps and rejects a document that cannot be indexed
// unambiguously. Two accounts sharing an aurora id is a real curation bug, not
// something to resolve silently.
func (r *Registry) index() error {
	r.byToon = make(map[string]int, len(r.Accounts)*2)
	r.byAurora = make(map[int64]int, len(r.Accounts))
	r.byName = make(map[string][]int, len(r.Accounts))
	for i, a := range r.Accounts {
		if _, dup := r.byAurora[a.AuroraID]; dup {
			return fmt.Errorf("registry: aurora id %d appears on two accounts", a.AuroraID)
		}
		r.byAurora[a.AuroraID] = i
		if a.Name != "" {
			key := strings.ToLower(a.Name)
			r.byName[key] = append(r.byName[key], i)
		}
		for _, t := range a.Toons {
			// A toon name is unique per gateway, not globally, so a
			// collision across gateways is possible in principle. First
			// one wins and callers who care pass a gateway.
			key := strings.ToLower(t.Name)
			if _, dup := r.byToon[key]; !dup {
				r.byToon[key] = i
			}
		}
	}
	return nil
}

// Len returns the number of accounts in the registry.
func (r *Registry) Len() int { return len(r.Accounts) }

// LookupToon finds the account owning a toon, on any gateway. Toon names are
// matched case-insensitively.
func (r *Registry) LookupToon(toon string) (Account, bool) {
	i, ok := r.byToon[strings.ToLower(strings.TrimSpace(toon))]
	if !ok {
		return Account{}, false
	}
	return r.Accounts[i], true
}

// LookupToonOnGateway finds the account owning a toon on one specific gateway.
// Use it when the gateway is known: toon names are only unique within a
// gateway, so this is the exact question and LookupToon is the loose one.
func (r *Registry) LookupToonOnGateway(toon string, gateway int) (Account, bool) {
	want := strings.ToLower(strings.TrimSpace(toon))
	for _, a := range r.Accounts {
		for _, t := range a.Toons {
			if t.Gateway == gateway && strings.ToLower(t.Name) == want {
				return a, true
			}
		}
	}
	return Account{}, false
}

// LookupAurora finds an account by its aurora id.
func (r *Registry) LookupAurora(id int64) (Account, bool) {
	i, ok := r.byAurora[id]
	if !ok {
		return Account{}, false
	}
	return r.Accounts[i], true
}

// LookupName returns every account attributed to a progamer name, matched
// case-insensitively. Pros routinely hold several accounts, so the plural is
// the normal case, not an edge case.
func (r *Registry) LookupName(name string) []Account {
	idxs := r.byName[strings.ToLower(strings.TrimSpace(name))]
	out := make([]Account, 0, len(idxs))
	for _, i := range idxs {
		out = append(out, r.Accounts[i])
	}
	return out
}

// Names lists every progamer name in the registry, sorted, without duplicates.
func (r *Registry) Names() []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range r.Accounts {
		if a.Name == "" || seen[a.Name] {
			continue
		}
		seen[a.Name] = true
		out = append(out, a.Name)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// ToonCount returns the total number of toons across all accounts.
func (r *Registry) ToonCount() int {
	n := 0
	for _, a := range r.Accounts {
		n += len(a.Toons)
	}
	return n
}

// Marshal renders the registry as the canonical on-disk document: accounts
// sorted by name then aurora id, toons sorted by gateway then name, so a
// refresh run produces a reviewable diff rather than a reshuffle.
func (r *Registry) Marshal() ([]byte, error) {
	out := Registry{Version: r.Version, Generated: r.Generated, Accounts: append([]Account(nil), r.Accounts...)}
	for i := range out.Accounts {
		// An account with no known toon is a real state — there is no
		// by-aurora-id route, so it cannot be entered — but it serializes
		// as [] rather than null so consumers need no null check.
		toons := make([]Toon, 0, len(out.Accounts[i].Toons))
		toons = append(toons, out.Accounts[i].Toons...)
		sort.SliceStable(toons, func(a, b int) bool {
			if toons[a].Gateway != toons[b].Gateway {
				return toons[a].Gateway < toons[b].Gateway
			}
			return toons[a].Name < toons[b].Name
		})
		out.Accounts[i].Toons = toons
	}
	sort.SliceStable(out.Accounts, func(a, b int) bool {
		na, nb := strings.ToLower(out.Accounts[a].Name), strings.ToLower(out.Accounts[b].Name)
		// Unattributed accounts sort last rather than first, so the readable
		// part of the file stays at the top. Two unattributed accounts fall
		// through to the aurora tiebreak: returning true for both orders
		// would not be a valid ordering.
		if (na == "") != (nb == "") {
			return nb == ""
		}
		if na != nb {
			return na < nb
		}
		return out.Accounts[a].AuroraID < out.Accounts[b].AuroraID
	})
	data, err := json.MarshalIndent(out, "", " ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// Upsert merges an account into the registry, replacing the entry with the
// same aurora id if there is one. Toons are unioned rather than replaced so a
// refresh never loses a toon that has simply gone quiet, and a non-empty Name
// on either side is preserved.
func (r *Registry) Upsert(a Account) {
	i, ok := r.byAurora[a.AuroraID]
	if !ok {
		r.Accounts = append(r.Accounts, a)
		_ = r.index()
		return
	}
	old := r.Accounts[i]
	if a.Name == "" {
		a.Name = old.Name
	}
	if a.BattleTag == "" {
		a.BattleTag = old.BattleTag
	}
	if a.Country == "" {
		a.Country = old.Country
	}
	a.Toons = mergeToons(old.Toons, a.Toons)
	r.Accounts[i] = a
	_ = r.index()
}

// mergeToons unions two toon lists, keyed by gateway+name, with the incoming
// entry winning on conflict.
func mergeToons(old, fresh []Toon) []Toon {
	key := func(t Toon) string { return fmt.Sprintf("%d/%s", t.Gateway, strings.ToLower(t.Name)) }
	pos := make(map[string]int, len(old))
	out := make([]Toon, 0, len(old)+len(fresh))
	for _, t := range old {
		pos[key(t)] = len(out)
		out = append(out, t)
	}
	for _, t := range fresh {
		if i, ok := pos[key(t)]; ok {
			out[i] = t
			continue
		}
		pos[key(t)] = len(out)
		out = append(out, t)
	}
	return out
}

// GatewayNames maps the numeric gateway IDs the web-api uses to their names.
var GatewayNames = map[int]string{
	10: "U.S. West",
	11: "U.S. East",
	20: "Europe",
	30: "Korea",
	45: "Asia",
}

// GatewayOrder lists gateway IDs in a stable order for output.
var GatewayOrder = []int{30, 20, 11, 10, 45}

// GatewayName renders a numeric gateway ID. An unrecognised ID comes back as
// its number rather than as an empty string, so output never loses
// information; zero means the gateway was never recorded, which is a real
// state for a toon learned from a game rather than from a profile.
func GatewayName(gateway int) string {
	if gateway == 0 {
		return "gateway unknown"
	}
	if n, ok := GatewayNames[gateway]; ok {
		return n
	}
	return fmt.Sprintf("gateway %d", gateway)
}

// UnsetName clears an account's name in place. Upsert deliberately keeps a
// known name when the incoming record has none, so removing an attribution
// needs its own operation — otherwise a refuted name could never be taken off.
func (r *Registry) UnsetName(aurora int64) {
	i, ok := r.byAurora[aurora]
	if !ok {
		return
	}
	r.Accounts[i].Name = ""
	_ = r.index()
}
