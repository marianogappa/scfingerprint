package corpus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// writeReplays lays out a fake replay tree, including a subdirectory, since the
// real corpus mixes corpus/replays/*.rep with corpus/replays/localapi/*.rep.
func writeReplays(t *testing.T, root string) map[string][]byte {
	t.Helper()
	rng := rand.New(rand.NewSource(1))
	want := map[string][]byte{}
	for _, name := range []string{"MM-a.rep", "MM-b.rep", "localapi/deadbeef.rep"} {
		body := make([]byte, 1024+rng.Intn(1024))
		rng.Read(body)
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
		want[name] = body
	}
	return want
}

func TestScanReplaysKeysBySlashRelativePath(t *testing.T) {
	root := t.TempDir()
	want := writeReplays(t, root)
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ScanReplays(root)
	if err != nil {
		t.Fatalf("ScanReplays: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("scanned %d files, want %d: %v", len(got), len(want), got)
	}
	for name, body := range want {
		fi, ok := got[name]
		if !ok {
			t.Fatalf("missing %q from scan; got %v", name, got)
		}
		sum := sha256.Sum256(body)
		if fi.SHA256 != hex.EncodeToString(sum[:]) {
			t.Errorf("%s: hash %s, want %s", name, fi.SHA256, hex.EncodeToString(sum[:]))
		}
		if fi.Bytes != int64(len(body)) {
			t.Errorf("%s: size %d, want %d", name, fi.Bytes, len(body))
		}
	}
}

func TestArchiveRoundTrip(t *testing.T) {
	src := t.TempDir()
	want := writeReplays(t, src)

	files, err := ScanReplays(src)
	if err != nil {
		t.Fatalf("ScanReplays: %v", err)
	}
	m := NewManifest(nil, "2026-09-06", files)

	var buf bytes.Buffer
	if err := CreateArchive(&buf, src, m.SortedNames()); err != nil {
		t.Fatalf("CreateArchive: %v", err)
	}

	dest := t.TempDir()
	names, err := ExtractArchive(bytes.NewReader(buf.Bytes()), dest)
	if err != nil {
		t.Fatalf("ExtractArchive: %v", err)
	}
	if len(names) != len(want) {
		t.Fatalf("extracted %d files, want %d: %v", len(names), len(want), names)
	}
	for name, body := range want {
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("reading extracted %s: %v", name, err)
		}
		if !bytes.Equal(got, body) {
			t.Errorf("%s: content differs after round trip", name)
		}
	}

	res, err := m.Verify(dest)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !res.OK() || len(res.Extra) != 0 {
		t.Errorf("extracted corpus does not match manifest: %s", res.Summary())
	}
}

