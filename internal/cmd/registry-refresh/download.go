package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/icza/screp/repparser"
	"github.com/marianogappa/scfingerprint"
	"github.com/marianogappa/scfingerprint/internal/bnet"
	"github.com/marianogappa/scfingerprint/internal/features"
)

// gameDownload is a downloaded game: the replay on disk plus the accounts that
// uploaded a copy of it.
type gameDownload struct {
	path string

	// auroras are the accounts that uploaded this game. Every player uploads
	// their own copy, so a 1v1 names both accounts — and it names them even
	// when a player's toon has since been renamed, which is the only way to
	// identify an account whose toon no longer resolves.
	auroras []int64
}

// downloadReplay fetches one upload for a game, reusing an already-downloaded
// copy. The cache matters on a re-run with -replay-dir: the games are large and
// immutable, and re-fetching them is the slowest part of a second pass. The
// account ids still cost their request, since they only exist in the response.
func downloadReplay(ctx context.Context, client *bnet.Client, key, dir string) (gameDownload, error) {
	info, err := client.GameInfo(ctx, key)
	if err != nil {
		return gameDownload{}, err
	}
	out := gameDownload{auroras: info.AuroraIDs()}

	path := filepath.Join(dir, sanitizeFilename(key)+".rep")
	if st, err := os.Stat(path); err == nil && st.Size() > 0 {
		out.path = path
		return out, nil
	}
	for _, r := range info.Replays {
		if r.URL == "" {
			continue
		}
		if err := fetchFile(ctx, r.URL, path); err != nil {
			continue
		}
		out.path = path
		return out, nil
	}
	return out, nil
}

func fetchFile(ctx context.Context, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return nil
}

// matchToon fingerprints one named player out of a replay against the dataset.
func matchToon(path, toon string, db *scfingerprint.Dataset) ([]scfingerprint.MatchResult, error) {
	r, err := repparser.ParseFileConfig(path, repparser.Config{Commands: true})
	if err != nil {
		return nil, err
	}
	pfs, err := features.Extract(r)
	if err != nil {
		return nil, err
	}
	for _, pf := range pfs {
		if !strings.EqualFold(pf.Name, toon) {
			continue
		}
		return scfingerprint.MatchMany([]scfingerprint.PlayerGame{{Vector: pf.Vector, Race: pf.Race, Toon: pf.Name}}, db)
	}
	return nil, fmt.Errorf("player %q not found among this replay's eligible players", toon)
}

// sanitizeFilename keeps a game key usable as a filename. Ladder keys and
// numeric private-game ids are already safe; this is the guard against
// anything else the far side might hand back.
func sanitizeFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return b.String()
}
