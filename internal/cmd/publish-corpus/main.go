// Command publish-corpus packages corpus/replays/ and publishes it as a GitHub
// release asset, then rewrites corpus/corpus-manifest.json and
// corpus/corpus-source.json to point at it.
//
// This is a maintainer-only command and cannot run in CI: the replays are not
// tracked in git, so only a machine that already holds the corpus can build the
// archive.
//
// Typical use, from the repository root:
//
//	go run ./internal/cmd/publish-corpus -tag corpus-v1 \
//	    -backfill-from ../screpharvest/harvest
//
// -backfill-from makes the corpus self-sufficient before packaging: every
// replay referenced by a dataset identity's replay_manifest but missing from
// corpus/replays/ is copied in from the harvest tree. Without it the command
// still reports which references are unsatisfied, but publishes as-is.
//
// Commit the two rewritten JSON files afterwards; they are what ties the commit
// to the release.
package main

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marianogappa/scfingerprint/internal/corpus"
	"github.com/marianogappa/scfingerprint/internal/dataset"
)

func main() {
	log.SetFlags(0)

	dir := flag.String("corpus", corpus.DefaultDir, "corpus directory")
	tag := flag.String("tag", "", "corpus release tag to publish to, e.g. corpus-v1")
	repo := flag.String("repo", corpus.DefaultRepo, "owner/name to publish the release under")
	backfillFrom := flag.String("backfill-from", "", "harvest directory to copy dataset-referenced replays from before packaging")
	out := flag.String("out", "", "where to write the archive (default: alongside the corpus, deleted after upload)")
	dryRun := flag.Bool("dry-run", false, "build and hash the archive locally, then stop without touching GitHub")
	flag.Parse()

	if *tag == "" {
		log.Fatal("-tag is required, e.g. -tag corpus-v1")
	}
	replays := corpus.ReplaysRoot(*dir)

	if *backfillFrom != "" {
		if err := backfill(replays, *backfillFrom); err != nil {
			log.Fatalf("backfilling: %v", err)
		}
	}
	if err := reportCoverage(replays); err != nil {
		log.Fatalf("checking dataset coverage: %v", err)
	}

	log.Printf("hashing replays in %s", replays)
	files, err := corpus.ScanReplays(replays)
	if err != nil {
		log.Fatalf("scanning replays: %v", err)
	}
	if len(files) == 0 {
		log.Fatalf("no .rep files under %s", replays)
	}

	prev, err := corpus.LoadManifest(*dir)
	if err != nil && !os.IsNotExist(err) {
		log.Fatalf("reading previous manifest: %v", err)
	}
	manifest := corpus.NewManifest(prev, time.Now().UTC().Format(time.DateOnly), files)
	if err := corpus.SaveManifest(*dir, manifest); err != nil {
		log.Fatalf("writing manifest: %v", err)
	}
	log.Printf("manifest: %d replays, %s raw", manifest.Stats.Replays, humanBytes(manifest.Stats.TotalBytes))

	assetName := fmt.Sprintf("replays-%s.tar.zst", *tag)
	archive := *out
	if archive == "" {
		archive = filepath.Join(*dir, assetName)
		defer func() { _ = os.Remove(archive) }()
	}

	log.Printf("packing %s", archive)
	size, err := pack(archive, replays, manifest.SortedNames())
	if err != nil {
		log.Fatalf("packing: %v", err)
	}
	sum, err := corpus.HashFile(archive)
	if err != nil {
		log.Fatalf("hashing archive: %v", err)
	}
	log.Printf("archive: %s (%.1f%% of raw), sha256 %s",
		humanBytes(size), 100*float64(size)/float64(manifest.Stats.TotalBytes), sum)

	if *dryRun {
		log.Printf("dry run: skipping the GitHub release; archive left at %s", archive)
		return
	}
	if err := upload(*repo, *tag, archive, manifest); err != nil {
		log.Fatalf("uploading: %v", err)
	}

	source := &corpus.Source{
		ReleaseTag: *tag,
		Generated:  manifest.Generated,
		RawBytes:   manifest.Stats.TotalBytes,
		Assets: []corpus.Asset{{
			Name:   assetName,
			SHA256: sum,
			Bytes:  size,
			Files:  manifest.Stats.Replays,
		}},
	}
	if *repo != corpus.DefaultRepo {
		source.Repo = *repo
	}
	if err := corpus.SaveSource(*dir, source); err != nil {
		log.Fatalf("writing source: %v", err)
	}
	log.Printf("published %s", corpus.AssetURL(source.Repo, *tag, assetName))
	log.Printf("now commit %s and %s",
		filepath.Join(*dir, corpus.SourceFile), filepath.Join(*dir, corpus.ManifestFile))
}

