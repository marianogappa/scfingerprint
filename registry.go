package scfingerprint

import (
	"fmt"
	"strings"

	"github.com/marianogappa/scfingerprint/internal/registry"
)

// Registry is the built-in progamer identity map: pro name ↔ aurora id ↔
// battle tag ↔ toons, per gateway. Load it with [BuiltinRegistry].
//
// It answers "who owns this account name?" instantly, with no replay and no
// scoring. That makes it useful for annotating a roster, finding every alt of
// a player before harvesting their games, and cross-checking a fingerprint.
//
// # It is not evidence
//
// A registry hit is a name lookup, and names are the exact thing fingerprinting
// exists to see past. Accounts get shared, sold, lent to a friend and renamed,
// and any snapshot is stale the day after it ships. Treat a registry answer as
// a second, independent opinion — never as a score, and never as a tiebreak.
//
// Where the registry and a fingerprint disagree, the disagreement is the
// finding: it points at a shared or sold account, or an entry that has gone
// stale. Surface it; do not reconcile it.
type Registry struct {
	inner *registry.Registry
}

// RegistryToon is one account name on one gateway.
type RegistryToon struct {
	Toon        string `json:"toon"`
	Gateway     int    `json:"gateway"`                // the numeric gateway ID, e.g. 30
	GatewayName string `json:"gateway_name,omitempty"` // e.g. "Korea"

	// GamesLastWeek is how active this toon was when the entry was last
	// verified — the cheapest available signal for which alt is live.
	GamesLastWeek int `json:"games_last_week,omitempty"`
}

// RegistryAccount is one Battle.net account: an aurora id, a battle tag, and
// every toon it owns across every gateway.
//
// A player normally holds several accounts, so Name is not unique — see
// [Registry.LookupName].
type RegistryAccount struct {
	Name      string `json:"name,omitempty"` // progamer handle; empty when unattributed
	AuroraID  int64  `json:"aurora_id"`
	BattleTag string `json:"battle_tag,omitempty"`
	Country   string `json:"country,omitempty"`

	// Toons spans every gateway this account has played on, not only the
	// one it was looked up by. Korean pros are routinely most active off
	// Korea, so filtering these by gateway hides live accounts.
	Toons []RegistryToon `json:"toons"`

	Source       string `json:"source"`        // where the entry came from
	LastVerified string `json:"last_verified"` // YYYY-MM-DD it was last confirmed
}

// PrimaryToon returns the account's most active toon, falling back to the
// first. Use it when one name has to stand in for the whole account.
func (a RegistryAccount) PrimaryToon() (RegistryToon, bool) {
	if len(a.Toons) == 0 {
		return RegistryToon{}, false
	}
	best := a.Toons[0]
	for _, t := range a.Toons[1:] {
		if t.GamesLastWeek > best.GamesLastWeek {
			best = t
		}
	}
	return best, true
}

// RegistryOpinion is what the identity map says about an observed player, as
// attached to a [MatchResult] when [WithRegistry] is passed.
//
// It is deliberately a separate field and it never touches Z, Cosine or
// SearchFPR: the fingerprint verdict stays exactly what it would have been
// without a registry. Agrees is the only per-candidate field; everything else
// describes the observed player and repeats across the result list.
type RegistryOpinion struct {
	Name         string `json:"name"`      // who the registry says the observed player is
	Toon         string `json:"toon"`      // the account name it was looked up by
	AuroraID     int64  `json:"aurora_id"` // the account the toon belongs to
	BattleTag    string `json:"battle_tag,omitempty"`
	Source       string `json:"source,omitempty"`
	LastVerified string `json:"last_verified,omitempty"`

	// Agrees reports whether the registry's name matches THIS candidate's
	// label. A false on the top candidate is the interesting case: it means
	// the fingerprint and the name disagree about who played this game.
	Agrees bool `json:"agrees"`

	// Ambiguous is true when the games in this probe resolved to more than
	// one registry account, i.e. the probe mixes accounts. The reported
	// account is then only one of them.
	Ambiguous bool `json:"ambiguous,omitempty"`
}

// BuiltinRegistry loads the identity map shipped with this build.
func BuiltinRegistry() (*Registry, error) {
	r, err := registry.LoadEmbedded()
	if err != nil {
		return nil, fmt.Errorf("scfingerprint: loading built-in registry: %w", err)
	}
	return &Registry{inner: r}, nil
}

// Version reports the registry document's schema version.
func (r *Registry) Version() int { return r.inner.Version }

// Generated reports the YYYY-MM-DD the shipped registry was built.
func (r *Registry) Generated() string { return r.inner.Generated }

// Len returns the number of accounts in the registry.
func (r *Registry) Len() int { return r.inner.Len() }

// ToonCount returns the total number of toons across all accounts.
func (r *Registry) ToonCount() int { return r.inner.ToonCount() }

// Names lists every progamer name in the registry, sorted.
func (r *Registry) Names() []string { return r.inner.Names() }

