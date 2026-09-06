package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/marianogappa/scfingerprint/internal/registry"
)

// corpusIdentity is one line of corpus/identities.jsonl.
type corpusIdentity struct {
	AuroraID  int64  `json:"auroraId"`
	BattleTag string `json:"battleTag"`
	ProName   string `json:"proName"`
	Handles   []struct {
		AuroraID int64  `json:"auroraId"`
		Toon     string `json:"toon"`
		Gateway  int    `json:"gateway"`
		ProName  string `json:"proName"`
	} `json:"handles"`
}

// runSeed rebuilds the registry from the corpus files, which between them
// already hold every part of the map — just never joined up:
//
//	pros_merged.json        pro name → aurora ids   (no toons)
//	cwal_default_list.json  pro name → aurora id + battle tag, with its snapshot date
//	identities.jsonl        aurora id → battle tag + toons, per gateway
//	pro_aliases.json        the catalog's canonical name for a pro CWAL names differently
//	pro_exclusions.json     aurora ids CWAL attributes to a pro that the fingerprints refute
//
// Seeding is offline and deterministic, so the shipped map always has a
// reproducible floor even if no one ever runs -refresh. It merges into an
// existing registry rather than replacing it, so re-seeding after a live
// refresh picks up corpus corrections without discarding the toons that
// refresh found.
func runSeed(cfg config) error {
	pros := map[string][]int64{}
	if err := readJSONFile(filepath.Join(cfg.corpusDir, "pros_merged.json"), &pros); err != nil {
		return err
	}
	// The catalog's own naming and its own refutations. Both are optional so
	// -seed still works against an older corpus.
	aliases := map[string][]string{}
	if err := readOptionalJSONFile(filepath.Join(cfg.corpusDir, "pro_aliases.json"), &aliases); err != nil {
		return err
	}
	exclusions, err := readExclusions(filepath.Join(cfg.corpusDir, "pro_exclusions.json"))
	if err != nil {
		return err
	}
	var cwal map[string][]struct {
		AuroraID  int64  `json:"aurora_id"`
		BattleTag string `json:"battle_tag"`
	}
	if err := readJSONFile(filepath.Join(cfg.corpusDir, "cwal_default_list.json"), &cwal); err != nil {
		return err
	}
	identities, err2 := readIdentities(filepath.Join(cfg.corpusDir, "identities.jsonl"))
	if err2 != nil {
		return err2
	}

	// The CWAL snapshot is the provenance for the names and battle tags, so
	// its own date is the honest last_verified for a seeded entry — not today.
	const cwalSnapshot = "2026-08-08"
	const cwalSource = "cwal-default-list@" + cwalSnapshot

	tagByAurora := map[int64]string{}
	for _, accs := range cwal {
		for _, a := range accs {
			if a.BattleTag != "" {
				tagByAurora[a.AuroraID] = a.BattleTag
			}
		}
	}

	// Merge into what is already there. A fresh checkout has no registry
	// yet, which is the from-scratch case.
	reg, regErr := loadRegistry(cfg.registryPath)
	if regErr != nil {
		reg = &registry.Registry{Version: 1}
	}
	reg.Generated = today()

	// pros_merged and CWAL can name a pro differently from the catalog; the
	// catalog's name is the one the fingerprints are labelled with, so it
	// wins, or a whois hit would not line up with a match verdict.
	canonical := map[string]string{}
	for name, alts := range aliases {
		for _, alt := range alts {
			canonical[strings.ToLower(alt)] = name
		}
	}

	names := make([]string, 0, len(pros))
	for name := range pros {
		names = append(names, name)
	}
	sort.Strings(names)

	var missing []string
	var renamed, excluded []string
	for _, rawName := range names {
		name := rawName
		if c, ok := canonical[strings.ToLower(rawName)]; ok && c != rawName {
			name = c
			renamed = append(renamed, fmt.Sprintf("%s→%s", rawName, c))
		}
		for _, aid := range pros[rawName] {
			// An aurora id the catalog has refuted must not be attributed:
			// the fingerprints are the stronger evidence, and asserting the
			// name anyway would make whois contradict match.
			if why, ok := exclusions[name][strconv.FormatInt(aid, 10)]; ok {
				excluded = append(excluded, fmt.Sprintf("%s (%d): %s", name, aid, why))
				unattribute(reg, aid, why)
				continue
			}
			acc := registry.Account{
				Name:         name,
				AuroraID:     aid,
				BattleTag:    tagByAurora[aid],
				Source:       cwalSource,
				LastVerified: cwalSnapshot,
			}
			// Never make a freshly verified entry look older than it is.
			// The seed's provenance is the 2026-08-08 CWAL snapshot; an
			// account a live refresh has since confirmed keeps its own.
			if existing, ok := reg.LookupAurora(aid); ok && existing.LastVerified > cwalSnapshot {
				acc.Source, acc.LastVerified = existing.Source, existing.LastVerified
			}
			id, ok := identities[aid]
			if !ok {
				missing = append(missing, fmt.Sprintf("%s (%d)", name, aid))
			} else {
				if acc.BattleTag == "" {
					acc.BattleTag = id.BattleTag
				}
				for _, h := range id.Handles {
					acc.Toons = append(acc.Toons, registry.Toon{Name: h.Toon, Gateway: h.Gateway})
				}
			}
			reg.Upsert(acc)
		}
	}

	fmt.Fprintf(os.Stderr, "seeded %d accounts for %d names, %d toons\n", reg.Len(), len(names), reg.ToonCount())
	if len(renamed) > 0 {
		fmt.Fprintf(os.Stderr, "used the catalog's name for %d pros: %s\n", len(renamed), strings.Join(renamed, ", "))
	}
	for _, e := range excluded {
		fmt.Fprintf(os.Stderr, "left unattributed — %s\n", e)
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "%d accounts have no toons in identities.jsonl (they need a -refresh to become useful): %s\n",
			len(missing), strings.Join(missing, ", "))
	}
	return writeRegistry(cfg, reg)
}

