package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/marianogappa/scfingerprint"
	"github.com/marianogappa/scfingerprint/internal/bnet"
	"github.com/marianogappa/scfingerprint/internal/dataset"
	"github.com/marianogappa/scfingerprint/internal/registry"
)

// unknownToon is an account name the loop met but could not put a name to.
// These are the output that needs a human, so each one records enough context
// to act on without re-running the loop.
type unknownToon struct {
	Toon      string  `json:"toon"`
	AuroraID  int64   `json:"aurora_id,omitempty"`
	BattleTag string  `json:"battle_tag,omitempty"`
	Gateway   int     `json:"gateway,omitempty"`
	SeenWith  string  `json:"seen_with"` // the anchor whose game it turned up in
	GameKey   string  `json:"game_key"`  // the game it was seen in
	Reason    string  `json:"reason"`    // why it stayed unnamed
	TopGuess  string  `json:"top_guess,omitempty"`
	TopZ      float64 `json:"top_z,omitempty"`
}

// discoverer carries the state of one discovery pass.
type discoverer struct {
	cfg       config
	client    *bnet.Client
	reg       *registry.Registry
	db        *scfingerprint.Dataset
	replayDir string

	unknowns   []unknownToon
	anchored   int
	candidates int
	named      int
}

func runDiscover(ctx context.Context, cfg config) error {
	reg, err := loadRegistry(cfg.registryPath)
	if err != nil {
		return err
	}
	client, err := connect(ctx, cfg)
	if err != nil {
		return err
	}
	// "high" and above: this decides what gets written into shipped data, so
	// candidate-tier entries have no business voting on it.
	db, err := scfingerprint.BuiltinDataset(dataset.ConfidenceHigh)
	if err != nil {
		return err
	}

	replayDir := cfg.replayDir
	if replayDir == "" {
		replayDir, err = os.MkdirTemp("", "scfingerprint-discover-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(replayDir) }()
	} else if err := os.MkdirAll(replayDir, 0o755); err != nil {
		return err
	}

	d := &discoverer{cfg: cfg, client: client, reg: reg, db: db, replayDir: replayDir}

	// Snapshot the anchors: the pass appends accounts as it names them, and
	// a freshly discovered account has not been refreshed yet, so it is not
	// a sound anchor within the same run.
	for _, anchor := range append([]registry.Account(nil), reg.Accounts...) {
		if cfg.limit > 0 && d.anchored >= cfg.limit {
			break
		}
		if err := d.anchorOn(ctx, anchor); err != nil {
			return err
		}
	}

	fmt.Fprintf(os.Stderr, "anchored on %d accounts, %d unknown rivals seen, %d named, %d queued for manual review\n",
		d.anchored, d.candidates, d.named, len(d.unknowns))
	if err := d.writeUnknowns(); err != nil {
		return err
	}
	if d.named == 0 {
		return nil
	}
	reg.Generated = today()
	return writeRegistry(cfg, reg)
}

// anchorOn pulls one known account's recent games and considers every unknown
// rival in them.
func (d *discoverer) anchorOn(ctx context.Context, anchor registry.Account) error {
	toon, ok := anchor.PrimaryToon()
	if !ok {
		return nil // no toon to enter through, and no by-aurora-id route
	}
	profile, _, err := profileVia(ctx, d.client, toon)
	if err != nil {
		if errors.Is(err, bnet.ErrRateLimited) {
			return fmt.Errorf("anchoring on %s (%s): %w", anchor.Name, toon.Name, err)
		}
		fmt.Fprintf(os.Stderr, "  %s (%s): %v — skipped as an anchor\n", anchor.Name, toon.Name, err)
		return nil
	}
	if profile == nil {
		return nil
	}
	d.anchored++

	own := map[string]bool{}
	for _, t := range profile.Toons {
		own[strings.ToLower(t.Toon)] = true
	}

	for _, rep := range profile.Replays {
		// Ladder games only ever pair us with people the ladder already
		// knows about; private 1v1s are where new pros turn up.
		if rep.IsLadder() || rep.Attributes.HumanCount() != 2 {
			continue
		}
		rival := rivalToon(rep.Attributes.Names(), own)
		if rival == "" {
			continue
		}
		if _, known := d.reg.LookupToon(rival); known {
			continue
		}
		d.candidates++
		if err := d.consider(ctx, anchor, rep, rival); err != nil {
			return err
		}
	}
	return nil
}

// consider tries to put a name to one unknown rival: download the game, ask
// the fingerprint who it was, and on a confident answer onboard the whole
// account. Anything short of confident is queued for a human instead.
//
// The fingerprint does the naming, which is the right way round — the map is
// built by identification rather than trusted in place of it.
func (d *discoverer) consider(ctx context.Context, anchor registry.Account, rep bnet.Replay, rival string) error {
	u := unknownToon{Toon: rival, SeenWith: anchor.Name, GameKey: rep.GameKey()}

	game, err := downloadReplay(ctx, d.client, rep.GameKey(), d.replayDir)
	if err != nil {
		return d.queue(u, "replay download failed: "+err.Error())
	}
	// The uploads name both accounts even when neither toon still resolves,
	// so record the rival's account before anything else can fail.
	u.AuroraID = otherAurora(game.auroras, anchor.AuroraID)
	if game.path == "" {
		return d.queue(u, "no replay upload available for this game")
	}

	res, err := matchToon(game.path, rival, d.db)
	if err != nil {
		return d.queue(u, "fingerprinting failed: "+err.Error())
	}
	if len(res) > 0 {
		u.TopGuess, u.TopZ = res[0].Label, res[0].Z
	}
	if !confident(res, d.cfg) {
		// Worth recording what we can even when we cannot name them: a
		// battle tag and gateway are what a human needs to pick this up.
		if p, gw, err := d.client.FindProfile(ctx, rival); err == nil && p.Exists() {
			u.BattleTag, u.Gateway = p.BattleTag, gw
			u.AuroraID = p.AuroraID
		}
		return d.queue(u, "no confident fingerprint match")
	}
	label := res[0].Label

	// Confident. One profile call now onboards the whole account — every toon
	// it owns, on every gateway — but only if the toon still resolves.
	p, gw, err := d.client.FindProfile(ctx, rival)
	if err != nil {
		if errors.Is(err, bnet.ErrRateLimited) {
			return fmt.Errorf("expanding %s: %w", rival, err)
		}
		return d.queue(u, fmt.Sprintf("matched %s but expanding the account failed: %v", label, err))
	}

	acc := registry.Account{Name: label, LastVerified: today()}
	switch {
	case p.Exists():
		acc = accountFromProfile(p, label)
	case u.AuroraID != 0:
		// The toon has been renamed away since the game, so there is no
		// profile to expand — but the upload still gave us the account id,
		// which is what the map is keyed on. Register that much and record
		// the toon as last seen rather than current.
		acc.AuroraID = u.AuroraID
		// Gateway 0, not a guess: the profile payload's replay entries do
		// not carry one, and inventing the anchor's would be wrong as often
		// as right.
		acc.Toons = []registry.Toon{{Name: rival}}
	default:
		return d.queue(u, "matched "+label+" but neither the toon nor the game named an account")
	}
	acc.Source = fmt.Sprintf("discovered-via-%s@%s", anchor.Name, rep.GameKey())

	// The rival TOON was unknown, but its ACCOUNT may not be — we may simply
	// never have seen this alt. If that account is already attributed to
	// somebody else then two independent claims disagree about who owns it,
	// and quietly renaming it would destroy exactly the signal worth seeing.
	if known, ok := d.reg.LookupAurora(acc.AuroraID); ok && known.Name != "" && !strings.EqualFold(known.Name, label) {
		reason := fmt.Sprintf("aurora %d is already registered to %s but fingerprints as %s — needs manual review",
			acc.AuroraID, known.Name, label)
		fmt.Fprintf(os.Stderr, "  ! %s\n", reason)
		return d.queue(u, reason)
	}

	d.reg.Upsert(acc)
	d.named++
	where := "toon since renamed"
	if p.Exists() {
		where = registry.GatewayName(gw)
	}
	fmt.Fprintf(os.Stderr, "  + %s: %s (%s) → aurora %d, %d toons (z=%.2f, in %s's private game)\n",
		label, rival, where, acc.AuroraID, len(acc.Toons), res[0].Z, anchor.Name)
	return nil
}

// otherAurora returns the account in ids that is not mine — the rival's, in a
// two-player game. Zero when the game named no other account, which happens
// when only one player's upload survived.
func otherAurora(ids []int64, mine int64) int64 {
	for _, id := range ids {
		if id != mine && id != 0 {
			return id
		}
	}
	return 0
}

// queue records a rival the loop could not name. It always returns nil: an
// unnamed candidate is a normal outcome, not a failure of the pass.
func (d *discoverer) queue(u unknownToon, reason string) error {
	u.Reason = reason
	d.unknowns = append(d.unknowns, u)
	return nil
}

func (d *discoverer) writeUnknowns() error {
	if d.cfg.unknownsPath == "" || len(d.unknowns) == 0 {
		return nil
	}
	data, err := json.MarshalIndent(d.unknowns, "", " ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(d.cfg.unknownsPath, append(data, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %d unidentified toons to %s\n", len(d.unknowns), d.cfg.unknownsPath)
	return nil
}

// rivalToon returns the one name in a 1v1 roster that is not the anchor's.
// Empty when the roster is not a clean two-player game, when both slots belong
// to the anchor (a pro practising against their own alt), or when neither does
// — a game the anchor is not in tells us nothing about which stranger is which.
func rivalToon(names []string, own map[string]bool) string {
	if len(names) != 2 {
		return ""
	}
	var out string
	for _, n := range names {
		if own[strings.ToLower(n)] {
			continue
		}
		if out != "" {
			return ""
		}
		out = n
	}
	return out
}

// Gates for attributing an account from a single game, in the same terms the
// CLI reports: a family-wise rate at or below the "worth following up" bar,
// plus a decisive gap to the runner-up.
//
// The discovery loop only ever has ONE game — a private practice game — so it
// cannot reach the 3-game evidence the CLI needs to call something strong.
// It substitutes margin for volume, which is the same trade the CLI makes when
// it upgrades a lead-grade rate with a decisive margin. Demanding the strong
// rate outright would need the 1-in-10,000 operating point, which
// docs/METHODOLOGY.md reports as an estimate only — so the loop would either
// never fire or would rest its writes on the least certified number available.
//
// Every write records the anchor and game it came from, so an attribution can
// be audited or reverted; a near miss is queued for a human instead.
const (
	discoverMaxFPR    = 0.10
	discoverMinMargin = 1.5
)

// confident decides whether a single-game match is good enough to attribute an
// account. This writes into shipped data, so a bare hit is never enough — see
// discoverMaxFPR.
func confident(res []scfingerprint.MatchResult, cfg config) bool {
	if len(res) == 0 || res[0].SearchFPR > cfg.maxFPR {
		return false
	}
	if len(res) == 1 {
		return true
	}
	return res[0].Z-res[1].Z >= cfg.minMargin
}
