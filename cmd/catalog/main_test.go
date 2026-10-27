package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

const (
	testRepo = "https://github.com/blurbery/silo-plugin-metadata-fanarttv"
	testBase = testRepo + "/releases/download/v0.2.0"
)

func testChecksums() string {
	return strings.Repeat("a", 64) + "  plugin-darwin-arm64\n" +
		strings.Repeat("b", 64) + "  plugin-linux-amd64\n" +
		strings.Repeat("c", 64) + "  plugin-linux-arm64\n"
}

// silosIndex mirrors the types Silo decodes a catalogue into
// (internal/plugins.RepositoryIndex), with encoding/json as Silo does.
type silosIndex struct {
	Plugins []struct {
		Manifest *pluginv1.PluginManifest `json:"manifest"`
		RepoURL  string                   `json:"repo_url"`
		Binaries map[string]struct {
			URL      string `json:"url"`
			Checksum string `json:"checksum"`
		} `json:"binaries"`
	} `json:"plugins"`
}

func TestBuildDecodesTheWaySiloReadsIt(t *testing.T) {
	manifest, err := os.ReadFile("../../manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	out, err := build(manifest, "v0.2.0", testRepo, testBase+"/", testChecksums())
	if err != nil {
		t.Fatalf("build() error = %v", err)
	}

	var index silosIndex
	if err := json.Unmarshal(out, &index); err != nil {
		t.Fatalf("Silo could not decode the catalogue: %v\n%s", err, out)
	}
	if len(index.Plugins) != 1 {
		t.Fatalf("plugins = %d, want 1", len(index.Plugins))
	}
	pkg := index.Plugins[0]
	if got := pkg.Manifest.GetPluginId(); got != "blurbery.fanarttv" {
		t.Errorf("plugin_id = %q", got)
	}
	if pkg.Manifest.GetVersion() != "0.2.0" || pkg.Manifest.GetChecksum() != "" {
		t.Errorf("version %q checksum %q, want 0.2.0 and no checksum", pkg.Manifest.GetVersion(), pkg.Manifest.GetChecksum())
	}
	if pkg.RepoURL != testRepo {
		t.Errorf("repo_url = %q", pkg.RepoURL)
	}
	fields := pkg.Manifest.GetGlobalConfigSchema()[0].GetAdminForm().GetFields()
	if fields[0].GetControl() != pluginv1.AdminFormControl_ADMIN_FORM_CONTROL_PASSWORD || !fields[0].GetSecret() {
		t.Errorf("api_key field = %v, want a secret password control", fields[0])
	}
	if got := pkg.Manifest.GetCapabilities()[0].GetMetadata().AsMap()["lookup_images"]; got != true {
		t.Errorf("lookup_images = %v", got)
	}
	linux := pkg.Binaries["linux/amd64"]
	if linux.URL != testBase+"/plugin-linux-amd64" || linux.Checksum != strings.Repeat("b", 64) {
		t.Errorf("linux/amd64 = %+v", linux)
	}
	if len(pkg.Binaries) != 3 {
		t.Errorf("binaries = %v, want three platforms", pkg.Binaries)
	}
}

func TestBuildRefusesAMissingPlatform(t *testing.T) {
	manifest, err := os.ReadFile("../../manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	onlyLinux := strings.Repeat("b", 64) + "  plugin-linux-amd64\n"
	if _, err := build(manifest, "0.2.0", testRepo, testBase, onlyLinux); err == nil {
		t.Fatal("build() accepted a release missing binaries the manifest supports")
	}
	if _, err := build(manifest, "0.2.0", testRepo, testBase, "not a checksum line"); err == nil {
		t.Fatal("build() accepted a malformed checksum line")
	}
}
