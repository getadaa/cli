// Package update finds out whether a newer adaa exists and how this copy was
// installed, so the right upgrade command can be suggested or run.
package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
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
	"strconv"
	"strings"
	"time"
)

const Repo = "getadaa/cli"

// Release is the part of a GitHub release this package uses.
type Release struct {
	Tag     string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (r *Release) Version() string { return strings.TrimPrefix(r.Tag, "v") }

func apiBase() string {
	if u := os.Getenv("ADAA_GITHUB_API"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "https://api.github.com"
}

// Latest asks GitHub for the newest published release.
func Latest(ctx context.Context) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase()+"/repos/"+Repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("no release has been published yet")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var r Release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Newer reports whether version a is newer than b. Both are semver, with or
// without a leading "v"; pre-release suffixes sort before the release.
func Newer(a, b string) bool {
	pa, pb := parse(a), parse(b)
	for n := range 3 {
		if pa.nums[n] != pb.nums[n] {
			return pa.nums[n] > pb.nums[n]
		}
	}
	if pa.pre == "" || pb.pre == "" {
		return pa.pre == "" && pb.pre != ""
	}
	return pa.pre > pb.pre
}

type semver struct {
	nums [3]int
	pre  string
}

func parse(v string) semver {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	var s semver
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		s.pre = v[i+1:]
		v = v[:i]
	}
	for n, part := range strings.SplitN(v, ".", 3) {
		s.nums[n], _ = strconv.Atoi(part)
	}
	return s
}

// Method is how this binary was installed, which decides how it is upgraded.
type Method string

const (
	Homebrew  Method = "homebrew"
	Scoop     Method = "scoop"
	GoInstall Method = "go install"
	Package   Method = "system package"
	Binary    Method = "binary"
)

// Detect works out the install method from where the executable lives.
func Detect() (Method, string) {
	exe, err := os.Executable()
	if err != nil {
		return Binary, ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	p := filepath.ToSlash(strings.ToLower(exe))
	switch {
	case strings.Contains(p, "/cellar/") || strings.Contains(p, "/caskroom/") || strings.Contains(p, "/homebrew/") || strings.Contains(p, "/linuxbrew/"):
		return Homebrew, exe
	case strings.Contains(p, "/scoop/"):
		return Scoop, exe
	case strings.Contains(p, "/go/bin/") || (os.Getenv("GOBIN") != "" && strings.HasPrefix(p, strings.ToLower(filepath.ToSlash(os.Getenv("GOBIN"))))):
		return GoInstall, exe
	case strings.HasPrefix(p, "/usr/bin/"):
		return Package, exe
	}
	return Binary, exe
}

// Command is the shell command that upgrades an install, or nil when adaa
// replaces its own binary.
func Command(m Method) []string {
	switch m {
	case Homebrew:
		return []string{"brew", "upgrade", "getadaa/tap/adaa"}
	case Scoop:
		return []string{"scoop", "update", "adaa"}
	case GoInstall:
		return []string{"go", "install", "github.com/getadaa/cli/cmd/adaa@latest"}
	}
	return nil
}

// Run upgrades using the package manager, streaming its output.
func Run(ctx context.Context, m Method, stdout, stderr io.Writer) error {
	argv := Command(m)
	if argv == nil {
		return fmt.Errorf("no package manager command for %s", m)
	}
	if m == Homebrew {
		// brew only sees a new version after fetching the tap.
		upd := exec.CommandContext(ctx, "brew", "update", "--quiet")
		upd.Stdout, upd.Stderr = stdout, stderr
		_ = upd.Run()
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	return cmd.Run()
}

// SelfReplace downloads the release archive for this platform, checks it
// against the release's checksums.txt, and swaps it in for the running binary.
func SelfReplace(ctx context.Context, rel *Release, exe string) error {
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	want := fmt.Sprintf("adaa_%s_%s_%s%s", rel.Version(), runtime.GOOS, runtime.GOARCH, ext)
	var archiveURL, sumsURL string
	for _, a := range rel.Assets {
		switch a.Name {
		case want:
			archiveURL = a.URL
		case "checksums.txt":
			sumsURL = a.URL
		}
	}
	if archiveURL == "" || sumsURL == "" {
		return fmt.Errorf("release %s has no %s", rel.Tag, want)
	}
	archive, err := download(ctx, archiveURL)
	if err != nil {
		return err
	}
	sums, err := download(ctx, sumsURL)
	if err != nil {
		return err
	}
	if err := verify(archive, sums, want); err != nil {
		return err
	}
	bin, err := extract(archive, ext)
	if err != nil {
		return err
	}
	return replace(exe, bin)
}

func download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c := &http.Client{Timeout: 5 * time.Minute}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 200<<20))
}

func verify(archive, sums []byte, name string) error {
	sum := sha256.Sum256(archive)
	got := hex.EncodeToString(sum[:])
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && f[1] == name {
			if f[0] != got {
				return fmt.Errorf("checksum mismatch for %s: refusing to install it", name)
			}
			return nil
		}
	}
	return fmt.Errorf("%s is not listed in checksums.txt", name)
}

func extract(archive []byte, ext string) ([]byte, error) {
	name := "adaa"
	if runtime.GOOS == "windows" {
		name = "adaa.exe"
	}
	if ext == ".zip" {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == name {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
		}
		return nil, fmt.Errorf("%s not found in archive", name)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("%s not found in archive", name)
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(h.Name) == name && h.Typeflag == tar.TypeReg {
			return io.ReadAll(tr)
		}
	}
}

// replace writes the new binary next to the old one and renames it into place.
// A running executable cannot be overwritten on Windows, but it can be renamed
// away, so the old one is moved aside first.
func replace(exe string, bin []byte) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".adaa-new-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w", dir, err)
	}
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	tmp.Close()
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), exe); err != nil {
		_ = os.Rename(old, exe)
		return err
	}
	if runtime.GOOS != "windows" {
		_ = os.Remove(old)
	}
	return nil
}
