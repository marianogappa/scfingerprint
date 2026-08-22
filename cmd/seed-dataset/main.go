// Command seed-dataset reads a labeled feature CSV and enrolls selected
// players into the built-in dataset under dataset/players/.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/marianogappa/scfingerprint/dataset"
	"github.com/marianogappa/scfingerprint/fingerprint"
	"github.com/marianogappa/scfingerprint/training"
)

type seedDef struct {
	id         string
	auroraID   string
	label      string
	confidence string
	aliases    []dataset.Alias
	notes      string
}

func main() {
	csvPath := flag.String("csv", "", "labeled feature CSV (required)")
	outDir := flag.String("out", "dataset/players", "output directory for identity JSON files")
	flag.Parse()

	if *csvPath == "" {
		log.Fatal("-csv is required")
	}

	seeds := []seedDef{
		{
			id: "larva", auroraID: "11682563", label: "Larva",
			confidence: dataset.ConfidenceConfirmed,
			aliases:    []dataset.Alias{{Name: "Larva", Primary: true, ZScore: 0}},
			notes:      "enrolled from cwal-harvest corpus",
		},
		{
			id: "pluto", auroraID: "15711612", label: "Pluto",
			confidence: dataset.ConfidenceHigh,
			aliases:    []dataset.Alias{{Name: "Pluto", Primary: true}},
			notes:      "enrolled from cwal-harvest corpus",
		},
		{
			id: "reach", auroraID: "42516339", label: "Reach",
			confidence: dataset.ConfidenceHigh,
			aliases:    []dataset.Alias{{Name: "Reach", Primary: true}},
			notes:      "enrolled from cwal-harvest corpus",
		},
		{
			id: "xuanxuan", auroraID: "43286826", label: "XuanXuan",
			confidence: dataset.ConfidenceHigh,
			aliases:    []dataset.Alias{{Name: "XuanXuan", Primary: true}},
			notes:      "enrolled from cwal-harvest corpus",
		},
		{
			id: "soo", auroraID: "14726310", label: "Soo",
			confidence: dataset.ConfidenceConfirmed,
			aliases:    []dataset.Alias{{Name: "Soo", Primary: true}},
			notes:      "enrolled from cwal-harvest corpus",
		},
	}

	wantIDs := map[string]bool{}
	for _, s := range seeds {
		wantIDs[s.auroraID] = true
	}

	samples, err := training.ReadCSV(*csvPath)
	if err != nil {
		log.Fatal(err)
	}

	byPlayer := map[string][]training.Sample{}
	for _, s := range samples {
		if wantIDs[s.Player] {
			byPlayer[s.Player] = append(byPlayer[s.Player], s)
		}
	}

	for _, seed := range seeds {
		playerSamples, ok := byPlayer[seed.auroraID]
		if !ok {
			log.Fatalf("player %s (%s) not found in CSV", seed.id, seed.auroraID)
		}

		fp := fingerprint.New(fingerprint.Meta{
			Label:      seed.label,
			Source:     "cwal-harvest",
			Confidence: seed.confidence,
		})
		var manifest []string
		for _, s := range playerSamples {
			race := strings.ToLower(s.Race)
			if err := fp.Add(s.Vector, race); err != nil {
				log.Fatalf("Add for %s: %v", seed.id, err)
			}
			manifest = append(manifest, s.File)
		}

		blob, err := fp.MarshalString()
		if err != nil {
			log.Fatalf("MarshalString for %s: %v", seed.id, err)
		}

		id := dataset.Identity{
			ID:             seed.id,
			Fingerprint:    blob,
			Confidence:     seed.confidence,
			Aliases:        seed.aliases,
			ReplayManifest: manifest,
			Notes:          seed.notes,
		}
		data, err := json.MarshalIndent(id, "", " ")
		if err != nil {
			log.Fatal(err)
		}
		path := filepath.Join(*outDir, seed.id+".json")
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			log.Fatal(err)
		}
		fmt.Fprintf(os.Stderr, "wrote %s (%d games, %d bytes)\n", path, fp.N(), len(data))
	}
}
