// Package update finds releases on a track, downloads the binary for this
// platform, verifies its checksum and swaps it in for the running copy.
package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	DefaultAPIBase      = "https://api.github.com/repos/beaconclaw/beaconclaw"
	DefaultDownloadBase = "https://github.com/beaconclaw/beaconclaw/releases/download"

	// ChecksumsFile is published with each release, in sha256sum format.
	ChecksumsFile = "checksums.txt"

	ChannelStable = "stable"
	ChannelBeta   = "beta"
)

// ErrNoChecksums means the release was published without a checksums file.
var ErrNoChecksums = errors.New("release has no checksums file")

// Client talks to the release host.
type Client struct {
	APIBase      string
	DownloadBase string
	HTTP         *http.Client
	// Token is optional. It only raises the API rate limit.
	Token string
}

// NewClient returns a client for the public releases. BEACONCLAW_RELEASES_API
// and BEACONCLAW_RELEASES_DOWNLOAD point it at another host for testing.
func NewClient() *Client {
	c := &Client{
		APIBase:      DefaultAPIBase,
		DownloadBase: DefaultDownloadBase,
		HTTP:         &http.Client{Timeout: 5 * time.Minute},
		Token:        os.Getenv("GITHUB_TOKEN"),
	}
	if v := os.Getenv("BEACONCLAW_RELEASES_API"); v != "" {
		c.APIBase = strings.TrimRight(v, "/")
	}
	if v := os.Getenv("BEACONCLAW_RELEASES_DOWNLOAD"); v != "" {
		c.DownloadBase = strings.TrimRight(v, "/")
	}
	return c
}

type release struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

func (c *Client) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "beaconclaw-updater")
	if c.Token != "" && strings.HasPrefix(url, c.APIBase) {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return c.HTTP.Do(req)
}

