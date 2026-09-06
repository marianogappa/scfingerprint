package corpus

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// ArchiveWindowSize is the zstd window the corpus archive is compressed with.
//
// Individual .rep files are already internally compressed and shrink by ~0.3%
// on their own. The corpus only compresses (to ~38% of its raw size) because
// replays share map and unit data, and a window large enough to span many files
// is what lets zstd find those matches. Decoders must allow at least this much.
const ArchiveWindowSize = 1 << 27

// archiveModTime is the fixed timestamp stamped on every archive entry so that
// packing the same set of replays twice yields byte-identical output, and the
// published asset's SHA-256 is reproducible. The tar zero value is year 1,
// which USTAR cannot represent, so use the Unix epoch.
var archiveModTime = time.Unix(0, 0).UTC()

// CreateArchive writes a deterministic tar+zstd of names, each a
// slash-separated path relative to root, to w.
func CreateArchive(w io.Writer, root string, names []string) error {
	zw, err := zstd.NewWriter(w,
		zstd.WithEncoderLevel(zstd.SpeedBestCompression),
		zstd.WithWindowSize(ArchiveWindowSize),
	)
	if err != nil {
		return fmt.Errorf("creating zstd writer: %w", err)
	}
	// zstd.Writer owns goroutines, so it has to be released even when packing
	// fails part way through. Close is idempotent, so the success path's
	// explicit Close below still reports its own error.
	defer func() { _ = zw.Close() }()
	tw := tar.NewWriter(zw)

	for _, name := range names {
		if err := writeArchiveEntry(tw, root, name); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("closing tar: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("closing zstd: %w", err)
	}
	return nil
}

func writeArchiveEntry(tw *tar.Writer, root, name string) error {
	path := filepath.Join(root, filepath.FromSlash(name))
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return err
	}
	hdr := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     name,
		Size:     info.Size(),
		Mode:     0o644,
		ModTime:  archiveModTime,
		Format:   tar.FormatUSTAR,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("writing header for %s: %w", name, err)
	}
	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}
	return nil
}

// ExtractArchive unpacks a tar+zstd stream into root and returns the names it
// wrote, as slash-separated paths relative to root.
func ExtractArchive(r io.Reader, root string) ([]string, error) {
	zr, err := zstd.NewReader(r, zstd.WithDecoderMaxWindow(ArchiveWindowSize))
	if err != nil {
		return nil, fmt.Errorf("creating zstd reader: %w", err)
	}
	defer zr.Close()

	var written []string
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return written, nil
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if err := extractArchiveEntry(tr, root, hdr); err != nil {
			return nil, err
		}
		written = append(written, filepath.ToSlash(strings.TrimPrefix(hdr.Name, "./")))
	}
}

func extractArchiveEntry(tr io.Reader, root string, hdr *tar.Header) error {
	dest, err := safeJoin(root, hdr.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, tr); err != nil {
		_ = f.Close()
		return fmt.Errorf("extracting %s: %w", hdr.Name, err)
	}
	return f.Close()
}
