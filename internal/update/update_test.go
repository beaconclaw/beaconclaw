package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAndCompare(t *testing.T) {
	// Ascending order.
	order := []string{
		"v0.0.11",
		"v0.1.0-alpha",
		"v0.1.0-beta.1",
		"v0.1.0-beta.2",
		"v0.1.0-beta.10",
		"v0.1.0-rc.1",
		"v0.1.0",
		"v0.1.1-beta.1",
		"v0.1.1",
		"v1.0.0",
	}
	for i := range order {
		for j := range order {
			a, err := ParseVersion(order[i])
			if err != nil {
				t.Fatal(err)
			}
			b, _ := ParseVersion(order[j])
			want := sign(i - j)
			if got := a.Compare(b); got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", order[i], order[j], got, want)
			}
		}
	}
	for _, bad := range []string{"", "dev", "v1", "v1.2", "latest", "v1.2.3.4"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("ParseVersion(%q) should fail", bad)
		}
	}
	if v, _ := ParseVersion("0.1.0-beta.1"); !v.IsPrerelease() {
		t.Error("0.1.0-beta.1 should be a pre-release")
	}
}

// fakeHost serves a release API and download host.
type fakeHost struct {
	latest   string            // JSON for /releases/latest
	list     string            // JSON for /releases
	files    map[string][]byte // "<tag>/<name>" -> body
	truncate map[string]bool   // serve a short body for these
}

func (f *fakeHost) client(t *testing.T) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, f.latest)
	})
	mux.HandleFunc("/api/releases", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, f.list)
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/dl/")
		body, ok := f.files[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if f.truncate[key] {
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			w.Write(body[:len(body)/2])
			return
		}
		w.Write(body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &Client{APIBase: srv.URL + "/api", DownloadBase: srv.URL + "/dl", HTTP: srv.Client()}
}

const releaseList = `[
  {"tag_name":"v0.1.0-beta.2","prerelease":true},
  {"tag_name":"v0.2.0-beta.9","prerelease":true,"draft":true},
  {"tag_name":"v0.1.0-beta.1","prerelease":true},
  {"tag_name":"v0.0.11"},
  {"tag_name":"nightly"}
]`

func TestLatestStableIgnoresNewerPrerelease(t *testing.T) {
	f := &fakeHost{latest: `{"tag_name":"v0.0.11"}`, list: releaseList}
	c := f.client(t)

	got, err := c.Latest(context.Background(), ChannelStable)
	if err != nil || got != "v0.0.11" {
		t.Fatalf("stable = %q, %v; want v0.0.11", got, err)
	}
	got, err = c.Latest(context.Background(), ChannelBeta)
	if err != nil || got != "v0.1.0-beta.2" {
		t.Fatalf("beta = %q, %v; want v0.1.0-beta.2", got, err)
	}
}

func TestStableRefusesPrerelease(t *testing.T) {
	for _, latest := range []string{
		`{"tag_name":"v0.1.0-beta.1"}`,
		`{"tag_name":"v0.1.0","prerelease":true}`,
	} {
		f := &fakeHost{latest: latest}
		if got, err := f.client(t).Latest(context.Background(), ChannelStable); err == nil {
			t.Errorf("stable accepted %s as %q", latest, got)
		}
	}
}

func TestBetaMovesOnToNewerStable(t *testing.T) {
	f := &fakeHost{list: `[{"tag_name":"v0.1.0"},{"tag_name":"v0.1.0-beta.3","prerelease":true}]`}
	got, err := f.client(t).Latest(context.Background(), ChannelBeta)
	if err != nil || got != "v0.1.0" {
		t.Fatalf("beta = %q, %v; want v0.1.0", got, err)
	}
}

func TestCheckSecure(t *testing.T) {
	for address, wantErr := range map[string]bool{
		"https://github.com/x":      false,
		"http://127.0.0.1:8080/x":   false,
		"http://localhost:8080/x":   false,
		"http://releases.example/x": true,
		"ftp://releases.example/x":  true,
		"not a url":                 true,
	} {
		if err := checkSecure(address); (err != nil) != wantErr {
			t.Errorf("checkSecure(%q) error = %v, wantErr %v", address, err, wantErr)
		}
	}
}

func TestUnknownChannel(t *testing.T) {
	f := &fakeHost{}
	if _, err := f.client(t).Latest(context.Background(), "nightly"); err == nil {
		t.Error("unknown channel should fail")
	}
}

func sumOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// applyFixture returns a client, an installed "old" binary and options that
// skip the run check by using a platform that is never the test host.
func applyFixture(t *testing.T, f *fakeHost) (*Client, Options) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "beaconclaw")
	if err := os.WriteFile(exe, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	return f.client(t), Options{ExecPath: exe, goos: "testos", goarch: "testarch"}
}

