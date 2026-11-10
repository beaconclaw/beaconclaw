package openclaw

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// packageMarker is the folder an npm install of OpenClaw lives in.
const packageMarker = "node_modules/openclaw"

var versionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]{0,63}$`)

// cleanVersion accepts a short version-like string and nothing else.
func cleanVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "unknown" || !versionPattern.MatchString(v) {
		return ""
	}
	return v
}

// versionOfRunningGateway finds the gateway among this machine's processes and
// reads the version of the package it was started from. Only the command line
// is looked at, and only to find OpenClaw's own folder.
func versionOfRunningGateway() string {
	for _, command := range runningCommands() {
		if root := gatewayPackageRoot(command); root != "" {
			if v := packageVersion(root); v != "" {
				return v
			}
		}
	}
	return ""
}

// gatewayPackageRoot returns OpenClaw's package folder if the command line is
// an OpenClaw gateway, and "" otherwise. A gateway looks like:
//
//	node /home/me/.npm-global/lib/node_modules/openclaw/dist/index.js gateway --port 18789
func gatewayPackageRoot(args []string) string {
	isGateway := false
	root := ""
	for _, arg := range args {
		if arg == "gateway" {
			isGateway = true
			continue
		}
		slashed := filepath.ToSlash(arg)
		if idx := strings.Index(slashed, packageMarker+"/"); idx >= 0 && root == "" {
			root = filepath.FromSlash(slashed[:idx+len(packageMarker)])
		}
	}
	if !isGateway {
		return ""
	}
	return root
}

// versionOfInstalledCommand follows the openclaw command to the package it
// belongs to and reads the version there. Unlike running the command, this
// works when the service cannot find node.
func versionOfInstalledCommand() string {
	for _, candidate := range commandCandidates() {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		slashed := filepath.ToSlash(resolved)
		idx := strings.Index(slashed, packageMarker+"/")
		if idx < 0 {
			continue
		}
		if v := packageVersion(filepath.FromSlash(slashed[:idx+len(packageMarker)])); v != "" {
			return v
		}
	}
	return ""
}

// commandCandidates lists where the openclaw command may be: wherever the
// lookup path finds it, then the usual places a per-user or system install
// puts it.
func commandCandidates() []string {
	var candidates []string
	if found, err := exec.LookPath("openclaw"); err == nil {
		candidates = append(candidates, found)
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".npm-global", "bin", "openclaw"),
			filepath.Join(home, ".local", "bin", "openclaw"),
		)
	}
	return append(candidates, "/usr/local/bin/openclaw", "/opt/homebrew/bin/openclaw", "/usr/bin/openclaw")
}

// packageVersion reads the version from OpenClaw's package.json. It reads the
// name and version fields only, and refuses a package that is not OpenClaw.
func packageVersion(root string) string {
	f, err := os.Open(filepath.Join(root, "package.json"))
	if err != nil {
		return ""
	}
	defer f.Close()

	var pkg struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(&limitedReader{r: f, n: 1 << 20}).Decode(&pkg); err != nil {
		return ""
	}
	if pkg.Name != "openclaw" {
		return ""
	}
	return cleanVersion(pkg.Version)
}

type limitedReader struct {
	r interface{ Read([]byte) (int, error) }
	n int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.n <= 0 {
		return 0, os.ErrInvalid
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.r.Read(p)
	l.n -= int64(n)
	return n, err
}
