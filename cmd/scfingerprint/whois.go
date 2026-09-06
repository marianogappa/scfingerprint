package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/marianogappa/scfingerprint"
)

// whoisAccount is one account in a whois answer: the registry's record plus
// whether that player also has a fingerprint in the built-in catalog.
type whoisAccount struct {
	Name         string                       `json:"name,omitempty"`
	AuroraID     int64                        `json:"aurora_id"`
	BattleTag    string                       `json:"battle_tag,omitempty"`
	Country      string                       `json:"country,omitempty"`
	Toons        []scfingerprint.RegistryToon `json:"toons"`
	Source       string                       `json:"source,omitempty"`
	LastVerified string                       `json:"last_verified,omitempty"`

	// InCatalog reports whether this player also has a fingerprint, i.e.
	// whether `scfingerprint match` could confirm the registry's answer.
	InCatalog         bool   `json:"in_catalog"`
	CatalogID         string `json:"catalog_id,omitempty"`
	CatalogConfidence string `json:"catalog_confidence,omitempty"`
	CatalogGames      int    `json:"catalog_games,omitempty"`
	Liquipedia        string `json:"liquipedia,omitempty"`
}

// whoisReport is the machine shape of a whois run.
type whoisReport struct {
	Query     string         `json:"query"`
	MatchedBy string         `json:"matched_by,omitempty"` // toon, name or aurora_id
	Found     bool           `json:"found"`
	Accounts  []whoisAccount `json:"accounts"`

	// Registry provenance, so a stale answer is visible without a second call.
	RegistryGenerated string `json:"registry_generated,omitempty"`
	RegistryAccounts  int    `json:"registry_accounts,omitempty"`
}

