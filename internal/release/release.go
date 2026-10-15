// Package release finds and installs published argowt releases.
//
// CI publishes one binary per platform and a checksum file on each git tag:
//
//	tag v1.2.0 ──▶ GitHub release
//	                ├── argowt-darwin-arm64
//	                ├── argowt-linux-amd64 ...
//	                └── checksums.txt
//
// Asset names must match the Makefile cross-build target and install.sh.
package release

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	semver "k8s.io/apimachinery/pkg/util/version"
)

const (
	latestURL    = "https://api.github.com/repos/JeronimoMendes/argowt/releases/latest"
	downloadURL  = "https://github.com/JeronimoMendes/argowt/releases/download"
	checksumFile = "checksums.txt"

	// metadataLimit caps the release JSON and checksum file sizes.
	metadataLimit = 1 << 20
)

// client bounds every request so a stalled connection cannot hang the caller.
var client = &http.Client{Timeout: 5 * time.Minute}

// Latest returns the tag of the newest published release, e.g. "v1.2.0".
func Latest(ctx context.Context) (string, error) {
	body, err := fetchSmall(ctx, latestURL)
	if err != nil {
		return "", err
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode latest release: %w", err)
	}
	if payload.TagName == "" {
		return "", errors.New("latest release has no tag")
	}
	return payload.TagName, nil
}

// Newer reports whether tag is a later version than current.
// A non-semver current (e.g. a "main" source build) is always outdated.
func Newer(current, tag string) bool {
	tagVersion, err := semver.ParseSemantic(tag)
	if err != nil {
		return false
	}
	currentVersion, err := semver.ParseSemantic(current)
	if err != nil {
		return true
	}
	return currentVersion.LessThan(tagVersion)
}

// Install downloads the tag binary for this platform, verifies its checksum,
// and atomically replaces the file at path.
func Install(ctx context.Context, tag, path string) error {
	asset := fmt.Sprintf("argowt-%s-%s", runtime.GOOS, runtime.GOARCH)
	base := downloadURL + "/" + tag + "/"

	// Read the checksum first: no entry means no binary for this platform.
	sums, err := fetchSmall(ctx, base+checksumFile)
	if err != nil {
		return err
	}
	expected, err := checksum(sums, asset)
	if err != nil {
		return err
	}

	// Write next to the target so the rename stays on one filesystem.
	temp, err := os.CreateTemp(filepath.Dir(path), ".argowt-update-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()

	body, err := open(ctx, base+asset)
	if err != nil {
		return err
	}
	defer body.Close()

	// Stream to disk and hash in one pass.
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(temp, hash), body); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return fmt.Errorf("checksum mismatch for %s", asset)
	}

	if err := temp.Chmod(0o755); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

// checksum finds the digest of asset in a sha256sum file:
// "<hex digest>  argowt-linux-amd64".
func checksum(sums []byte, asset string) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(sums))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[1] == asset {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("no release binary for %s", asset)
}

// fetchSmall reads a response of at most metadataLimit bytes.
func fetchSmall(ctx context.Context, url string) ([]byte, error) {
	body, err := open(ctx, url)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return io.ReadAll(io.LimitReader(body, metadataLimit))
}

func open(ctx context.Context, url string) (io.ReadCloser, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, response.Status)
	}
	return response.Body, nil
}