// unattribute strips the name from an account the catalog has refuted, keeping
// the account itself. Deleting it would lose a real Battle.net account and
// hide the very conflict the exclusion records; an unnamed entry keeps
// LookupAurora working and shows up as unowned.
func unattribute(reg *registry.Registry, aurora int64, why string) {
	acc, ok := reg.LookupAurora(aurora)
	if !ok {
		acc = registry.Account{AuroraID: aurora}
	}
	acc.Name = ""
	acc.Source = "unattributed by corpus/pro_exclusions.json: " + why
	acc.LastVerified = today()
	reg.UnsetName(aurora)
	reg.Upsert(acc)
}

// readOptionalJSONFile reads a file that a corpus may not have yet.
func readOptionalJSONFile(path string, v any) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	return readJSONFile(path, v)
}

// readExclusions reads corpus/pro_exclusions.json into pro name → aurora id →
// reason. The file carries a top-level "_comment" string alongside the pro
// entries, so it cannot be unmarshalled as one uniform map.
func readExclusions(path string) (map[string]map[string]string, error) {
	raw := map[string]json.RawMessage{}
	if err := readOptionalJSONFile(path, &raw); err != nil {
		return nil, err
	}
	out := make(map[string]map[string]string, len(raw))
	for name, blob := range raw {
		if strings.HasPrefix(name, "_") {
			continue
		}
		var byAurora map[string]string
		if err := json.Unmarshal(blob, &byAurora); err != nil {
			return nil, fmt.Errorf("parsing %s entry %q: %w", path, name, err)
		}
		out[name] = byAurora
	}
	return out, nil
}

func readJSONFile(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	return nil
}

// readIdentities indexes corpus/identities.jsonl by aurora id.
func readIdentities(path string) (map[int64]corpusIdentity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[int64]corpusIdentity{}
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var id corpusIdentity
		if err := json.Unmarshal([]byte(line), &id); err != nil {
			return nil, fmt.Errorf("parsing %s line %d: %w", path, i+1, err)
		}
		out[id.AuroraID] = id
	}
	return out, nil
}