func cmdWhois(args []string) int {
	fs := flag.NewFlagSet("whois", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON on stdout even when it is a terminal")
	noColor := fs.Bool("no-color", false, "disable colour on stderr")
	positional, err := parseAll(fs, args)
	if err != nil {
		return exitError
	}
	if len(positional) != 1 {
		return fail(fmt.Errorf("whois takes exactly one account name, progamer name or aurora id"))
	}
	query := positional[0]
	pal := newPalette(colorEnabled(*noColor))

	reg, err := scfingerprint.BuiltinRegistry()
	if err != nil {
		return fail(err)
	}
	accounts, matchedBy := resolveWhois(reg, query)

	report := whoisReport{
		Query:             query,
		MatchedBy:         matchedBy,
		Found:             len(accounts) > 0,
		RegistryGenerated: reg.Generated(),
		RegistryAccounts:  reg.Len(),
		// Always a list, never null: a script reading this should be able to
		// iterate the result without a null check.
		Accounts: []whoisAccount{},
	}
	for _, a := range accounts {
		report.Accounts = append(report.Accounts, annotateWithCatalog(a))
	}

	renderWhois(os.Stderr, pal, report)
	if stdoutWantsJSON(*asJSON) {
		printJSON(report)
	}
	if !report.Found {
		return exitNoMatch
	}
	return exitOK
}

// resolveWhois tries the three ways in, most specific first: an aurora id is
// unambiguous, a toon names one account, and a progamer name can name several.
func resolveWhois(reg *scfingerprint.Registry, query string) ([]scfingerprint.RegistryAccount, string) {
	if id, err := strconv.ParseInt(strings.TrimSpace(query), 10, 64); err == nil {
		if a, ok := reg.LookupAurora(id); ok {
			return []scfingerprint.RegistryAccount{a}, "aurora_id"
		}
	}
	if a, ok := reg.LookupToon(query); ok {
		return []scfingerprint.RegistryAccount{a}, "toon"
	}
	if accs := reg.LookupName(query); len(accs) > 0 {
		return accs, "name"
	}
	return nil, ""
}

// annotateWithCatalog adds the fingerprint catalog's view of this player, so
// the answer says whether the registry's claim is checkable at all. A registry
// hit with no fingerprint is a harvest target; one with a fingerprint can be
// confirmed from a replay.
func annotateWithCatalog(a scfingerprint.RegistryAccount) whoisAccount {
	out := whoisAccount{
		Name:         a.Name,
		AuroraID:     a.AuroraID,
		BattleTag:    a.BattleTag,
		Country:      a.Country,
		Toons:        a.Toons,
		Source:       a.Source,
		LastVerified: a.LastVerified,
	}
	if a.Name == "" {
		return out
	}
	ids, fps, err := loadCatalog()
	if err != nil {
		return out
	}
	id, fp, err := findCatalogPlayer(ids, fps, a.Name)
	if err != nil {
		return out
	}
	out.InCatalog = true
	out.CatalogID = id.ID
	out.CatalogConfidence = id.Confidence
	out.CatalogGames = fp.N()
	out.Liquipedia = id.Liquipedia
	return out
}

func renderWhois(w io.Writer, pal palette, r whoisReport) {
	if !r.Found {
		_, _ = fmt.Fprintf(w, "%s%s✗ UNKNOWN:%s %s is not in the identity map\n\n",
			pal.bold, pal.red, pal.reset, pal.name(r.Query))
		_, _ = fmt.Fprintf(w, "  The map holds %d progamer accounts as of %s. Most Battle.net accounts\n", r.RegistryAccounts, r.RegistryGenerated)
		_, _ = fmt.Fprintf(w, "  are not in it, so this says nothing about who the player is — point\n")
		_, _ = fmt.Fprintf(w, "  %sscfingerprint match%s at one of their replays instead.\n\n", pal.cyan, pal.reset)
		return
	}

	label := r.Accounts[0].Name
	if label == "" {
		label = r.Query
	}
	plural := ""
	if len(r.Accounts) > 1 {
		plural = fmt.Sprintf(", across %d accounts", len(r.Accounts))
	}
	_, _ = fmt.Fprintf(w, "%s%s✓ %s%s%s (matched by %s%s)\n\n",
		pal.bold, pal.green, pal.reset, pal.name(label), pal.reset, r.MatchedBy, plural)

	for _, a := range r.Accounts {
		_, _ = fmt.Fprintf(w, "  %saurora %d%s", pal.bold, a.AuroraID, pal.reset)
		if a.BattleTag != "" {
			_, _ = fmt.Fprintf(w, "  battle tag %s", a.BattleTag)
		}
		if a.Country != "" {
			_, _ = fmt.Fprintf(w, "  (%s)", a.Country)
		}
		_, _ = fmt.Fprintln(w)

		if len(a.Toons) == 0 {
			_, _ = fmt.Fprintf(w, "    %sno known account names — this entry cannot be looked up by toon yet%s\n", pal.dim, pal.reset)
		} else {
			tw := tabwriter.NewWriter(w, 4, 4, 2, ' ', 0)
			for _, t := range a.Toons {
				// The activity column is dropped rather than left blank on a
				// quiet toon: tabwriter pads every cell but the last, so an
				// empty final cell would leave trailing whitespace on the line.
				if t.GamesLastWeek == 0 {
					_, _ = fmt.Fprintf(tw, "    %s%s%s\t%s\n", pal.cyan, t.Toon, pal.reset, t.GatewayName)
					continue
				}
				_, _ = fmt.Fprintf(tw, "    %s%s%s\t%s\t%s last week\n",
					pal.cyan, t.Toon, pal.reset, t.GatewayName, gamesCount(t.GamesLastWeek))
			}
			_ = tw.Flush()
		}
		if a.InCatalog {
			_, _ = fmt.Fprintf(w, "    %sfingerprint:%s in the catalog as %s (%s, %d games) — `scfingerprint match` can confirm this\n",
				pal.dim, pal.reset, a.CatalogID, a.CatalogConfidence, a.CatalogGames)
		} else if a.Name != "" {
			_, _ = fmt.Fprintf(w, "    %sfingerprint:%s none yet — harvest this account's replays to enroll them\n", pal.dim, pal.reset)
		}
		if a.Liquipedia != "" {
			_, _ = fmt.Fprintf(w, "    %sliquipedia:%s %s%s%s\n", pal.dim, pal.reset, pal.cyan, a.Liquipedia, pal.reset)
		}
		_, _ = fmt.Fprintf(w, "    %ssource: %s, last verified %s%s\n", pal.dim, a.Source, a.LastVerified, pal.reset)
		_, _ = fmt.Fprintln(w)
	}

	_, _ = fmt.Fprintf(w, "%s  This is a name lookup, not a fingerprint. Accounts get shared, sold, lent%s\n", pal.dim, pal.reset)
	_, _ = fmt.Fprintf(w, "%s  to a friend and renamed, so it is a second opinion and never evidence.%s\n", pal.dim, pal.reset)
	_, _ = fmt.Fprintf(w, "%s  Confirm from a replay: scfingerprint match <replay.rep>.%s\n", pal.dim, pal.reset)
}

// gamesCount renders a game count with the right plural, e.g. "1 game".
func gamesCount(n int) string {
	if n == 1 {
		return "1 game"
	}
	return fmt.Sprintf("%d games", n)
}