// The published asset's SHA-256 is recorded in corpus-source.json, so packing
// the same replays twice must produce the same bytes.
func TestCreateArchiveIsDeterministic(t *testing.T) {
	src := t.TempDir()
	writeReplays(t, src)
	files, err := ScanReplays(src)
	if err != nil {
		t.Fatalf("ScanReplays: %v", err)
	}
	names := NewManifest(nil, "2026-09-06", files).SortedNames()

	var first, second bytes.Buffer
	if err := CreateArchive(&first, src, names); err != nil {
		t.Fatalf("CreateArchive: %v", err)
	}
	// Touching mtimes must not change the output.
	if err := os.Chtimes(filepath.Join(src, "MM-a.rep"), archiveModTime.AddDate(1, 0, 0), archiveModTime.AddDate(1, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if err := CreateArchive(&second, src, names); err != nil {
		t.Fatalf("CreateArchive: %v", err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Error("archive is not byte-reproducible across runs")
	}
}

func TestVerifyReportsMissingCorruptAndExtra(t *testing.T) {
	root := t.TempDir()
	writeReplays(t, root)
	files, err := ScanReplays(root)
	if err != nil {
		t.Fatalf("ScanReplays: %v", err)
	}
	m := NewManifest(nil, "2026-09-06", files)

	if err := os.Remove(filepath.Join(root, "MM-a.rep")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "MM-b.rep"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "MM-c.rep"), []byte("unexpected"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := m.Verify(root)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.OK() {
		t.Error("Verify reported OK despite a missing and a corrupt replay")
	}
	if len(res.Missing) != 1 || res.Missing[0] != "MM-a.rep" {
		t.Errorf("Missing = %v, want [MM-a.rep]", res.Missing)
	}
	if len(res.Corrupt) != 1 || res.Corrupt[0] != "MM-b.rep" {
		t.Errorf("Corrupt = %v, want [MM-b.rep]", res.Corrupt)
	}
	if len(res.Extra) != 1 || res.Extra[0] != "MM-c.rep" {
		t.Errorf("Extra = %v, want [MM-c.rep]", res.Extra)
	}
}

// A corpus fetched from an untrusted URL is only usable if the checksum is
// enforced, so a mismatch must leave nothing behind for the extractor to find.
func TestDownloadRejectsChecksumMismatch(t *testing.T) {
	body := []byte("not the corpus you were promised")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "replays.tar.zst")
	err := Download(context.Background(), srv.URL, dest, "00", nil)
	if err == nil {
		t.Fatal("Download accepted a body whose hash did not match")
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("Download left %s behind after a checksum mismatch", dest)
	}
}

func TestDownloadVerifiesAndMoves(t *testing.T) {
	body := []byte("corpus bytes")
	sum := sha256.Sum256(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "nested", "replays.tar.zst")
	if err := Download(context.Background(), srv.URL, dest, hex.EncodeToString(sum[:]), nil); err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading downloaded file: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Error("downloaded content differs")
	}

	size, err := AssetSize(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("AssetSize: %v", err)
	}
	if size != int64(len(body)) {
		t.Errorf("AssetSize = %d, want %d", size, len(body))
	}
}

// Release assets are third-party input by the time they reach a contributor's
// disk, so entry names must not be able to escape the replay directory.
func TestExtractArchiveRefusesPathTraversal(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "evil.rep"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := CreateArchive(&buf, src, []string{"evil.rep"}); err != nil {
		t.Fatalf("CreateArchive: %v", err)
	}
	// Rewrite the entry name in place; both are 8 bytes so the tar stays valid.
	raw := buf.Bytes()
	repacked := bytes.Replace(raw, []byte("evil.rep"), []byte("../x.rep"), 1)
	if bytes.Equal(raw, repacked) {
		t.Skip("entry name is not stored verbatim in this archive layout")
	}

	dest := filepath.Join(t.TempDir(), "replays")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractArchive(bytes.NewReader(repacked), dest); err == nil {
		if _, statErr := os.Stat(filepath.Join(filepath.Dir(dest), "x.rep")); statErr == nil {
			t.Fatal("ExtractArchive wrote outside the destination directory")
		}
	}
}

func TestSafeJoinRefusesEscapingNames(t *testing.T) {
	root := filepath.Join(t.TempDir(), "replays")
	for _, name := range []string{"../escape.rep", "a/../../escape.rep", "/etc/passwd"} {
		got, err := safeJoin(root, name)
		if err != nil {
			continue
		}
		rel, relErr := filepath.Rel(root, got)
		if relErr != nil || rel == ".." || filepath.IsAbs(rel) || len(rel) > 2 && rel[:3] == ".."+string(filepath.Separator) {
			t.Errorf("safeJoin(%q) = %q, which escapes %q", name, got, root)
		}
	}
}

func TestTagForPrefersAssetOverride(t *testing.T) {
	s := &Source{ReleaseTag: "corpus-v2"}
	if got := s.TagFor(Asset{Name: "delta"}); got != "corpus-v2" {
		t.Errorf("TagFor = %q, want corpus-v2", got)
	}
	if got := s.TagFor(Asset{Name: "base", ReleaseTag: "corpus-v1"}); got != "corpus-v1" {
		t.Errorf("TagFor = %q, want corpus-v1", got)
	}
}

func TestNewManifestCarriesFilterProvenance(t *testing.T) {
	root := t.TempDir()
	writeReplays(t, root)
	files, err := ScanReplays(root)
	if err != nil {
		t.Fatalf("ScanReplays: %v", err)
	}
	prev := &Manifest{
		Filter: []byte(`{"min_games":20}`),
		Stats:  ManifestStats{Players: 231, Rows: 9173, Replays: 7935, TotalBytes: 1},
	}
	m := NewManifest(prev, "2026-09-06", files)

	if string(m.Filter) != `{"min_games":20}` {
		t.Errorf("Filter = %s, want it carried through", m.Filter)
	}
	if m.Stats.Players != 231 || m.Stats.Rows != 9173 {
		t.Errorf("ladder provenance lost: %+v", m.Stats)
	}
	if m.Stats.Replays != len(files) {
		t.Errorf("Stats.Replays = %d, want %d recomputed from disk", m.Stats.Replays, len(files))
	}
	var total int64
	for _, fi := range files {
		total += fi.Bytes
	}
	if m.Stats.TotalBytes != total {
		t.Errorf("Stats.TotalBytes = %d, want %d", m.Stats.TotalBytes, total)
	}
}

func TestAssetURL(t *testing.T) {
	want := "https://github.com/marianogappa/scfingerprint/releases/download/corpus-v1/replays-corpus-v1.tar.zst"
	if got := AssetURL("", "corpus-v1", "replays-corpus-v1.tar.zst"); got != want {
		t.Errorf("AssetURL = %q, want %q", got, want)
	}
}

func TestManifestAndSourceRoundTripOnDisk(t *testing.T) {
	dir := t.TempDir()
	m := &Manifest{Generated: "2026-09-06", Files: map[string]string{"MM-a.rep": "ab"}}
	if err := SaveManifest(dir, m); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	gotM, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if gotM.Files["MM-a.rep"] != "ab" || gotM.Generated != "2026-09-06" {
		t.Errorf("manifest round trip lost data: %+v", gotM)
	}

	s := &Source{ReleaseTag: "corpus-v1", Assets: []Asset{{Name: "a", SHA256: "cd", Bytes: 7, Files: 1}}}
	if err := SaveSource(dir, s); err != nil {
		t.Fatalf("SaveSource: %v", err)
	}
	gotS, err := LoadSource(dir)
	if err != nil {
		t.Fatalf("LoadSource: %v", err)
	}
	if gotS.ReleaseTag != "corpus-v1" || len(gotS.Assets) != 1 || gotS.Assets[0].SHA256 != "cd" {
		t.Errorf("source round trip lost data: %+v", gotS)
	}
}
