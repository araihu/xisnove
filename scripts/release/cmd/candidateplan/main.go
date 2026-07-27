// Command candidateplan assembles deterministic release-candidate inputs.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	digestPattern  = regexp.MustCompile(`^sha256:([0-9a-f]{64})$`)
)

type plan struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Locator   string `json:"locator"`
	Path      string `json:"path"`
	Platform  string `json:"platform,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
}

type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Platform  *struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	} `json:"platform,omitempty"`
}

type imageIndex struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType,omitempty"`
	Manifests     []descriptor `json:"manifests"`
}

type imageManifest struct {
	Config descriptor   `json:"config"`
	Layers []descriptor `json:"layers"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "candidateplan:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("expected package-chart, extract-oci, verify-chart-layout, sbom-oci, plan, checksums, or lock")
	}
	switch args[0] {
	case "package-chart":
		return packageChart(args[1:])
	case "extract-oci":
		return extractOCI(args[1:])
	case "verify-chart-layout":
		return verifyChartLayout(args[1:])
	case "sbom-oci":
		return sbomOCI(args[1:])
	case "plan":
		return writePlan(args[1:])
	case "checksums":
		return writeChecksums(args[1:])
	case "lock":
		return readLock(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func packageChart(args []string) error {
	flags := flag.NewFlagSet("package-chart", flag.ContinueOnError)
	chart := flags.String("chart", "", "chart directory")
	output := flags.String("output", "", "output tgz")
	version := flags.String("version", "", "chart and app version")
	epochValue := flags.String("source-date-epoch", "", "Unix timestamp")
	if err := flags.Parse(args); err != nil {
		return err
	}
	epoch, err := parseEpoch(*epochValue)
	if err != nil {
		return err
	}
	if *chart == "" || *output == "" || !versionPattern.MatchString(*version) {
		return errors.New("--chart, --output, and semantic --version are required")
	}
	name := filepath.Base(filepath.Clean(*chart))
	var paths []string
	err = filepath.WalkDir(*chart, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refuse non-regular chart input %s", path)
		}
		relative, err := filepath.Rel(*chart, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(paths)
	if !contains(paths, "Chart.yaml") || !contains(paths, "LICENSE") || !contains(paths, "NOTICE") {
		return errors.New("chart must contain Chart.yaml, LICENSE, and NOTICE")
	}
	return writeTarGzip(*output, epoch, func(writer *tar.Writer) error {
		for _, relative := range paths {
			contents, err := os.ReadFile(filepath.Join(*chart, filepath.FromSlash(relative)))
			if err != nil {
				return err
			}
			if relative == "Chart.yaml" {
				contents, err = releaseChartYAML(contents, *version)
				if err != nil {
					return err
				}
			}
			header := &tar.Header{Name: filepath.ToSlash(filepath.Join(name, filepath.FromSlash(relative))), Mode: 0o644, Uid: 0, Gid: 0, Size: int64(len(contents)), ModTime: epoch, Format: tar.FormatPAX}
			if err := writer.WriteHeader(header); err != nil {
				return err
			}
			if _, err := writer.Write(contents); err != nil {
				return err
			}
		}
		return nil
	})
}

func releaseChartYAML(contents []byte, version string) ([]byte, error) {
	lines := strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n")
	foundVersion, foundApp := false, false
	for index, line := range lines {
		switch {
		case strings.HasPrefix(line, "version:"):
			lines[index], foundVersion = "version: "+version, true
		case strings.HasPrefix(line, "appVersion:"):
			lines[index], foundApp = "appVersion: \""+version+"\"", true
		}
	}
	if !foundVersion || !foundApp {
		return nil, errors.New("Chart.yaml must define top-level version and appVersion")
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

func writeTarGzip(output string, epoch time.Time, populate func(*tar.Writer) error) error {
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	temporary := output + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(temporary)
		}
	}()
	gz, err := gzip.NewWriterLevel(file, gzip.BestCompression)
	if err != nil {
		return err
	}
	gz.Header.ModTime, gz.Header.OS = epoch, 255
	writer := tar.NewWriter(gz)
	if err := populate(writer); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, output); err != nil {
		return err
	}
	ok = true
	return nil
}

func extractOCI(args []string) error {
	flags := flag.NewFlagSet("extract-oci", flag.ContinueOnError)
	layout := flags.String("layout", "", "OCI layout tar")
	outputDir := flags.String("output-dir", "", "output directory")
	name := flags.String("name", "", "image name")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *layout == "" || *outputDir == "" || *name == "" {
		return errors.New("--layout, --output-dir, and --name are required")
	}
	blobs, err := readLayout(*layout)
	if err != nil {
		return err
	}
	var top imageIndex
	if err := json.Unmarshal(blobs["index.json"], &top); err != nil {
		return fmt.Errorf("decode OCI layout index: %w", err)
	}
	if err := validateLayoutClosure(blobs, top); err != nil {
		return err
	}
	_, _, platforms, err := selectImageSubjects(blobs, top)
	if err != nil {
		return fmt.Errorf("%s: %w", *name, err)
	}
	if len(platforms) != 2 {
		return fmt.Errorf("%s lacks exact linux/amd64 and linux/arm64 manifests", *name)
	}
	paths := make([]string, 0, len(blobs))
	for path := range blobs {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := atomicWrite(filepath.Join(*outputDir, "layout", filepath.FromSlash(path)), blobs[path]); err != nil {
			return err
		}
	}
	return nil
}

func readLayout(path string) (map[string][]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := tar.NewReader(file)
	values := map[string][]byte{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		raw := filepath.ToSlash(header.Name)
		trimmed := strings.TrimPrefix(raw, "./")
		for _, segment := range strings.Split(trimmed, "/") {
			if segment == ".." {
				return nil, fmt.Errorf("unsafe OCI layout path %q", header.Name)
			}
		}
		clean := filepath.ToSlash(filepath.Clean(trimmed))
		if filepath.IsAbs(header.Name) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, fmt.Errorf("unsafe OCI layout path %q", header.Name)
		}
		if header.Typeflag == tar.TypeDir {
			if clean != "blobs" && clean != "blobs/sha256" {
				return nil, fmt.Errorf("unexpected OCI layout directory %q", header.Name)
			}
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return nil, fmt.Errorf("refuse non-regular OCI layout entry %q", header.Name)
		}
		if clean != "index.json" && clean != "oci-layout" && !regexp.MustCompile(`^blobs/sha256/[0-9a-f]{64}$`).MatchString(clean) {
			return nil, fmt.Errorf("unexpected OCI layout entry %q", header.Name)
		}
		contents, err := io.ReadAll(reader)
		if err != nil {
			return nil, err
		}
		if _, duplicate := values[clean]; duplicate {
			return nil, fmt.Errorf("duplicate OCI layout entry %q", clean)
		}
		values[clean] = contents
	}
	if _, ok := values["index.json"]; !ok {
		return nil, errors.New("OCI layout index.json missing")
	}
	if _, ok := values["oci-layout"]; !ok {
		return nil, errors.New("OCI layout marker missing")
	}
	for path, contents := range values {
		if !strings.HasPrefix(path, "blobs/sha256/") {
			continue
		}
		digest := sha256.Sum256(contents)
		if hex.EncodeToString(digest[:]) != strings.TrimPrefix(path, "blobs/sha256/") {
			return nil, fmt.Errorf("OCI blob filename digest mismatch: %s", path)
		}
	}
	return values, nil
}

func validateLayoutClosure(blobs map[string][]byte, top imageIndex) error {
	visited := map[string]bool{}
	var visit func(descriptor) error
	visit = func(item descriptor) error {
		if visited[item.Digest] {
			return nil
		}
		contents, ok := blobs[blobPath(item.Digest)]
		if !ok {
			return fmt.Errorf("referenced OCI blob %s missing", item.Digest)
		}
		if err := verifyDescriptor(item, contents); err != nil {
			return err
		}
		visited[item.Digest] = true
		switch {
		case strings.Contains(item.MediaType, "image.index"), strings.Contains(item.MediaType, "manifest.list"):
			var index imageIndex
			if err := json.Unmarshal(contents, &index); err != nil {
				return err
			}
			for _, child := range index.Manifests {
				if err := visit(child); err != nil {
					return err
				}
			}
		case strings.Contains(item.MediaType, "image.manifest"):
			var manifest imageManifest
			if err := json.Unmarshal(contents, &manifest); err != nil {
				return err
			}
			if err := visit(manifest.Config); err != nil {
				return err
			}
			for _, layer := range manifest.Layers {
				if err := visit(layer); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, item := range top.Manifests {
		if err := visit(item); err != nil {
			return err
		}
	}
	return nil
}

func selectImageSubjects(blobs map[string][]byte, top imageIndex) (descriptor, []byte, map[string]descriptor, error) {
	var root descriptor
	var index imageIndex
	var indexBytes []byte
	if len(top.Manifests) == 1 && strings.Contains(top.Manifests[0].MediaType, "image.index") {
		root = top.Manifests[0]
		indexBytes = blobs[blobPath(root.Digest)]
		if err := json.Unmarshal(indexBytes, &index); err != nil {
			return descriptor{}, nil, nil, err
		}
	} else {
		indexBytes = blobs["index.json"]
		digest := sha256.Sum256(indexBytes)
		root = descriptor{MediaType: "application/vnd.oci.image.index.v1+json", Digest: "sha256:" + hex.EncodeToString(digest[:]), Size: int64(len(indexBytes))}
		index = top
	}
	platforms := map[string]descriptor{}
	for _, item := range index.Manifests {
		if item.Platform == nil || item.Platform.OS != "linux" || (item.Platform.Architecture != "amd64" && item.Platform.Architecture != "arm64") {
			continue
		}
		if _, exists := platforms[item.Platform.Architecture]; exists {
			return descriptor{}, nil, nil, fmt.Errorf("duplicate linux/%s manifest", item.Platform.Architecture)
		}
		platforms[item.Platform.Architecture] = item
	}
	if _, ok := platforms["amd64"]; !ok {
		return descriptor{}, nil, nil, errors.New("linux/amd64 manifest missing")
	}
	if _, ok := platforms["arm64"]; !ok {
		return descriptor{}, nil, nil, errors.New("linux/arm64 manifest missing")
	}
	return root, indexBytes, platforms, nil
}

func selectArtifactSubject(blobs map[string][]byte, top imageIndex) (descriptor, []byte, error) {
	if len(top.Manifests) != 1 {
		return descriptor{}, nil, errors.New("artifact layout must have exactly one root descriptor")
	}
	root := top.Manifests[0]
	contents, ok := blobs[blobPath(root.Digest)]
	if !ok {
		return descriptor{}, nil, fmt.Errorf("artifact root %s missing", root.Digest)
	}
	if err := verifyDescriptor(root, contents); err != nil {
		return descriptor{}, nil, err
	}
	return root, contents, nil
}

func verifyChartLayout(args []string) error {
	flags := flag.NewFlagSet("verify-chart-layout", flag.ContinueOnError)
	layoutDir := flags.String("layout-dir", "", "complete OCI layout directory")
	chart := flags.String("chart", "", "accepted chart package")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *layoutDir == "" || *chart == "" {
		return errors.New("--layout-dir and --chart are required")
	}
	blobs, err := readLayoutDir(*layoutDir)
	if err != nil {
		return err
	}
	var top imageIndex
	if err := json.Unmarshal(blobs["index.json"], &top); err != nil {
		return err
	}
	if err := validateLayoutClosure(blobs, top); err != nil {
		return err
	}
	root, contents, err := selectArtifactSubject(blobs, top)
	if err != nil {
		return err
	}
	if !strings.Contains(root.MediaType, "image.manifest") {
		return errors.New("chart root is not an OCI manifest")
	}
	var manifest imageManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		return err
	}
	chartBytes, err := os.ReadFile(*chart)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(chartBytes)
	want := "sha256:" + hex.EncodeToString(digest[:])
	for _, layer := range manifest.Layers {
		if layer.Digest == want && layer.Size == int64(len(chartBytes)) && strings.Contains(layer.MediaType, "helm.chart.content") {
			return nil
		}
	}
	return errors.New("chart OCI layout does not close over exact accepted tgz bytes")
}

func sbomOCI(args []string) error {
	flags := flag.NewFlagSet("sbom-oci", flag.ContinueOnError)
	layoutDir := flags.String("layout-dir", "", "complete OCI layout directory")
	kind := flags.String("kind", "", "oci-index or oci-manifest")
	name := flags.String("name", "", "subject name")
	outputDir := flags.String("output-dir", "", "SBOM output directory")
	epochValue := flags.String("source-date-epoch", "", "Unix timestamp")
	syft := flags.String("syft", "syft", "Syft executable")
	releasebundle := flags.String("releasebundle", "releasebundle", "normalizer executable")
	platformsRequired := flags.Bool("platforms", true, "emit linux/amd64 and linux/arm64 SBOMs")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *layoutDir == "" || *outputDir == "" || (*kind != "oci-index" && *kind != "oci-manifest") {
		return errors.New("--layout-dir, --output-dir, and valid --kind are required")
	}
	if err := validateSubjectName(*name); err != nil {
		return err
	}
	epoch, err := parseEpoch(*epochValue)
	if err != nil {
		return err
	}
	blobs, err := readLayoutDir(*layoutDir)
	if err != nil {
		return err
	}
	var top imageIndex
	if err := json.Unmarshal(blobs["index.json"], &top); err != nil {
		return err
	}
	if err := validateLayoutClosure(blobs, top); err != nil {
		return err
	}
	root, indexBytes, platforms, err := selectImageSubjects(blobs, top)
	if !*platformsRequired {
		root, indexBytes, err = selectArtifactSubject(blobs, top)
		platforms = nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		return err
	}
	type target struct{ suffix, platform, digest string }
	targets := []target{}
	actualIndexDigest := sha256.Sum256(indexBytes)
	indexDigest := strings.TrimPrefix(root.Digest, "sha256:")
	if hex.EncodeToString(actualIndexDigest[:]) != indexDigest {
		return errors.New("selected OCI index digest mismatch")
	}
	if *platformsRequired {
		for _, arch := range []string{"amd64", "arm64"} {
			targets = append(targets, target{suffix: "linux-" + arch, platform: "linux/" + arch, digest: strings.TrimPrefix(platforms[arch].Digest, "sha256:")})
		}
	} else {
		targets = append(targets, target{suffix: "index", digest: indexDigest})
	}
	platformOutputs := map[string]string{}
	for _, item := range targets {
		formulaKind := *kind
		platformSuffix := ""
		if item.platform != "" {
			formulaKind = "oci-platform-manifest"
			platformSuffix = "--" + strings.ReplaceAll(item.platform, "/", "-")
		}
		formula := formulaKind + "--" + *name + platformSuffix
		raw := filepath.Join(*outputDir, "."+formula+".raw.json")
		output := filepath.Join(*outputDir, formula+".spdx.json")
		syftArgs := []string{"oci-dir:" + *layoutDir}
		if item.platform != "" {
			syftArgs = append(syftArgs, "--platform", item.platform)
		}
		syftArgs = append(syftArgs, "-o", "spdx-json="+raw)
		if combined, err := exec.Command(*syft, syftArgs...).CombinedOutput(); err != nil {
			return fmt.Errorf("syft %s: %w: %s", item.suffix, err, strings.TrimSpace(string(combined)))
		}
		command := exec.Command(*releasebundle, "normalize-sbom", "--input", raw, "--output", output, "--subject-sha256", item.digest, "--source-date-epoch", *epochValue)
		if combined, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("normalize %s: %w: %s", item.suffix, err, strings.TrimSpace(string(combined)))
		}
		if err := os.Remove(raw); err != nil {
			return err
		}
		if item.platform != "" {
			platformOutputs[item.platform] = output
		}
	}
	if *platformsRequired {
		type checksum struct {
			Algorithm string `json:"algorithm"`
			Value     string `json:"checksumValue"`
		}
		type reference struct {
			ID       string   `json:"externalDocumentId"`
			Document string   `json:"spdxDocument"`
			Checksum checksum `json:"checksum"`
		}
		type creation struct {
			Created  string   `json:"created"`
			Creators []string `json:"creators"`
		}
		type composition struct {
			SPDXVersion string      `json:"spdxVersion"`
			DataLicense string      `json:"dataLicense"`
			SPDXID      string      `json:"SPDXID"`
			Name        string      `json:"name"`
			Namespace   string      `json:"documentNamespace"`
			Creation    creation    `json:"creationInfo"`
			References  []reference `json:"externalDocumentRefs"`
			Packages    []any       `json:"packages"`
		}
		var references []reference
		for _, arch := range []string{"amd64", "arm64"} {
			path := platformOutputs["linux/"+arch]
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			digest := sha256.Sum256(contents)
			references = append(references, reference{ID: "DocumentRef-linux-" + arch, Document: "file:sboms/" + filepath.Base(path), Checksum: checksum{Algorithm: "SHA256", Value: hex.EncodeToString(digest[:])}})
		}
		formula := "oci-index--" + *name
		document := composition{SPDXVersion: "SPDX-2.3", DataLicense: "CC0-1.0", SPDXID: "SPDXRef-DOCUMENT", Name: formula, Namespace: "https://xisnove.dev/sbom/" + indexDigest, Creation: creation{Created: epoch.Format(time.RFC3339), Creators: []string{"Tool: xisnove-candidateplan"}}, References: references, Packages: []any{}}
		contents, err := json.Marshal(document)
		if err != nil {
			return err
		}
		if err := atomicWrite(filepath.Join(*outputDir, formula+".spdx.json"), append(contents, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func validateSubjectName(name string) error {
	if !regexp.MustCompile(`^[A-Za-z0-9._-]+$`).MatchString(name) || strings.Contains(name, "--") {
		return errors.New("subject name must match [A-Za-z0-9._-]+ and exclude --")
	}
	return nil
}

func readLayoutDir(root string) (map[string][]byte, error) {
	values := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative != "index.json" && relative != "oci-layout" && !regexp.MustCompile(`^blobs/sha256/[0-9a-f]{64}$`).MatchString(relative) {
			return fmt.Errorf("unexpected OCI layout file %s", relative)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		values[relative] = contents
		return nil
	})
	if err != nil {
		return nil, err
	}
	if _, ok := values["index.json"]; !ok {
		return nil, errors.New("layout index missing")
	}
	if marker, ok := values["oci-layout"]; !ok || !bytes.Equal(marker, []byte(`{"imageLayoutVersion":"1.0.0"}`)) {
		return nil, errors.New("invalid OCI layout marker")
	}
	for path, contents := range values {
		if !strings.HasPrefix(path, "blobs/sha256/") {
			continue
		}
		digest := sha256.Sum256(contents)
		if hex.EncodeToString(digest[:]) != strings.TrimPrefix(path, "blobs/sha256/") {
			return nil, fmt.Errorf("OCI blob filename digest mismatch: %s", path)
		}
	}
	return values, nil
}

func verifyDescriptor(item descriptor, contents []byte) error {
	match := digestPattern.FindStringSubmatch(item.Digest)
	if match == nil {
		return fmt.Errorf("invalid OCI digest %q", item.Digest)
	}
	digest := sha256.Sum256(contents)
	if hex.EncodeToString(digest[:]) != match[1] || int64(len(contents)) != item.Size {
		return fmt.Errorf("OCI blob does not match %s", item.Digest)
	}
	return nil
}

func blobPath(digest string) string { return "blobs/sha256/" + strings.TrimPrefix(digest, "sha256:") }

func writePlan(args []string) error {
	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	root := flags.String("root", "", "candidate root")
	output := flags.String("output", "", "subject plan")
	contractVersion := flags.String("contract-version", "", "require the complete release matrix for this version")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *root == "" || *output == "" {
		return errors.New("--root and --output are required")
	}
	var plans []plan
	for _, spec := range []struct{ directory, pattern, kind, mediaType string }{
		{"archives", "*.tar.gz", "archive", "application/gzip"},
		{"charts", "*.tgz", "chart", "application/vnd.cncf.helm.chart.content.v1.tar+gzip"},
		{"bundles", "*.tar.gz", "bundle", "application/gzip"},
		{"sboms", "*.spdx.json", "sbom", "application/spdx+json"},
		{"metadata", "*.json", "metadata", "application/json"},
	} {
		matches, err := filepath.Glob(filepath.Join(*root, spec.directory, spec.pattern))
		if err != nil {
			return err
		}
		for _, match := range matches {
			relative := filepath.ToSlash(filepath.Join(spec.directory, filepath.Base(match)))
			name := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(match), ".tar.gz"), ".tgz")
			if spec.kind == "sbom" {
				name = strings.TrimSuffix(name, ".spdx.json")
			}
			if spec.kind == "metadata" {
				name = strings.ReplaceAll(strings.TrimSuffix(name, ".json"), ".", "-")
			}
			if *contractVersion != "" && spec.kind == "chart" {
				name = strings.TrimSuffix(name, "_"+*contractVersion)
			}
			if *contractVersion != "" && spec.kind == "bundle" {
				name = strings.TrimSuffix(name, "_"+*contractVersion)
			}
			plans = append(plans, plan{Kind: spec.kind, Name: name, Locator: relative, Path: relative, MediaType: spec.mediaType})
		}
	}
	images, err := filepath.Glob(filepath.Join(*root, "oci", "images", "*", "layout", "index.json"))
	if err != nil {
		return err
	}
	for _, index := range images {
		image := filepath.Base(filepath.Dir(filepath.Dir(index)))
		blobs, err := readLayoutDir(filepath.Dir(index))
		if err != nil {
			return err
		}
		var top imageIndex
		if err := json.Unmarshal(blobs["index.json"], &top); err != nil {
			return err
		}
		rootDescriptor, _, platforms, err := selectImageSubjects(blobs, top)
		if err != nil {
			return err
		}
		rootPath := blobPath(rootDescriptor.Digest)
		if _, ok := blobs[rootPath]; !ok {
			rootPath = "index.json"
		}
		relative := filepath.ToSlash(filepath.Join("oci", "images", image, "layout", filepath.FromSlash(rootPath)))
		plans = append(plans, plan{Kind: "oci-index", Name: image, Locator: relative, Path: relative, MediaType: "application/vnd.oci.image.index.v1+json"})
		for _, arch := range []string{"amd64", "arm64"} {
			manifestRelative := blobPath(platforms[arch].Digest)
			manifest := filepath.Join(*root, "oci", "images", image, "layout", filepath.FromSlash(manifestRelative))
			if _, err := os.Stat(manifest); err != nil {
				return fmt.Errorf("%s: %w", image, err)
			}
			relative = filepath.ToSlash(filepath.Join("oci", "images", image, "layout", filepath.FromSlash(manifestRelative)))
			plans = append(plans, plan{Kind: "oci-platform-manifest", Name: image, Locator: relative, Path: relative, Platform: "linux/" + arch, MediaType: "application/vnd.oci.image.manifest.v1+json"})
		}
	}
	charts, err := filepath.Glob(filepath.Join(*root, "oci", "charts", "*", "layout", "index.json"))
	if err != nil {
		return err
	}
	for _, index := range charts {
		name := filepath.Base(filepath.Dir(filepath.Dir(index)))
		blobs, err := readLayoutDir(filepath.Dir(index))
		if err != nil {
			return err
		}
		var top imageIndex
		if err := json.Unmarshal(blobs["index.json"], &top); err != nil {
			return err
		}
		if err := validateLayoutClosure(blobs, top); err != nil {
			return err
		}
		rootDescriptor, _, err := selectArtifactSubject(blobs, top)
		if err != nil {
			return err
		}
		relative := filepath.ToSlash(filepath.Join("oci", "charts", name, "layout", filepath.FromSlash(blobPath(rootDescriptor.Digest))))
		plans = append(plans, plan{Kind: "oci-manifest", Name: name, Locator: relative, Path: relative, MediaType: rootDescriptor.MediaType})
	}
	if len(plans) == 0 {
		return errors.New("candidate has no subjects")
	}
	if *contractVersion != "" {
		if !versionPattern.MatchString(*contractVersion) {
			return errors.New("--contract-version must be semantic")
		}
		if err := validateContractPlans(plans, *contractVersion); err != nil {
			return err
		}
	}
	sort.Slice(plans, func(i, j int) bool {
		return plans[i].Kind+"\x00"+plans[i].Name+"\x00"+plans[i].Platform < plans[j].Kind+"\x00"+plans[j].Name+"\x00"+plans[j].Platform
	})
	contents, err := json.Marshal(plans)
	if err != nil {
		return err
	}
	return atomicWrite(*output, append(contents, '\n'))
}

func validateContractPlans(plans []plan, version string) error {
	expected := make(map[string]struct{})
	add := func(kind, locator, platform string) {
		expected[kind+"\x00"+locator+"\x00"+platform] = struct{}{}
	}
	expectedNames := map[string]string{}
	addNamed := func(kind, name, locator, platform string) {
		add(kind, locator, platform)
		expectedNames[kind+"\x00"+locator+"\x00"+platform] = name
	}
	for _, binary := range []string{"xisnove-server", "xisnove-ui", "xisnove-agent", "xisnove-operator"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name := binary + "_" + version + "_linux_" + arch
			addNamed("archive", name, "archives/"+name+".tar.gz", "")
		}
	}
	for _, osName := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name := "xisnove_" + version + "_" + osName + "_" + arch
			addNamed("archive", name, "archives/"+name+".tar.gz", "")
		}
	}
	for _, chart := range []string{"xisnove", "xisnove-edge"} {
		addNamed("chart", chart, "charts/"+chart+"_"+version+".tgz", "")
	}
	for _, bundle := range []string{"xisnove-source", "xisnove-deployment"} {
		addNamed("bundle", bundle, "bundles/"+bundle+"_"+version+".tar.gz", "")
	}
	addNamed("metadata", "licenses", "metadata/licenses.json", "")
	addNamed("metadata", "toolchain-lock", "metadata/toolchain.lock.json", "")
	imageKinds := map[string]map[string]int{}
	chartManifests := map[string]int{}
	for _, item := range plans {
		if item.Kind == "oci-index" || item.Kind == "oci-platform-manifest" {
			if !strings.HasPrefix(item.Locator, "oci/images/"+item.Name+"/layout/blobs/sha256/") {
				return fmt.Errorf("%s image subject locator is not digest-addressed", item.Name)
			}
			addNamed(item.Kind, item.Name, item.Locator, item.Platform)
			if imageKinds[item.Name] == nil {
				imageKinds[item.Name] = map[string]int{}
			}
			imageKinds[item.Name][item.Kind]++
		} else if item.Kind == "oci-manifest" {
			if !strings.HasPrefix(item.Locator, "oci/charts/"+item.Name+"/layout/blobs/sha256/") {
				return fmt.Errorf("%s chart subject locator is not digest-addressed", item.Name)
			}
			addNamed(item.Kind, item.Name, item.Locator, "")
			chartManifests[item.Name]++
		}
	}
	for _, image := range []string{"xisnove-server", "xisnove-ui", "xisnove-agent", "xisnove-operator"} {
		if imageKinds[image]["oci-index"] != 1 || imageKinds[image]["oci-platform-manifest"] != 2 {
			return fmt.Errorf("%s OCI subject set must contain one index and two platform manifests", image)
		}
		delete(imageKinds, image)
	}
	if len(imageKinds) != 0 {
		return fmt.Errorf("unexpected OCI image subjects: %v", imageKinds)
	}
	for _, chart := range []string{"xisnove", "xisnove-edge"} {
		if chartManifests[chart] != 1 {
			return fmt.Errorf("%s chart OCI manifest missing or duplicated", chart)
		}
		delete(chartManifests, chart)
	}
	if len(chartManifests) != 0 {
		return fmt.Errorf("unexpected chart OCI subjects: %v", chartManifests)
	}
	primary := append([]plan(nil), plans...)
	for _, item := range primary {
		switch item.Kind {
		case "archive", "chart", "oci-index", "oci-manifest", "oci-platform-manifest":
			if err := validateSubjectName(item.Name); err != nil {
				return fmt.Errorf("%s: %w", item.Kind, err)
			}
			formula := item.Kind + "--" + item.Name
			if item.Platform != "" {
				formula += "--" + strings.ReplaceAll(item.Platform, "/", "-")
			}
			addNamed("sbom", formula, "sboms/"+formula+".spdx.json", "")
		}
	}
	actual := make(map[string]struct{}, len(plans))
	for _, item := range plans {
		key := item.Kind + "\x00" + item.Locator + "\x00" + item.Platform
		if _, duplicate := actual[key]; duplicate {
			return fmt.Errorf("duplicate candidate subject %s", item.Locator)
		}
		actual[key] = struct{}{}
		if want, ok := expectedNames[key]; ok && item.Name != want {
			return fmt.Errorf("subject %s name=%q want=%q", item.Locator, item.Name, want)
		}
	}
	var missing, extra []string
	for key := range expected {
		if _, ok := actual[key]; !ok {
			missing = append(missing, strings.ReplaceAll(key, "\x00", "/"))
		}
	}
	for key := range actual {
		if _, ok := expected[key]; !ok {
			extra = append(extra, strings.ReplaceAll(key, "\x00", "/"))
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		return fmt.Errorf("candidate subject closure mismatch: missing=%v extra=%v", missing, extra)
	}
	if len(plans) != 64 {
		return fmt.Errorf("candidate subject count=%d want=64", len(plans))
	}
	return nil
}

func writeChecksums(args []string) error {
	flags := flag.NewFlagSet("checksums", flag.ContinueOnError)
	root := flags.String("root", "", "candidate root")
	subjectsPath := flags.String("subjects", "", "subject plan")
	manifest := flags.String("manifest", "", "canonical manifest")
	output := flags.String("output", "", "checksum listing")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *root == "" || *subjectsPath == "" || *manifest == "" || *output == "" {
		return errors.New("--root, --subjects, --manifest, and --output are required")
	}
	data, err := os.ReadFile(*subjectsPath)
	if err != nil {
		return err
	}
	var plans []plan
	if err := json.Unmarshal(data, &plans); err != nil {
		return err
	}
	paths := make([]string, 0, len(plans)+1)
	for _, item := range plans {
		paths = append(paths, item.Path)
	}
	paths = append(paths, *manifest)
	sort.Strings(paths)
	var builder strings.Builder
	for _, relative := range paths {
		clean, err := cleanRelative(relative)
		if err != nil {
			return err
		}
		contents, err := os.ReadFile(filepath.Join(*root, filepath.FromSlash(clean)))
		if err != nil {
			return err
		}
		digest := sha256.Sum256(contents)
		fmt.Fprintf(&builder, "%x  %s\n", digest, clean)
	}
	return atomicWrite(*output, []byte(builder.String()))
}

func readLock(args []string) error {
	flags := flag.NewFlagSet("lock", flag.ContinueOnError)
	file := flags.String("file", "", "toolchain lock")
	kind := flags.String("kind", "", "tools or images")
	name := flags.String("name", "", "entry name")
	platform := flags.String("platform", "", "checksum platform")
	field := flags.String("field", "version", "version, checksum, or digest")
	if err := flags.Parse(args); err != nil {
		return err
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	var lock struct {
		Tools []struct {
			Name, Version string
			Checksums     map[string]string `json:"checksums"`
		} `json:"tools"`
		Images []struct{ Name, Use, Digest string } `json:"images"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return err
	}
	switch *kind {
	case "tools":
		for _, tool := range lock.Tools {
			if tool.Name != *name {
				continue
			}
			switch *field {
			case "version":
				fmt.Print(tool.Version)
				return nil
			case "checksum":
				value, ok := tool.Checksums[*platform]
				if !ok {
					return errors.New("checksum platform missing")
				}
				fmt.Print(value)
				return nil
			}
		}
	case "images":
		for _, image := range lock.Images {
			if image.Name == *name && (*platform == "" || image.Use == *platform) && *field == "digest" {
				fmt.Print(image.Digest)
				return nil
			}
		}
	}
	return errors.New("lock value not found")
}

func parseEpoch(value string) (time.Time, error) {
	epoch, err := strconv.ParseInt(value, 10, 64)
	if err != nil || epoch < 1 {
		return time.Time{}, errors.New("source date epoch must be positive")
	}
	return time.Unix(epoch, 0).UTC(), nil
}

func atomicWrite(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, contents, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func cleanRelative(value string) (string, error) {
	if value == "" || filepath.IsAbs(value) {
		return "", errors.New("path must be relative")
	}
	clean := filepath.ToSlash(filepath.Clean(value))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("path escapes root")
	}
	return clean, nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
