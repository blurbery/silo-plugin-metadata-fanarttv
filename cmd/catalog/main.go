// Command catalog writes the one-plugin Silo catalogue a release publishes as
// catalog.json:
//
//	go run ./cmd/catalog -version 0.2.0 -repo https://github.com/OWNER/REPO \
//	  -base https://github.com/OWNER/REPO/releases/download/v0.2.0 \
//	  -checksums dist/checksums.txt > dist/catalog.json
//
// It follows Silo-Server/silo-plugins' catalogue tool: the manifest is read
// with protojson and written with encoding/json, which is how Silo decodes a
// catalogue (enums as numbers, field names as in the proto JSON tags).
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	pluginmanifest "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
	"google.golang.org/protobuf/encoding/protojson"
)

type platformBinary struct {
	URL      string `json:"url"`
	Checksum string `json:"checksum"`
}

type catalogPackage struct {
	Manifest *pluginv1.PluginManifest  `json:"manifest"`
	RepoURL  string                    `json:"repo_url"`
	Binaries map[string]platformBinary `json:"binaries"`
}

type repositoryIndex struct {
	Plugins []catalogPackage `json:"plugins"`
}

func main() {
	manifestPath := flag.String("manifest", "manifest.json", "plugin manifest")
	version := flag.String("version", "", "release version, without the v")
	repoURL := flag.String("repo", "", "https://github.com/OWNER/REPO")
	baseURL := flag.String("base", "", "URL the release binaries download from")
	checksumsPath := flag.String("checksums", "dist/checksums.txt", "sha256sum output for the binaries")
	flag.Parse()

	manifest, err := os.ReadFile(*manifestPath)
	if err != nil {
		fail(err)
	}
	checksums, err := os.ReadFile(*checksumsPath)
	if err != nil {
		fail(err)
	}
	out, err := build(manifest, *version, *repoURL, *baseURL, string(checksums))
	if err != nil {
		fail(err)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "catalog:", err)
	os.Exit(1)
}

// build returns the catalogue JSON for one release.
func build(manifestJSON []byte, version, repoURL, baseURL, checksums string) ([]byte, error) {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if version == "" || repoURL == "" || baseURL == "" {
		return nil, errors.New("version, repo and base are required")
	}
	var manifest pluginv1.PluginManifest
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(manifestJSON, &manifest); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if err := pluginmanifest.ValidateCatalogPresentation(&manifest, repoURL); err != nil {
		return nil, fmt.Errorf("manifest presentation: %w", err)
	}
	manifest.Version = version
	// The manifest's "__CHECKSUM__" placeholder is per binary; the catalogue
	// carries each platform's checksum instead.
	manifest.Checksum = ""

	binaries, err := binariesFromChecksums(checksums, strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, err
	}
	for _, platform := range manifest.GetSupportedPlatforms() {
		key := platform.GetOs() + "/" + platform.GetArch()
		if _, ok := binaries[key]; !ok {
			return nil, fmt.Errorf("no binary for supported platform %s", key)
		}
	}

	out, err := json.MarshalIndent(repositoryIndex{Plugins: []catalogPackage{{
		Manifest: &manifest,
		RepoURL:  repoURL,
		Binaries: binaries,
	}}}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// binariesFromChecksums reads sha256sum lines for plugin-<os>-<arch> files.
func binariesFromChecksums(checksums, baseURL string) (map[string]platformBinary, error) {
	binaries := make(map[string]platformBinary)
	scanner := bufio.NewScanner(strings.NewReader(checksums))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 || len(fields[0]) != 64 {
			return nil, fmt.Errorf("invalid checksum line %q", scanner.Text())
		}
		name := strings.TrimPrefix(fields[1], "*")
		platform, ok := strings.CutPrefix(name, "plugin-")
		if !ok {
			continue
		}
		osName, arch, ok := strings.Cut(platform, "-")
		if !ok || osName == "" || arch == "" {
			return nil, fmt.Errorf("binary %q is not named plugin-<os>-<arch>", name)
		}
		binaries[osName+"/"+arch] = platformBinary{URL: baseURL + "/" + name, Checksum: fields[0]}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(binaries) == 0 {
		return nil, errors.New("no plugin binaries in the checksums")
	}
	return binaries, nil
}
