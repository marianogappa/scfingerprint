// Command fetch-corpus materializes corpus/replays/ from the GitHub release
// assets named in corpus/corpus-source.json.
//
// This replaces `git lfs pull`. The replay files are not tracked in git; see
// package internal/corpus for why. Run it from the repository root:
//
//	go run ./internal/cmd/fetch-corpus
//
// It is idempotent: if the replays on disk already match
// corpus/corpus-manifest.json it downloads nothing. Use -verify-only to check
// an existing corpus without any network access.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/marianogappa/scfingerprint/internal/corpus"
)

func main() {
	log.SetFlags(0)

	dir := flag.String("corpus", corpus.DefaultDir, "corpus directory")
	verifyOnly := flag.Bool("verify-only", false, "check the corpus on disk against the manifest and exit; no network access")
	checkAssets := flag.Bool("check-assets", false, "check that every release asset still exists at the expected size, without downloading it")
	force := flag.Bool("force", false, "re-download and re-extract even if the corpus already matches")
	prune := flag.Bool("prune", false, "delete replays that are not in the manifest")
	cacheDir := flag.String("cache-dir", "", "keep downloaded archives here instead of discarding them")
	flag.Parse()

	if *checkAssets {
		if err := checkAssetsLive(*dir); err != nil {
			log.Fatal(err)
		}
		return
	}

	manifest, err := corpus.LoadManifest(*dir)
	if err != nil {
		log.Fatalf("reading manifest: %v", err)
	}
	// An empty manifest would make every verification pass vacuously.
	// filter_corpus.py writes the provenance half of the manifest and leaves
	// the hashes to publish-corpus, so this is the state between those two.
	if len(manifest.Files) == 0 {
		log.Fatalf("%s lists no files; run publish-corpus to hash the replay tree",
			filepath.Join(*dir, corpus.ManifestFile))
	}
	replays := corpus.ReplaysRoot(*dir)

	if *verifyOnly {
		if err := report(manifest, replays, *prune); err != nil {
			log.Fatal(err)
		}
		return
	}

	if !*force && upToDate(manifest, replays) {
		log.Printf("corpus is already complete: %d replays in %s", len(manifest.Files), replays)
		if err := report(manifest, replays, *prune); err != nil {
			log.Fatal(err)
		}
		return
	}

	source, err := corpus.LoadSource(*dir)
	if err != nil {
		log.Fatalf("reading source: %v", err)
	}
	if len(source.Assets) == 0 {
		log.Fatalf("%s lists no assets", filepath.Join(*dir, corpus.SourceFile))
	}

	if err := fetch(source, replays, *cacheDir); err != nil {
		log.Fatal(err)
	}
	if err := report(manifest, replays, *prune); err != nil {
		log.Fatal(err)
	}
}

// checkAssetsLive confirms every asset named in corpus-source.json is still
// reachable at the recorded size. It downloads nothing, so CI can run it on
// every change to the pointer file without moving hundreds of megabytes.
func checkAssetsLive(dir string) error {
	source, err := corpus.LoadSource(dir)
	if err != nil {
		return fmt.Errorf("reading source: %w", err)
	}
	if len(source.Assets) == 0 {
		return fmt.Errorf("%s lists no assets", filepath.Join(dir, corpus.SourceFile))
	}

	var failed int
	for _, asset := range source.Assets {
		url := corpus.AssetURL(source.Repo, source.TagFor(asset), asset.Name)
		size, err := corpus.AssetSize(context.Background(), url)
		switch {
		case err != nil:
			log.Printf("FAIL %s: %v", asset.Name, err)
			failed++
		case size != asset.Bytes:
			log.Printf("FAIL %s: server reports %d bytes, %s records %d", asset.Name, size, corpus.SourceFile, asset.Bytes)
			failed++
		default:
			log.Printf("ok   %s (%s) at %s", asset.Name, humanBytes(size), url)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d corpus assets are unreachable or the wrong size", failed, len(source.Assets))
	}
	return nil
}

// upToDate reports whether every manifest entry is present on disk with the
// right hash, in which case there is nothing to download.
func upToDate(m *corpus.Manifest, replays string) bool {
	res, err := m.Verify(replays)
	return err == nil && res.OK()
}

func fetch(source *corpus.Source, replays, cacheDir string) error {
	if err := os.MkdirAll(replays, 0o755); err != nil {
		return err
	}
	for _, asset := range source.Assets {
		tag := source.TagFor(asset)
		url := corpus.AssetURL(source.Repo, tag, asset.Name)

		dest, discard, err := archiveDest(cacheDir, asset.Name)
		if err != nil {
			return err
		}

		if reusable(dest, asset) {
			log.Printf("using cached %s", dest)
		} else {
			log.Printf("downloading %s (%s) from release %s", asset.Name, humanBytes(asset.Bytes), tag)
			err := corpus.Download(context.Background(), url, dest, asset.SHA256, func(done, total int64) {
				if total > 0 {
					log.Printf("  %s / %s (%.0f%%)", humanBytes(done), humanBytes(total), 100*float64(done)/float64(total))
					return
				}
				log.Printf("  %s", humanBytes(done))
			})
			if err != nil {
				return err
			}
		}

		names, err := extract(dest, replays)
		if err != nil {
			return err
		}
		log.Printf("extracted %d replays from %s", len(names), asset.Name)

		if discard {
			if err := os.Remove(dest); err != nil {
				return err
			}
		}
	}
	return nil
}

// archiveDest returns where to write the downloaded archive, and whether it
// should be removed once extracted.
func archiveDest(cacheDir, name string) (path string, discard bool, err error) {
	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			return "", false, err
		}
		return filepath.Join(cacheDir, name), false, nil
	}
	tmp, err := os.MkdirTemp("", "scfingerprint-corpus-*")
	if err != nil {
		return "", false, err
	}
	return filepath.Join(tmp, name), true, nil
}

// reusable reports whether a cached archive is complete and uncorrupted, so the
// download can be skipped.
func reusable(path string, asset corpus.Asset) bool {
	info, err := os.Stat(path)
	if err != nil || info.Size() != asset.Bytes {
		return false
	}
	sum, err := corpus.HashFile(path)
	return err == nil && sum == asset.SHA256
}

func extract(archive, replays string) ([]string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return corpus.ExtractArchive(f, replays)
}

func report(m *corpus.Manifest, replays string, prune bool) error {
	res, err := m.Verify(replays)
	if err != nil {
		return fmt.Errorf("verifying %s: %w", replays, err)
	}
	log.Printf("verify: %s", res.Summary())

	for _, name := range truncate(res.Missing) {
		log.Printf("  MISSING %s", name)
	}
	for _, name := range truncate(res.Corrupt) {
		log.Printf("  CORRUPT %s", name)
	}

	if len(res.Extra) > 0 {
		if !prune {
			log.Printf("  %d replays are not in the manifest; pass -prune to delete them", len(res.Extra))
		} else {
			for _, name := range res.Extra {
				if err := os.Remove(filepath.Join(replays, filepath.FromSlash(name))); err != nil {
					return err
				}
			}
			log.Printf("  pruned %d replays not in the manifest", len(res.Extra))
		}
	}

	if !res.OK() {
		return fmt.Errorf("corpus does not match the manifest")
	}
	return nil
}

func truncate(names []string) []string {
	const max = 10
	if len(names) <= max {
		return names
	}
	return names[:max]
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	for _, suffix := range []string{"KiB", "MiB", "GiB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f TiB", value/unit)
}
