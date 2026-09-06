package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/marianogappa/scfingerprint/internal/bnet"
	"github.com/marianogappa/scfingerprint/internal/registry"
)

// runRefresh re-verifies every account against the live client. One profile
// call per account returns every toon it owns across every gateway, so a full
// pass costs one request per account and not one per toon.
//
// Toons are unioned, never replaced: a toon that has gone quiet is still a
// true fact about the account, and losing it would break the very lookups the
// map exists for.
func runRefresh(ctx context.Context, cfg config) error {
	reg, err := loadRegistry(cfg.registryPath)
	if err != nil {
		return err
	}
	client, err := connect(ctx, cfg)
	if err != nil {
		return err
	}

	// A full pass is slow — name search in particular stalls for seconds —
	// so the registry is checkpointed as it goes and a re-run resumes rather
	// than starting over. That makes the pass interruptible, which matters
	// when it is measured in tens of minutes.
	const checkpointEvery = 10

	var checked, updated, unreachable, failed, newToons, sinceCheckpoint int
	total := len(reg.Accounts)
	for i, acc := range reg.Accounts {
		if cfg.limit > 0 && checked >= cfg.limit {
			break
		}
		if !cfg.recheck && acc.LastVerified == today() {
			if cfg.verbose {
				fmt.Fprintf(os.Stderr, "  %s (%d): already verified today\n", acc.Name, acc.AuroraID)
			}
			continue
		}
		if sinceCheckpoint >= checkpointEvery {
			if err := checkpoint(cfg, reg); err != nil {
				return err
			}
			sinceCheckpoint = 0
		}
		// Counted before the work, not after: an account we fail to reach
		// still costs requests, so -limit has to bound attempts.
		checked++
		if cfg.verbose {
			// Indexed against the whole registry, not against the accounts
			// attempted, so a resumed run still shows where it is.
			fmt.Fprintf(os.Stderr, "  [%d/%d] %s (aurora %d)\n", i+1, total, acc.Name, acc.AuroraID)
		}
		toon, ok := acc.PrimaryToon()
		if !ok {
			// No known toon, and no by-aurora-id route to fall back on:
			// the only remaining entrance is a name search. It is the
			// slowest route on the bridge and usually finds nothing, so a
			// run that just wants to retry the reachable accounts can skip
			// it entirely.
			if cfg.skipGapFill {
				unreachable++
				continue
			}
			found, err := fillGap(ctx, client, acc, cfg)
			if err != nil {
				if errors.Is(err, bnet.ErrRateLimited) {
					return err
				}
				fmt.Fprintf(os.Stderr, "  %s: %v — entry kept, not re-verified\n", acc.Name, err)
				failed++
				continue
			}
			if found == nil {
				if cfg.verbose {
					fmt.Fprintf(os.Stderr, "  %s (%d): no toon known and no name search hit resolved to it\n", acc.Name, acc.AuroraID)
				}
				unreachable++
				continue
			}
			reg.Upsert(*found)
			updated++
			sinceCheckpoint++
			newToons += len(found.Toons)
			fmt.Fprintf(os.Stderr, "  %-14s entered via name search: %d toons for aurora %d\n", acc.Name, len(found.Toons), acc.AuroraID)
			continue
		}

		profile, gw, err := profileVia(ctx, client, toon)
		if err != nil {
			// One stalled request must not end a 150-account pass, and a
			// kept-but-stale entry is strictly better than a lost one. A
			// rate limit is different: it means stop.
			if errors.Is(err, bnet.ErrRateLimited) {
				return fmt.Errorf("refreshing %s (%s): %w", acc.Name, toon.Name, err)
			}
			fmt.Fprintf(os.Stderr, "  %s (%s): %v — entry kept, not re-verified\n", acc.Name, toon.Name, err)
			failed++
			continue
		}
		if profile == nil {
			fmt.Fprintf(os.Stderr, "  %s: toon %q no longer exists on any gateway — entry kept, not re-verified\n", acc.Name, toon.Name)
			unreachable++
			continue
		}
		if profile.AuroraID != acc.AuroraID {
			// The toon was renamed away and picked up by someone else.
			// Reporting beats silently rewriting the account's identity.
			fmt.Fprintf(os.Stderr, "  %s: toon %q now belongs to aurora %d, not %d — entry kept, needs manual review\n",
				acc.Name, toon.Name, profile.AuroraID, acc.AuroraID)
			continue
		}

		before := len(reg.Accounts[i].Toons)
		fresh := accountFromProfile(profile, acc.Name)
		reg.Upsert(fresh)
		after := len(mustAccount(reg, acc.AuroraID).Toons)
		newToons += after - before
		updated++
		sinceCheckpoint++
		if cfg.verbose || after != before {
			fmt.Fprintf(os.Stderr, "  %-14s %d toons (%+d) via %s on %s\n",
				acc.Name, after, after-before, toon.Name, registry.GatewayName(gw))
		}
	}

	if cfg.limit == 0 {
		reg.Generated = today()
	}
	fmt.Fprintf(os.Stderr, "refreshed %d/%d accounts, %d new toons, %d unreachable, %d request failures\n",
		updated, checked, newToons, unreachable, failed)
	return writeRegistry(cfg, reg)
}

