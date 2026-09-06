package corpus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// DefaultRepo is the owner/name that hosts the corpus releases.
const DefaultRepo = "marianogappa/scfingerprint"

// AssetURL is the unauthenticated download URL for a release asset. The
// repository is public, so this needs neither a token nor the gh CLI.
func AssetURL(repo, tag, name string) string {
	if repo == "" {
		repo = DefaultRepo
	}
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, tag, name)
}

// AssetSize returns the Content-Length the server reports for url, without
// downloading the body.
func AssetSize(ctx context.Context, url string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HEAD %s: %s", url, resp.Status)
	}
	if resp.ContentLength < 0 {
		return 0, fmt.Errorf("HEAD %s: server did not report a size", url)
	}
	return resp.ContentLength, nil
}

// Download fetches url into dest, verifying that its SHA-256 is wantSHA256
// before the file is moved into place. A partial or corrupt transfer therefore
// never leaves a usable file behind. progress, when non-nil, is called
// periodically with the bytes transferred so far and the total when known.
func Download(ctx context.Context, url, dest, wantSHA256 string, progress func(done, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".part-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	h := sha256.New()
	src := io.Reader(resp.Body)
	if progress != nil {
		src = &progressReader{r: src, total: resp.ContentLength, report: progress}
	}
	if _, err := io.Copy(io.MultiWriter(tmp, h), src); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if got := hex.EncodeToString(h.Sum(nil)); got != wantSHA256 {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", url, got, wantSHA256)
	}
	return os.Rename(tmp.Name(), dest)
}

// progressReader reports transfer progress at most once a second.
type progressReader struct {
	r      io.Reader
	total  int64
	done   int64
	last   time.Time
	report func(done, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if now := time.Now(); now.Sub(p.last) >= time.Second || err == io.EOF {
		p.last = now
		p.report(p.done, p.total)
	}
	return n, err
}
