package images_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type descriptor struct {
	Digest    string `json:"digest"`
	MediaType string `json:"mediaType"`
	Platform  *struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	} `json:"platform,omitempty"`
}

type imageIndex struct {
	Manifests []descriptor `json:"manifests"`
}

type imageManifest struct {
	Config descriptor   `json:"config"`
	Layers []descriptor `json:"layers"`
}

func TestOCILayoutContainsExecutableLicensedPlatformImages(t *testing.T) {
	for _, name := range []string{"server", "ui", "agent", "operator"} {
		t.Run(name, func(t *testing.T) {
			blobs := readOCILayout(t, filepath.Join("..", "..", "..", ".artifacts", "oci", "xisnove-"+name+".tar"))
			var index imageIndex
			decodeBlob(t, blobs, "index.json", &index)
			platforms := map[string]bool{}
			for _, item := range platformManifests(t, blobs, index.Manifests) {
				if item.Platform == nil || item.Platform.OS != "linux" || (item.Platform.Architecture != "amd64" && item.Platform.Architecture != "arm64") {
					continue
				}
				platforms[item.Platform.Architecture] = true
				verifyOCIManifest(t, blobs, item, name)
			}
			if !platforms["amd64"] || !platforms["arm64"] || len(platforms) != 2 {
				t.Fatalf("platforms = %#v, want linux/amd64 and linux/arm64", platforms)
			}
		})
	}
}

func platformManifests(t *testing.T, blobs map[string][]byte, manifests []descriptor) []descriptor {
	t.Helper()
	var platforms []descriptor
	for _, item := range manifests {
		if item.Platform != nil {
			platforms = append(platforms, item)
			continue
		}
		if !strings.Contains(item.MediaType, "image.index") && !strings.Contains(item.MediaType, "manifest.list") {
			continue
		}
		var nested imageIndex
		decodeBlob(t, blobs, blobPath(item.Digest), &nested)
		platforms = append(platforms, platformManifests(t, blobs, nested.Manifests)...)
	}
	return platforms
}

func verifyOCIManifest(t *testing.T, blobs map[string][]byte, item descriptor, name string) {
	t.Helper()
	var manifest imageManifest
	decodeBlob(t, blobs, blobPath(item.Digest), &manifest)
	var config struct {
		Architecture string `json:"architecture"`
		History      []struct {
			CreatedBy string `json:"created_by"`
		} `json:"history"`
		Config struct {
			User       string            `json:"User"`
			Entrypoint []string          `json:"Entrypoint"`
			Labels     map[string]string `json:"Labels"`
		} `json:"config"`
	}
	decodeBlob(t, blobs, blobPath(manifest.Config.Digest), &config)
	if config.Architecture != item.Platform.Architecture || config.Config.User != "101:101" {
		t.Fatalf("%s config architecture/user = %s/%s", item.Platform.Architecture, config.Architecture, config.Config.User)
	}
	if config.Config.Labels["org.opencontainers.image.licenses"] != "Apache-2.0" {
		t.Fatalf("%s license label missing", item.Platform.Architecture)
	}
	wantEntry := "/usr/local/bin/xisnove-" + name
	if name == "ui" {
		wantEntry = "/usr/local/bin/xisnove-ui"
	}
	if len(config.Config.Entrypoint) != 1 || config.Config.Entrypoint[0] != wantEntry {
		t.Fatalf("%s entrypoint = %#v, want %s", item.Platform.Architecture, config.Config.Entrypoint, wantEntry)
	}
	for _, history := range config.History {
		createdBy := strings.ToLower(history.CreatedBy)
		for _, forbidden := range []string{".env", "password=", "token=", "secret="} {
			if strings.Contains(createdBy, forbidden) {
				t.Fatalf("%s image history contains forbidden marker %q", item.Platform.Architecture, forbidden)
			}
		}
	}

	files := map[string][]byte{}
	for _, layer := range manifest.Layers {
		readLayerFiles(t, blobs[blobPath(layer.Digest)], files)
	}
	for path, want := range map[string]string{
		"usr/share/licenses/xisnove/LICENSE": "cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30",
		"usr/share/licenses/xisnove/NOTICE":  "e3ad5f2f51b2365c85478db227c5fdf36e7f193cab97d132904af61765e4e7ba",
	} {
		contents, ok := files[path]
		if !ok || sha256Hex(contents) != want {
			t.Fatalf("%s %s missing or not exact", item.Platform.Architecture, path)
		}
	}
	if _, ok := files["etc/ssl/certs/ca-certificates.crt"]; !ok {
		t.Fatalf("%s CA bundle missing", item.Platform.Architecture)
	}
}

func readOCILayout(t *testing.T, path string) map[string][]byte {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open OCI layout %s: %v", path, err)
	}
	defer file.Close()
	values := map[string][]byte{}
	reader := tar.NewReader(file)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag != tar.TypeReg || (header.Name != "index.json" && !strings.HasPrefix(header.Name, "blobs/sha256/")) {
			continue
		}
		contents, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		values[header.Name] = contents
	}
	return values
}

func readLayerFiles(t *testing.T, compressed []byte, files map[string][]byte) {
	t.Helper()
	gzipReader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("open layer: %v", err)
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimPrefix(filepath.ToSlash(header.Name), "./")
		if name != "usr/share/licenses/xisnove/LICENSE" && name != "usr/share/licenses/xisnove/NOTICE" && name != "etc/ssl/certs/ca-certificates.crt" {
			continue
		}
		contents, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = contents
	}
}

func decodeBlob(t *testing.T, blobs map[string][]byte, path string, value any) {
	t.Helper()
	contents, ok := blobs[path]
	if !ok {
		t.Fatalf("OCI blob %s missing", path)
	}
	if err := json.Unmarshal(contents, value); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func blobPath(digest string) string {
	return "blobs/sha256/" + strings.TrimPrefix(digest, "sha256:")
}

func sha256Hex(value []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(value))
}