func (c *Client) getJSON(ctx context.Context, url string, out any) error {
	resp, err := c.get(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Latest returns the newest release tag on a track. Stable never returns a
// pre-release. Beta returns the highest version of any kind, so it moves on
// to a stable release once that is newer than the last pre-release.
func (c *Client) Latest(ctx context.Context, channel string) (string, error) {
	switch channel {
	case ChannelStable:
		var r release
		if err := c.getJSON(ctx, c.APIBase+"/releases/latest", &r); err != nil {
			return "", fmt.Errorf("find latest stable release: %w", err)
		}
		if r.TagName == "" {
			return "", errors.New("find latest stable release: no release found")
		}
		if r.Prerelease || r.Draft || IsPrereleaseTag(r.TagName) {
			return "", fmt.Errorf("refusing %s: the stable track returned a pre-release", r.TagName)
		}
		return r.TagName, nil

	case ChannelBeta:
		var rs []release
		if err := c.getJSON(ctx, c.APIBase+"/releases?per_page=100", &rs); err != nil {
			return "", fmt.Errorf("list releases: %w", err)
		}
		best, bestTag := Version{}, ""
		for _, r := range rs {
			if r.Draft {
				continue
			}
			v, err := ParseVersion(r.TagName)
			if err != nil {
				continue
			}
			if bestTag == "" || v.Compare(best) > 0 {
				best, bestTag = v, r.TagName
			}
		}
		if bestTag == "" {
			return "", errors.New("list releases: no release found")
		}
		return bestTag, nil
	}
	return "", fmt.Errorf("unknown channel %q: use %s or %s", channel, ChannelStable, ChannelBeta)
}

// AssetName is the release file for a platform.
func AssetName(goos, goarch string) string {
	name := fmt.Sprintf("beaconclaw-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// Checksum returns the published SHA-256 for one asset of a release, or
// ErrNoChecksums if the release has no checksums file.
func (c *Client) Checksum(ctx context.Context, tag, asset string) (string, error) {
	url := fmt.Sprintf("%s/%s/%s", c.DownloadBase, tag, ChecksumsFile)
	resp, err := c.get(ctx, url)
	if err != nil {
		return "", fmt.Errorf("fetch checksums: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", ErrNoChecksums
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch checksums: %s", resp.Status)
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == asset {
			sum := strings.ToLower(fields[0])
			if len(sum) != sha256.Size*2 {
				return "", fmt.Errorf("checksums file has a malformed entry for %s", asset)
			}
			return sum, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("read checksums: %w", err)
	}
	return "", fmt.Errorf("checksums file for %s has no entry for %s", tag, asset)
}

// download writes one release asset to dst and returns its SHA-256.
func (c *Client) download(ctx context.Context, tag, asset string, dst *os.File) (string, error) {
	url := fmt.Sprintf("%s/%s/%s", c.DownloadBase, tag, asset)
	resp, err := c.get(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), resp.Body)
	if err != nil {
		return "", err
	}
	if resp.ContentLength > 0 && n != resp.ContentLength {
		return "", fmt.Errorf("download cut short: got %d of %d bytes", n, resp.ContentLength)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Options controls Apply.
type Options struct {
	// ExecPath is the binary to replace.
	ExecPath string
	// Restart restarts the service running ExecPath and returns an error if
	// it does not come back up. Nil means nothing needs restarting.
	Restart func() error
	// Warn receives non-fatal messages.
	Warn func(string)

	goos, goarch string // overridable in tests
}

// Apply installs the given release over ExecPath. The old binary stays in
// place unless the new one has downloaded completely, matched its checksum
// and run successfully. If the service then fails to restart, the old binary
// is put back.
func (c *Client) Apply(ctx context.Context, tag string, opts Options) error {
	goos, goarch := opts.goos, opts.goarch
	if goos == "" {
		goos, goarch = runtime.GOOS, runtime.GOARCH
	}
	warn := opts.Warn
	if warn == nil {
		warn = func(string) {}
	}
	asset := AssetName(goos, goarch)

	want, err := c.Checksum(ctx, tag, asset)
	if errors.Is(err, ErrNoChecksums) {
		warn(fmt.Sprintf("release %s was published without checksums; installing unverified", tag))
	} else if err != nil {
		return err
	}

	dir := filepath.Dir(opts.ExecPath)
	pattern := ".beaconclaw-update-*"
	if goos == "windows" {
		pattern += ".exe"
	}
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return fmt.Errorf("create temporary file beside the binary: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	got, err := c.download(ctx, tag, asset, tmp)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download %s %s: %w", tag, asset, err)
	}
	if want != "" && got != want {
		return fmt.Errorf("checksum mismatch for %s %s: expected %s, got %s", tag, asset, want, got)
	}
	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("make new binary executable: %w", err)
	}

	// Prove the new binary runs on this machine before it replaces anything.
	if goos == runtime.GOOS && goarch == runtime.GOARCH {
		vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		out, err := exec.CommandContext(vctx, tmpPath, "version").CombinedOutput()
		cancel()
		if err != nil {
			return fmt.Errorf("new binary does not run: %v: %s", err, strings.TrimSpace(string(out)))
		}
	}

	backup := opts.ExecPath + ".old"
	os.Remove(backup)
	if err := os.Rename(opts.ExecPath, backup); err != nil {
		return fmt.Errorf("set the old binary aside: %w", err)
	}
	if err := os.Rename(tmpPath, opts.ExecPath); err != nil {
		if rerr := os.Rename(backup, opts.ExecPath); rerr != nil {
			return fmt.Errorf("install new binary: %v; restoring the old one also failed: %w", err, rerr)
		}
		return fmt.Errorf("install new binary: %w", err)
	}

	if opts.Restart != nil {
		if err := opts.Restart(); err != nil {
			os.Remove(opts.ExecPath)
			if rerr := os.Rename(backup, opts.ExecPath); rerr != nil {
				return fmt.Errorf("service did not restart on %s (%v); restoring the old binary also failed: %w", tag, err, rerr)
			}
			if rerr := opts.Restart(); rerr != nil {
				return fmt.Errorf("service did not restart on %s (%v); the old binary was restored but its service did not start: %w", tag, err, rerr)
			}
			return fmt.Errorf("service did not restart on %s; the old binary was restored and is running: %w", tag, err)
		}
	}

	// On Windows a running binary cannot be deleted; the leftover is removed
	// on the next update.
	os.Remove(backup)
	return nil
}