// datasetReplays returns every replay path referenced by a dataset identity's
// replay_manifest, relative to the replays directory.
func datasetReplays() ([]string, error) {
	ids, _, err := dataset.LoadEmbedded()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, id := range ids {
		for _, ref := range id.ReplayManifest {
			seen[strings.TrimPrefix(filepath.ToSlash(ref), corpus.ReplaysSubdir+"/")] = true
		}
	}
	refs := make([]string, 0, len(seen))
	for ref := range seen {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs, nil
}

// missingFromDisk returns the dataset-referenced replays that are not on disk.
func missingFromDisk(replays string) ([]string, error) {
	refs, err := datasetReplays()
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, ref := range refs {
		if _, err := os.Stat(filepath.Join(replays, filepath.FromSlash(ref))); os.IsNotExist(err) {
			missing = append(missing, ref)
		} else if err != nil {
			return nil, err
		}
	}
	return missing, nil
}

// backfill copies dataset-referenced replays that are missing from the corpus
// out of a harvest tree, matching on filename.
func backfill(replays, harvest string) error {
	missing, err := missingFromDisk(replays)
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		log.Printf("backfill: nothing missing")
		return nil
	}
	log.Printf("backfill: %d dataset-referenced replays missing; indexing %s", len(missing), harvest)

	index, err := indexByName(harvest)
	if err != nil {
		return err
	}

	var unresolved []string
	var copied int
	var bytes int64
	for _, ref := range missing {
		src, ok := index[filepath.Base(ref)]
		if !ok {
			unresolved = append(unresolved, ref)
			continue
		}
		n, err := copyFile(src, filepath.Join(replays, filepath.FromSlash(ref)))
		if err != nil {
			return err
		}
		copied++
		bytes += n
	}
	if len(unresolved) > 0 {
		return fmt.Errorf("%d replays not found in %s (first: %s)", len(unresolved), harvest, unresolved[0])
	}
	log.Printf("backfill: copied %d replays (%s)", copied, humanBytes(bytes))
	return nil
}

// indexByName maps each .rep filename in a harvest tree to its full path. The
// first occurrence wins; replay filenames are match IDs and so are unique.
func indexByName(root string) (map[string]string, error) {
	index := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".rep") {
			return nil
		}
		if _, ok := index[d.Name()]; !ok {
			index[d.Name()] = p
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return index, nil
}

func copyFile(src, dest string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, err
	}
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer func() { _ = in.Close() }()
	tmp, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".part-*")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	n, err := io.Copy(tmp, in)
	if err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return 0, err
	}
	return n, nil
}

// reportCoverage says whether every fingerprint in the dataset can still be
// re-derived from the corpus alone, which is what replay_manifest is for.
func reportCoverage(replays string) error {
	missing, err := missingFromDisk(replays)
	if err != nil {
		return err
	}
	refs, err := datasetReplays()
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		log.Printf("coverage: all %d dataset-referenced replays are present", len(refs))
		return nil
	}
	log.Printf("WARN coverage: %d of %d dataset-referenced replays are missing; "+
		"fingerprints cannot be re-derived from this repository alone", len(missing), len(refs))
	return nil
}

func pack(archive, replays string, names []string) (int64, error) {
	f, err := os.Create(archive)
	if err != nil {
		return 0, err
	}
	if err := corpus.CreateArchive(f, replays, names); err != nil {
		_ = f.Close()
		return 0, err
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	info, err := os.Stat(archive)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// upload attaches the archive to the release, creating it if it does not exist.
// The release is explicitly not marked latest so that software releases keep
// that slot.
func upload(repo, tag, archive string, m *corpus.Manifest) error {
	if err := exec.Command("gh", "release", "view", tag, "--repo", repo).Run(); err != nil {
		notes := fmt.Sprintf(
			"Replay corpus for `%s`: %d replays, %s raw.\n\n"+
				"Fetch it with `go run ./internal/cmd/fetch-corpus` from a checkout whose "+
				"`corpus/corpus-source.json` names this tag. Do not download it by hand — the "+
				"fetcher verifies the archive and every replay against `corpus/corpus-manifest.json`.",
			tag, m.Stats.Replays, humanBytes(m.Stats.TotalBytes))
		// --prerelease, not just --latest=false: GitHub falls back to the newest
		// non-prerelease release for /releases/latest, so a corpus release would
		// otherwise occupy the latest slot whenever it is the most recent tag.
		return run("gh", "release", "create", tag,
			"--repo", repo,
			"--prerelease",
			"--title", "Replay corpus "+tag,
			"--notes", notes,
			archive)
	}
	log.Printf("release %s already exists; uploading asset", tag)
	return run("gh", "release", "upload", tag, archive, "--repo", repo, "--clobber")
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
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
