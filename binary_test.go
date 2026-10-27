package main

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	sdkruntime "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtime"
)

// TestBinaryServesThroughPluginHost builds the plugin and starts it the way
// Silo's plugin host does, then calls it over gRPC. It proves the embedded
// manifest loads with a checksum and the metadata provider service answers.
func TestBinaryServesThroughPluginHost(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and starts the plugin binary")
	}
	binary := filepath.Join(t.TempDir(), "plugin")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build plugin: %v\n%s", err, output)
	}

	process := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig:  sdkruntime.HandshakeConfig(),
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
		Cmd:              exec.Command(binary),
		Plugins:          sdkruntime.DefaultPluginSet(sdkruntime.CapabilityServers{}),
		Logger:           hclog.New(&hclog.LoggerOptions{Output: io.Discard}),
	})
	t.Cleanup(process.Kill)
	rpcClient, err := process.Client()
	if err != nil {
		t.Fatalf("start plugin: %v", err)
	}
	raw, err := rpcClient.Dispense(sdkruntime.PluginSetName)
	if err != nil {
		t.Fatalf("dispense plugin: %v", err)
	}
	client, ok := raw.(*sdkruntime.Client)
	if !ok {
		t.Fatalf("dispensed %T, want *runtime.Client", raw)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	manifest, err := client.Runtime().GetManifest(ctx, &pluginv1.GetManifestRequest{})
	if err != nil {
		t.Fatalf("GetManifest() error = %v", err)
	}
	if got := manifest.GetManifest(); got.GetPluginId() != "blurbery.fanarttv" || len(got.GetChecksum()) != 64 {
		t.Fatalf("manifest = %s with checksum %q", got.GetPluginId(), got.GetChecksum())
	}

	// With no key configured, a lookup answers empty without reaching
	// fanart.tv, so this runs offline.
	if _, err := client.Runtime().Configure(ctx, &pluginv1.ConfigureRequest{}); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	images, err := client.MetadataProvider().GetImages(ctx, &pluginv1.GetImagesRequest{
		ItemType:    "movie",
		ProviderIds: providerIDs(t, map[string]any{"tmdb": "550"}),
	})
	if err != nil || len(images.GetImages()) != 0 {
		t.Fatalf("GetImages() without a key = %v, %v, want an empty answer", images, err)
	}
}
