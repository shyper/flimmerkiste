package stream

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/shyper/flimmerkiste/internal/mediaserver"
)

// maxPrefetch caps a single download; trailers in 1080p stay well below.
const maxPrefetch = 300 << 20

var prefetchClient = &http.Client{Timeout: 3 * time.Minute}

// prefetcher downloads remote clips of one stream session to local files while
// the previous item plays, so they start instantly. Files are removed after use.
type prefetcher struct {
	dir     string
	session int64
	log     *slog.Logger

	mu    sync.Mutex
	jobs  map[string]*download
	files []string
}

type download struct {
	path   string
	done   chan struct{}
	err    error
	cancel context.CancelFunc
}

// start begins downloading url for the program key (once).
func (p *prefetcher) start(key, url string) {
	if p.dir == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.jobs == nil {
		p.jobs = map[string]*download{}
	}
	if _, ok := p.jobs[key]; ok {
		return
	}
	sum := sha1.Sum([]byte(key))
	path := filepath.Join(p.dir, fmt.Sprintf("clip-%d-%s.part", p.session, hex.EncodeToString(sum[:6])))
	ctx, cancel := context.WithCancel(context.Background())
	d := &download{path: path, done: make(chan struct{}), cancel: cancel}
	p.jobs[key] = d
	p.files = append(p.files, path)
	go func() {
		defer close(d.done)
		d.err = fetchFile(ctx, url, path)
		if d.err != nil {
			// HTTP errors name the URL, which may carry the Plex token.
			p.log.Debug("prefetch failed, streaming directly", "key", key, "err", mediaserver.Redact(d.err.Error()))
		}
	}()
}

// take returns the local file for key if its download has finished ("" otherwise,
// the caller then streams from the source). Unfinished downloads are abandoned.
func (p *prefetcher) take(key string) string {
	p.mu.Lock()
	d := p.jobs[key]
	delete(p.jobs, key)
	p.mu.Unlock()
	if d == nil {
		return ""
	}
	select {
	case <-d.done:
		if d.err == nil {
			return d.path
		}
	default:
		d.cancel()
	}
	return ""
}

// close stops all downloads and removes the files of this session.
func (p *prefetcher) close() {
	p.mu.Lock()
	jobs, files := p.jobs, p.files
	p.jobs, p.files = nil, nil
	p.mu.Unlock()
	for _, d := range jobs {
		d.cancel()
		<-d.done
	}
	for _, f := range files {
		_ = os.Remove(f)
	}
}

func fetchFile(ctx context.Context, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := prefetchClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxPrefetch+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxPrefetch {
		err = fmt.Errorf("clip larger than %d MB", maxPrefetch>>20)
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
