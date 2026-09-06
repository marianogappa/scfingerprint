package corpus_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/marianogappa/scfingerprint/internal/corpus"
	"github.com/marianogappa/scfingerprint/internal/dataset"
)

// corpusDir is the committed corpus, relative to this package.
const corpusDir = "../../corpus"

// replayRow is the subset of a corpus/replays.jsonl row this test needs.
type replayRow struct {
	File     string `json:"file"`
	AuroraID int64  `json:"auroraId"`
}

// A shipped fingerprint the corpus cannot reproduce is an unverifiable claim:
// the README documents extract-corpus + seed-dataset over the committed corpus
// as the way to rebuild the catalog, and that path reads corpus-manifest.json
// for the replay and corpus/replays.jsonl for who was playing. Both checks run
// on committed JSON alone, so they hold in CI without fetching any replay.
func TestCorpusBacksEveryFingerprint(t *testing.T) {
	identities, _, err := dataset.LoadEmbedded()
	if err != nil {
		t.Fatalf("loading embedded dataset: %v", err)
	}

	manifest, err := corpus.LoadManifest(corpusDir)
	if err != nil {
		t.Fatalf("loading corpus manifest: %v", err)
	}
	rows := readReplayRows(t)
	accounts := accountsByIdentity(t)

	var unhashed, unlabelled []string
	for _, id := range identities {
		for _, ref := range id.ReplayManifest {
			name := strings.TrimPrefix(ref, corpus.ReplaysSubdir+"/")
			if _, ok := manifest.Files[name]; !ok {
				unhashed = append(unhashed, id.ID+" "+ref)
				continue
			}
			// The row has to name the enrolled player, not just the replay: a
			// replay is in the corpus on either player's account, and a row
			// for the opponent alone leaves this enrollment unreproducible.
			if !rows[ref].ContainsAny(accounts[id.ID]) {
				unlabelled = append(unlabelled, id.ID+" "+ref)
			}
		}
	}

	if n := len(unhashed); n > 0 {
		sort.Strings(unhashed)
		t.Errorf("%d replays behind a fingerprint are not in corpus-manifest.json, so the corpus cannot supply them; "+
			"run `go run ./internal/cmd/publish-corpus -backfill-from <harvest>`. First few: %v", n, first(unhashed, 5))
	}
	if n := len(unlabelled); n > 0 {
		sort.Strings(unlabelled)
		t.Errorf("%d replays behind a fingerprint have no corpus/replays.jsonl row for the enrolled account, so "+
			"extract-corpus cannot re-label them. First few: %v", n, first(unlabelled, 5))
	}
}

// accountSet is the set of aurora IDs a corpus row credited a replay to.
type accountSet map[int64]bool

func (s accountSet) ContainsAny(ids []int64) bool {
	// An identity with no mapping entry cannot be checked; treat it as fine
	// rather than failing on a curation gap this test does not own.
	if len(ids) == 0 {
		return true
	}
	for _, id := range ids {
		if s[id] {
			return true
		}
	}
	return false
}

func readReplayRows(t *testing.T) map[string]accountSet {
	t.Helper()
	f, err := os.Open(filepath.Join(corpusDir, "replays.jsonl"))
	if err != nil {
		t.Fatalf("opening replays.jsonl: %v", err)
	}
	defer func() { _ = f.Close() }()

	out := map[string]accountSet{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r replayRow
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("parsing replays.jsonl: %v", err)
		}
		if out[r.File] == nil {
			out[r.File] = accountSet{}
		}
		out[r.File][r.AuroraID] = true
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading replays.jsonl: %v", err)
	}
	return out
}

// accountsByIdentity maps an identity ID to the aurora IDs it may enroll from.
// seed-dataset lowercases the pro name to form the ID, so the mapping inverts
// the same way.
func accountsByIdentity(t *testing.T) map[string][]int64 {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(corpusDir, "pros_merged.json"))
	if err != nil {
		t.Fatalf("opening pros_merged.json: %v", err)
	}
	var pros map[string][]int64
	if err := json.Unmarshal(data, &pros); err != nil {
		t.Fatalf("parsing pros_merged.json: %v", err)
	}
	out := make(map[string][]int64, len(pros))
	for name, ids := range pros {
		out[strings.ToLower(name)] = ids
	}
	return out
}

func first(ss []string, n int) []string {
	if len(ss) < n {
		return ss
	}
	return ss[:n]
}