// Accounts returns every account in the registry.
func (r *Registry) Accounts() []RegistryAccount {
	out := make([]RegistryAccount, 0, r.inner.Len())
	for _, a := range r.inner.Accounts {
		out = append(out, publicAccount(a))
	}
	return out
}

// LookupToon finds the account owning a toon, on any gateway, matched
// case-insensitively. This is the zero-replay identity question.
func (r *Registry) LookupToon(toon string) (RegistryAccount, bool) {
	a, ok := r.inner.LookupToon(toon)
	if !ok {
		return RegistryAccount{}, false
	}
	return publicAccount(a), true
}

// LookupToonOnGateway finds the account owning a toon on one specific gateway.
// Toon names are only unique within a gateway, so prefer this when the gateway
// is known.
func (r *Registry) LookupToonOnGateway(toon string, gateway int) (RegistryAccount, bool) {
	a, ok := r.inner.LookupToonOnGateway(toon, gateway)
	if !ok {
		return RegistryAccount{}, false
	}
	return publicAccount(a), true
}

// LookupAurora finds an account by its aurora id.
func (r *Registry) LookupAurora(id int64) (RegistryAccount, bool) {
	a, ok := r.inner.LookupAurora(id)
	if !ok {
		return RegistryAccount{}, false
	}
	return publicAccount(a), true
}

// LookupName returns every account attributed to a progamer name, matched
// case-insensitively. Pros hold several accounts, so more than one result is
// the normal case.
func (r *Registry) LookupName(name string) []RegistryAccount {
	accs := r.inner.LookupName(name)
	out := make([]RegistryAccount, 0, len(accs))
	for _, a := range accs {
		out = append(out, publicAccount(a))
	}
	return out
}

// GatewayName renders a numeric gateway ID, e.g. 30 → "Korea". Unknown IDs
// come back as their number.
func GatewayName(gateway int) string { return registry.GatewayName(gateway) }

func publicAccount(a registry.Account) RegistryAccount {
	toons := make([]RegistryToon, 0, len(a.Toons))
	for _, t := range a.Toons {
		toons = append(toons, RegistryToon{
			Toon:          t.Name,
			Gateway:       t.Gateway,
			GatewayName:   registry.GatewayName(t.Gateway),
			GamesLastWeek: t.GamesLastWeek,
		})
	}
	return RegistryAccount{
		Name:         a.Name,
		AuroraID:     a.AuroraID,
		BattleTag:    a.BattleTag,
		Country:      a.Country,
		Toons:        toons,
		Source:       a.Source,
		LastVerified: a.LastVerified,
	}
}

// registryOpinion resolves the toons observed across a probe's games to a
// single registry account.
//
// Returns nil when no game carried a toon or none of them is in the registry —
// silence is the honest answer, since most accounts are not progamer accounts.
// When the games resolve to more than one account the first is reported with
// Ambiguous set, because a probe that mixes accounts is itself worth seeing.
func registryOpinion(games []PlayerGame, reg *Registry) *RegistryOpinion {
	if reg == nil {
		return nil
	}
	var first *RegistryOpinion
	ambiguous := false
	for _, g := range games {
		toon := gameToon(g)
		if toon == "" {
			continue
		}
		acc, ok := reg.LookupToon(toon)
		if !ok {
			continue
		}
		if first == nil {
			first = &RegistryOpinion{
				Name:         acc.Name,
				Toon:         toon,
				AuroraID:     acc.AuroraID,
				BattleTag:    acc.BattleTag,
				Source:       acc.Source,
				LastVerified: acc.LastVerified,
			}
			continue
		}
		if acc.AuroraID != first.AuroraID {
			ambiguous = true
		}
	}
	if first == nil {
		return nil
	}
	first.Ambiguous = ambiguous
	return first
}

// gameToon reports the account name a game was played on: the explicit Toon
// when the caller set one, otherwise the name in the replay slot.
//
// The slot is read straight off the header rather than through Extract: the
// name is the only thing wanted here, and re-extracting features to get it
// would double the cost of every Match that passes a registry.
func gameToon(g PlayerGame) string {
	if g.Toon != "" {
		return g.Toon
	}
	if g.Replay == nil || g.Replay.Header == nil {
		return ""
	}
	for _, p := range g.Replay.Header.Players {
		if p.ID == g.PlayerID && !p.Observer {
			return p.Name
		}
	}
	return ""
}

// agreesWith reports whether the registry's name is this candidate's label.
func (o *RegistryOpinion) agreesWith(label string) bool {
	return o != nil && o.Name != "" && strings.EqualFold(o.Name, label)
}

// forLabel returns a copy of the opinion with Agrees set for one candidate, so
// every MatchResult can be read on its own without the caller re-deriving
// agreement. Nil in, nil out.
func (o *RegistryOpinion) forLabel(label string) *RegistryOpinion {
	if o == nil {
		return nil
	}
	c := *o
	c.Agrees = o.agreesWith(label)
	return &c
}