// profileVia resolves a toon, trying its recorded gateway first. The gateway
// has to be right — a wrong pair is a 200 with aurora_id 0 — so this saves the
// four wasted requests a blind gateway probe would cost on every account whose
// gateway is already known.
func profileVia(ctx context.Context, client *bnet.Client, toon registry.Toon) (*bnet.Profile, int, error) {
	if toon.Gateway != 0 {
		p, err := client.ProfileByToon(ctx, toon.Name, toon.Gateway)
		if err != nil {
			return nil, 0, err
		}
		if p.Exists() {
			return p, toon.Gateway, nil
		}
	}
	return client.FindProfile(ctx, toon.Name)
}

// fillGap tries to enter an account that has no known toon. The only route in
// is v1/leaderboard-name-search, which matches battle tag as well as account
// name — so a Korean pro's real name can find their alts.
//
// Every hit is a candidate and nothing more: a real name is shared by many
// players, so each one is resolved with a profile call and accepted only when
// its aurora id is the one being looked for. Getting this wrong would attach a
// stranger's toons to a pro, which is worse than leaving the entry empty.
func fillGap(ctx context.Context, client *bnet.Client, acc registry.Account, cfg config) (*registry.Account, error) {
	queries := make([]string, 0, 2)
	if acc.BattleTag != "" {
		queries = append(queries, acc.BattleTag)
	}
	if acc.Name != "" && !strings.EqualFold(acc.Name, acc.BattleTag) {
		queries = append(queries, acc.Name)
	}

	// A Korean real name is shared by many ladder players, and resolving a
	// candidate costs a 300KB profile fetch. Cap the fan-out: if the account
	// is not in the first handful of hits it is not worth the requests.
	const maxCandidates = 8

	tried := map[string]bool{}
	for _, q := range queries {
		for _, lb := range bnet.SearchLeaderboards {
			hits, err := client.NameSearch(ctx, lb, q)
			if err != nil {
				// This route 500s on some queries; a dead end for one
				// account must not abort the whole pass.
				if cfg.verbose {
					fmt.Fprintf(os.Stderr, "  %s: name search %q on leaderboard %d failed: %v\n", acc.Name, q, lb, err)
				}
				continue
			}
			for _, h := range hits {
				if h.Toon == "" || tried[strings.ToLower(h.Toon)] {
					continue
				}
				if len(tried) >= maxCandidates {
					return nil, nil
				}
				tried[strings.ToLower(h.Toon)] = true
				p, err := client.ProfileByToon(ctx, h.Toon, h.GatewayID)
				if err != nil {
					continue
				}
				if !p.Exists() || p.AuroraID != acc.AuroraID {
					continue
				}
				out := accountFromProfile(p, acc.Name)
				out.Source = "bnet-webapi/name-search:" + q
				return &out, nil
			}
		}
	}
	return nil, nil
}

// accountFromProfile turns a live profile into a registry account verified now.
func accountFromProfile(p *bnet.Profile, name string) registry.Account {
	acc := registry.Account{
		Name:         name,
		AuroraID:     p.AuroraID,
		BattleTag:    p.BattleTag,
		Country:      p.CountryCode,
		Source:       "bnet-webapi",
		LastVerified: today(),
	}
	for _, t := range p.Toons {
		acc.Toons = append(acc.Toons, registry.Toon{Name: t.Toon, Gateway: t.GatewayID, GamesLastWeek: t.GamesLastWeek})
	}
	return acc
}

// mustAccount reads back an account the caller has just upserted.
func mustAccount(reg *registry.Registry, aurora int64) registry.Account {
	a, _ := reg.LookupAurora(aurora)
	return a
}
