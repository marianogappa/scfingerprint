// Command registry-refresh maintains internal/registry/registry.json, the
// built-in progamer identity map. It is a curation tool, run by hand, and is
// never on the matching path.
//
// Three modes, in the order you would normally use them:
//
//	registry-refresh -seed      # rebuild the map from the corpus, offline
//	registry-refresh -refresh   # re-verify every account against a live SC:R client
//	registry-refresh -discover  # find accounts the map does not know about yet
//
// -refresh and -discover need StarCraft: Remastered running AND logged in to
// Battle.net on this machine; they drive its local web-api. The port changes
// every launch, so it is discovered rather than configured.
//
// # Why -discover exists
//
// The profile route only expands toons WITHIN accounts you already know: there
// is no by-aurora-id route, so a toon you have never seen is unreachable. New
// accounts need a different lever, and private practice games are it. Pros spar
// pros, so the unknown rival in a 1v1 private lobby is very likely another pro:
//
//	anchor on a known account
//	  → its non-ladder 1v1 games            (link is a bare number, not "MM-…")
//	  → the rival toon we do not know
//	  → download the .rep and fingerprint it against the dataset
//	  → confident match: one profile call onboards the whole new account,
//	    with every toon it owns, on every gateway
//	  → no match: queue the toon for manual naming
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/marianogappa/scfingerprint/internal/bnet"
	"github.com/marianogappa/scfingerprint/internal/registry"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type config struct {
	registryPath string
	corpusDir    string
	replayDir    string
	unknownsPath string
	apiURL       string
	limit        int
	maxFPR       float64
	minMargin    float64
	recheck      bool
	skipGapFill  bool
	dryRun       bool
	verbose      bool
}

func run() error {
	seed := flag.Bool("seed", false, "rebuild the registry from the corpus files (offline)")
	refresh := flag.Bool("refresh", false, "re-verify every account against the live SC:R web-api")
	discover := flag.Bool("discover", false, "find new accounts via non-ladder practice games")

	var cfg config
	flag.StringVar(&cfg.registryPath, "registry", "internal/registry/registry.json", "registry document to read and write")
	flag.StringVar(&cfg.corpusDir, "corpus", "corpus", "corpus directory, for -seed")
	flag.StringVar(&cfg.replayDir, "replay-dir", "", "keep replays downloaded by -discover in this directory")
	flag.StringVar(&cfg.unknownsPath, "unknowns", "", "write -discover's unidentified toons to this JSON file")
	flag.StringVar(&cfg.apiURL, "api", "", "local web-api base URL; discovered from the running client when empty")
	flag.IntVar(&cfg.limit, "limit", 0, "stop after this many accounts (0 = all)")
	flag.Float64Var(&cfg.maxFPR, "max-fpr", discoverMaxFPR, "-discover: loosest family-wise FPR a match may have and still be trusted")
	flag.Float64Var(&cfg.minMargin, "min-margin", discoverMinMargin, "-discover: minimum z-score margin over the runner-up")
	flag.BoolVar(&cfg.recheck, "recheck", false, "-refresh: re-verify accounts already verified today instead of resuming past them")
	flag.BoolVar(&cfg.skipGapFill, "skip-gap-fill", false, "-refresh: skip accounts with no known toon; they cost a slow name search that usually finds nothing")
	flag.BoolVar(&cfg.dryRun, "dry-run", false, "report what would change without writing the registry")
	flag.BoolVar(&cfg.verbose, "v", false, "log every account, not just changes")
	flag.Parse()

	modes := 0
	for _, on := range []bool{*seed, *refresh, *discover} {
		if on {
			modes++
		}
	}
	if modes != 1 {
		flag.Usage()
		return fmt.Errorf("pick exactly one of -seed, -refresh or -discover")
	}

	ctx := context.Background()
	switch {
	case *seed:
		return runSeed(cfg)
	case *refresh:
		return runRefresh(ctx, cfg)
	default:
		return runDiscover(ctx, cfg)
	}
}

// connect binds to the running client's local web-api, discovering the port
// unless the caller pinned one.
func connect(ctx context.Context, cfg config) (*bnet.Client, error) {
	if cfg.apiURL != "" {
		c := bnet.New(cfg.apiURL)
		if _, err := c.Gateways(ctx); err != nil {
			return nil, err
		}
		return c, nil
	}
	c, err := bnet.Discover(ctx)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "using the local web-api at %s\n", c.BaseURL())
	return c, nil
}

func loadRegistry(path string) (*registry.Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return registry.Parse(data)
}

// checkpoint saves progress mid-pass, so an interrupted run keeps the accounts
// it has already verified and a re-run resumes past them.
func checkpoint(cfg config, reg *registry.Registry) error {
	if cfg.dryRun {
		return nil
	}
	data, err := reg.Marshal()
	if err != nil {
		return err
	}
	if err := os.WriteFile(cfg.registryPath, data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "  … checkpointed %d accounts, %d toons\n", reg.Len(), reg.ToonCount())
	return nil
}

func writeRegistry(cfg config, reg *registry.Registry) error {
	data, err := reg.Marshal()
	if err != nil {
		return err
	}
	if cfg.dryRun {
		fmt.Fprintf(os.Stderr, "dry run: %s left untouched (%d accounts, %d toons)\n", cfg.registryPath, reg.Len(), reg.ToonCount())
		return nil
	}
	if err := os.WriteFile(cfg.registryPath, data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s: %d accounts, %d toons\n", cfg.registryPath, reg.Len(), reg.ToonCount())
	return nil
}

func today() string { return time.Now().UTC().Format("2006-01-02") }
