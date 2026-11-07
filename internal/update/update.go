package update

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The official installers create this receipt. Combined with the release build
// marker it prevents replacing a source build or an unrecognized package install.
const receiptName = ".line-cli-install"
const receiptContent = "line-cli standalone v1\n"

type Plan struct {
	original       os.FileInfo
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	Status         string `json:"status"`
	Installation   string `json:"installation"`
	Executable     string `json:"executable"`
	CanSelfUpdate  bool   `json:"can_self_update"`
	ReleaseURL     string `json:"release_url"`
	UpgradeCommand string `json:"upgrade_command,omitempty"`
	Instructions   string `json:"instructions,omitempty"`
}

type Service struct {
	current, distribution string
	client                *http.Client
	goos, arch            string
	executable            func() (string, error)
}

func New(current, distribution string) *Service {
	return &Service{current: current, distribution: distribution, client: newHTTPClient(),
		goos: runtime.GOOS, arch: runtime.GOARCH, executable: os.Executable}
}

// Inspect reports the running version, executable, and installation method
// without network access.
func (s *Service) Inspect() (Plan, error) {
	p := Plan{CurrentVersion: s.current}
	path, err := s.executable()
	if err != nil {
		return p, errors.New("could not locate this executable; check releases at " + repositoryURL + "/releases/latest")
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return p, errors.New("could not resolve this executable; check releases at " + repositoryURL + "/releases/latest")
	}
	p.Executable = path
	p.original, err = os.Lstat(path)
	if err != nil {
		return p, errors.New("could not inspect the installed executable")
	}
	p.Installation = s.installation(path)
	return p, nil
}

func (s *Service) Check(ctx context.Context) (Plan, error) {
	p, err := s.Inspect()
	if err != nil {
		return p, err
	}
	path := p.Executable
	r, err := s.latest(ctx)
	if err != nil {
		return p, err
	}
	p.LatestVersion, p.ReleaseURL = r.Tag, releaseURL(r.Tag)
	p.Status = versionStatus(s.current, r.Tag)
	p.CanSelfUpdate = p.Installation == "standalone" && (s.goos == "darwin" || s.goos == "linux") && supportedPlatform(s.goos, s.arch) && stableVersion.MatchString(s.current)
	switch p.Installation {
	case "homebrew":
		p.UpgradeCommand = "brew upgrade line-cli"
		p.Instructions = "Managed by Homebrew. Stop LINE CLI commands and watchers, then run the upgrade command."
	case "source":
		p.Instructions = "Built from source. Stop LINE CLI commands and watchers, check out " + r.Tag + " in your source checkout, then follow the source-build instructions in CLI.md."
	case "standalone":
		if s.goos == "windows" {
			// Quote PowerShell literals, including custom directories with apostrophes.
			installDir := "'" + strings.ReplaceAll(filepath.Dir(path), "'", "''") + "'"
			p.UpgradeCommand = "$env:LINE_CLI_INSTALL_DIR = " + installDir + "; irm https://raw.githubusercontent.com/kongesque/line-cli/" + r.Tag + "/scripts/install-release.ps1 | iex"
			p.Instructions = "Stop LINE CLI commands and watchers, then run this in PowerShell. The command preserves your installation directory."
		} else if p.CanSelfUpdate {
			p.Instructions = "Run line update to install this release. Stop other LINE CLI commands and watchers first."
		} else {
			p.Instructions = "Automatic replacement is unavailable for this build or platform. Upgrade using your original installation method."
		}
	default:
		p.Instructions = "Installation method could not be verified. Upgrade using your original installer or package manager; this executable will not be replaced."
	}
	if p.CanSelfUpdate && p.Status == "update_available" && (!r.hasAsset(archiveName(s.goos, s.arch)) || !r.hasAsset("SHA256SUMS.txt")) {
		p.CanSelfUpdate = false
		p.Instructions = "This release does not yet have verified download links for your platform. Check the release page or try again later."
	}
	if p.Status == "up_to_date" || p.Status == "ahead" {
		p.Instructions, p.UpgradeCommand = "", ""
	}
	return p, nil
}

func (s *Service) installation(path string) string {
	// Resolve symlinks before recognizing Homebrew, including custom prefixes.
	normal := filepath.ToSlash(path)
	parts := strings.Split(normal, "/")
	for i := 0; i+4 < len(parts); i++ {
		if parts[i] == "Cellar" && parts[i+1] == "line-cli" && parts[i+3] == "bin" && parts[i+4] == "line" && i+5 == len(parts) {
			return "homebrew"
		}
	}
	if s.distribution != "standalone" {
		return "source"
	}
	if filepath.Base(path) != "line" && filepath.Base(path) != "line.exe" {
		return "unknown"
	}
	marker := filepath.Join(filepath.Dir(path), receiptName)
	info, err := os.Lstat(marker)
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(receiptContent)) {
		return "unknown"
	}
	f, err := os.Open(marker)
	if err != nil {
		return "unknown"
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(len(receiptContent)+1)))
	if err != nil || string(data) != receiptContent {
		return "unknown"
	}
	return "standalone"
}