func assertBinary(t *testing.T, opts Options, want string) {
	t.Helper()
	got, err := os.ReadFile(opts.ExecPath)
	if err != nil || string(got) != want {
		t.Errorf("binary = %q, %v; want %q", got, err, want)
	}
	entries, _ := os.ReadDir(filepath.Dir(opts.ExecPath))
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("leftover files beside the binary: %v", names)
	}
}

const asset = "beaconclaw-testos-testarch"

func TestApplyVerifiesAndReplaces(t *testing.T) {
	body := []byte("new binary")
	f := &fakeHost{files: map[string][]byte{
		"v0.1.0/" + asset:         body,
		"v0.1.0/" + ChecksumsFile: []byte(sumOf([]byte("other")) + "  beaconclaw-linux-amd64\n" + sumOf(body) + "  " + asset + "\n"),
	}}
	c, opts := applyFixture(t, f)
	restarts := 0
	opts.Restart = func() error { restarts++; return nil }

	if err := c.Apply(context.Background(), "v0.1.0", opts); err != nil {
		t.Fatal(err)
	}
	assertBinary(t, opts, "new binary")
	if restarts != 1 {
		t.Errorf("restarts = %d, want 1", restarts)
	}
	info, _ := os.Stat(opts.ExecPath)
	if info.Mode().Perm()&0111 == 0 {
		t.Error("new binary is not executable")
	}
}

func TestApplyFailuresLeaveOldBinary(t *testing.T) {
	body := []byte("new binary")
	tests := []struct {
		name string
		host *fakeHost
	}{
		{"checksum mismatch", &fakeHost{files: map[string][]byte{
			"v0.1.0/" + asset:         body,
			"v0.1.0/" + ChecksumsFile: []byte(sumOf([]byte("tampered")) + "  " + asset + "\n"),
		}}},
		{"asset missing from checksums", &fakeHost{files: map[string][]byte{
			"v0.1.0/" + asset:         body,
			"v0.1.0/" + ChecksumsFile: []byte(sumOf(body) + "  beaconclaw-linux-amd64\n"),
		}}},
		{"download missing", &fakeHost{files: map[string][]byte{
			"v0.1.0/" + ChecksumsFile: []byte(sumOf(body) + "  " + asset + "\n"),
		}}},
		{"download cut short", &fakeHost{
			files: map[string][]byte{
				"v0.1.0/" + asset:         body,
				"v0.1.0/" + ChecksumsFile: []byte(sumOf(body) + "  " + asset + "\n"),
			},
			truncate: map[string]bool{"v0.1.0/" + asset: true},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, opts := applyFixture(t, tt.host)
			opts.Restart = func() error { t.Error("service restarted after a failed update"); return nil }
			if err := c.Apply(context.Background(), "v0.1.0", opts); err == nil {
				t.Fatal("Apply should fail")
			}
			assertBinary(t, opts, "old")
		})
	}
}

func TestApplyRollsBackWhenRestartFails(t *testing.T) {
	body := []byte("new binary")
	f := &fakeHost{files: map[string][]byte{
		"v0.1.0/" + asset:         body,
		"v0.1.0/" + ChecksumsFile: []byte(sumOf(body) + "  " + asset + "\n"),
	}}
	c, opts := applyFixture(t, f)
	calls := 0
	opts.Restart = func() error {
		calls++
		if calls == 1 {
			return errors.New("did not come up")
		}
		return nil
	}
	err := c.Apply(context.Background(), "v0.1.0", opts)
	if err == nil || !strings.Contains(err.Error(), "restored") {
		t.Fatalf("err = %v; want a rollback error", err)
	}
	if calls != 2 {
		t.Errorf("restart calls = %d, want 2 (new, then old)", calls)
	}
	assertBinary(t, opts, "old")
}

func TestApplyWithoutChecksumsWarns(t *testing.T) {
	f := &fakeHost{files: map[string][]byte{"v0.0.11/" + asset: []byte("legacy")}}
	c, opts := applyFixture(t, f)
	var warnings []string
	opts.Warn = func(s string) { warnings = append(warnings, s) }
	if err := c.Apply(context.Background(), "v0.0.11", opts); err != nil {
		t.Fatal(err)
	}
	assertBinary(t, opts, "legacy")
	if len(warnings) != 1 || !strings.Contains(warnings[0], "unverified") {
		t.Errorf("warnings = %v", warnings)
	}
}
