// Package corpus manages distribution of the labeled replay corpus.
//
// The replays are too large for Git LFS's free tier (1 GB storage and 1 GB of
// monthly bandwidth, against a ~770 MiB corpus), so they are not tracked in git
// at all. They are published as GitHub release assets under their own corpus-vN
// tags, independent of software releases, and materialized locally by
// internal/cmd/fetch-corpus.
//
// Two files in git tie a commit to its data:
//
//   - corpus-source.json names the release assets that reproduce this commit's
//     corpus, with the SHA-256 of each asset.
//   - corpus-manifest.json is the per-file integrity index: the SHA-256 of every
//     replay, keyed by slash-separated path relative to corpus/replays.
package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// ManifestFile is the per-file integrity index, relative to the corpus dir.
	ManifestFile = "corpus-manifest.json"
	// SourceFile is the release-asset pointer, relative to the corpus dir.
	SourceFile = "corpus-source.json"
	// ReplaysSubdir holds the replay files, relative to the corpus dir.
	ReplaysSubdir = "replays"
	// DefaultDir is the corpus directory relative to the repository root.
	DefaultDir = "corpus"
)

// Manifest is the decoded corpus-manifest.json.
type Manifest struct {
	Generated string          `json:"generated"`
	Filter    json.RawMessage `json:"filter,omitempty"`
	Stats     ManifestStats   `json:"stats"`
	// Files maps a slash-separated path relative to corpus/replays to its
	// SHA-256, hex-encoded.
	Files map[string]string `json:"files"`
}

// ManifestStats is the aggregate block of corpus-manifest.json. Players, Rows
// and DateRange describe the ladder subset that corpus/scripts/filter_corpus.py
// selected and are carried through untouched.
type ManifestStats struct {
	Players    int      `json:"players,omitempty"`
	Replays    int      `json:"replays"`
	Rows       int      `json:"rows,omitempty"`
	DateRange  []string `json:"date_range,omitempty"`
	TotalBytes int64    `json:"total_bytes"`
}

// Source is the decoded corpus-source.json.
type Source struct {
	// Repo is the owner/name the assets are published under. Empty means
	// DefaultRepo.
	Repo string `json:"repo,omitempty"`
	// ReleaseTag is the corpus release these assets belong to, unless an
	// individual asset overrides it.
	ReleaseTag string  `json:"release_tag"`
	Generated  string  `json:"generated"`
	RawBytes   int64   `json:"raw_bytes"`
	Assets     []Asset `json:"assets"`
}

// Asset is one published release asset.
type Asset struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Files  int    `json:"files"`
	// ReleaseTag overrides Source.ReleaseTag, so that a later corpus release can
	// keep pointing at an earlier release's assets.
	ReleaseTag string `json:"release_tag,omitempty"`
}

// TagFor returns the release tag that hosts a.
func (s *Source) TagFor(a Asset) string {
	if a.ReleaseTag != "" {
		return a.ReleaseTag
	}
	return s.ReleaseTag
}

// FileInfo is the size and hash of one replay on disk.
type FileInfo struct {
	SHA256 string
	Bytes  int64
}

// LoadManifest reads corpus-manifest.json from the corpus directory.
func LoadManifest(dir string) (*Manifest, error) {
	return loadJSON[Manifest](filepath.Join(dir, ManifestFile))
}

// LoadSource reads corpus-source.json from the corpus directory.
func LoadSource(dir string) (*Source, error) {
	return loadJSON[Source](filepath.Join(dir, SourceFile))
}

// SaveManifest writes corpus-manifest.json into the corpus directory.
func SaveManifest(dir string, m *Manifest) error {
	return saveJSON(filepath.Join(dir, ManifestFile), m)
}

// SaveSource writes corpus-source.json into the corpus directory.
func SaveSource(dir string, s *Source) error {
	return saveJSON(filepath.Join(dir, SourceFile), s)
}

func loadJSON[T any](path string) (*T, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &v, nil
}

func saveJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// ReplaysRoot returns the replay directory inside the corpus directory.
func ReplaysRoot(dir string) string { return filepath.Join(dir, ReplaysSubdir) }

// ScanReplays hashes every .rep under root and keys the results by
// slash-separated path relative to root.
func ScanReplays(root string) (map[string]FileInfo, error) {
	out := map[string]FileInfo{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".rep") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		sum, err := HashFile(p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = FileInfo{SHA256: sum, Bytes: info.Size()}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// HashFile returns the hex-encoded SHA-256 of the file at path.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyResult reports how a replay directory compares to a manifest.
type VerifyResult struct {
	Checked int
	Missing []string
	Corrupt []string
	Extra   []string
}

// OK reports whether every manifest entry was present and matched. Extra files
// are reported but not treated as a failure.
func (r VerifyResult) OK() bool { return len(r.Missing) == 0 && len(r.Corrupt) == 0 }

// Summary describes the result in one line.
func (r VerifyResult) Summary() string {
	return fmt.Sprintf("%d files checked, %d missing, %d corrupt, %d extra",
		r.Checked, len(r.Missing), len(r.Corrupt), len(r.Extra))
}

// Verify hashes the replays under root and compares them to the manifest.
func (m *Manifest) Verify(root string) (VerifyResult, error) {
	onDisk, err := ScanReplays(root)
	if err != nil {
		return VerifyResult{}, err
	}
	res := VerifyResult{Checked: len(m.Files)}
	for name, want := range m.Files {
		got, ok := onDisk[name]
		switch {
		case !ok:
			res.Missing = append(res.Missing, name)
		case got.SHA256 != want:
			res.Corrupt = append(res.Corrupt, name)
		}
	}
	for name := range onDisk {
		if _, ok := m.Files[name]; !ok {
			res.Extra = append(res.Extra, name)
		}
	}
	sort.Strings(res.Missing)
	sort.Strings(res.Corrupt)
	sort.Strings(res.Extra)
	return res, nil
}

// SortedNames returns the manifest's file names in a stable order, which is the
// order CreateArchive packs them in.
func (m *Manifest) SortedNames() []string {
	names := make([]string, 0, len(m.Files))
	for name := range m.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// NewManifest builds a manifest over a scan of the replay tree, carrying the
// Generated and Filter provenance of prev when one is given.
func NewManifest(prev *Manifest, generated string, files map[string]FileInfo) *Manifest {
	m := &Manifest{
		Generated: generated,
		Files:     make(map[string]string, len(files)),
	}
	if prev != nil {
		m.Filter = prev.Filter
		m.Stats = prev.Stats
	}
	var total int64
	for name, fi := range files {
		m.Files[name] = fi.SHA256
		total += fi.Bytes
	}
	m.Stats.Replays = len(files)
	m.Stats.TotalBytes = total
	return m
}

// safeJoin resolves a slash-separated archive entry name against root, refusing
// anything that would escape it.
func safeJoin(root, name string) (string, error) {
	clean := path.Clean("/" + name)
	if clean == "/" {
		return "", fmt.Errorf("archive entry %q has no name", name)
	}
	return filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(clean, "/"))), nil
}
