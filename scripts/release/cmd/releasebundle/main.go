// Command releasebundle builds and verifies deterministic Xisnove release metadata.
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

const repositoryIdentity = "github.com/araihu/xisnove"

var (
	commitPattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)
	digestPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }
func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

type subjectPlan struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Locator   string `json:"locator"`
	Path      string `json:"path"`
	Platform  string `json:"platform,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
}

type subject struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Locator   string `json:"locator"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	Platform  string `json:"platform,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
}

type candidateManifest struct {
	SchemaVersion   int       `json:"schemaVersion"`
	Repository      string    `json:"repository"`
	Commit          string    `json:"commit"`
	Version         string    `json:"version"`
	SourceDateEpoch int64     `json:"sourceDateEpoch"`
	Subjects        []subject `json:"subjects"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "releasebundle:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("expected command: bundle, manifest, verify, normalize-sbom, or licenses")
	}
	switch args[0] {
	case "bundle":
		return runBundle(args[1:])
	case "manifest":
		return runManifest(args[1:])
	case "verify":
		return runVerify(args[1:])
	case "normalize-sbom":
		return runNormalizeSBOM(args[1:])
	case "licenses":
		return runLicenses(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runBundle(args []string) error {
	flags := flag.NewFlagSet("bundle", flag.ContinueOnError)
	root := flags.String("root", "", "input root")
	output := flags.String("output", "", "output tar.gz")
	prefix := flags.String("prefix", "", "archive prefix")
	epochValue := flags.String("source-date-epoch", "", "Unix timestamp")
	trackedOnly := flags.Bool("tracked-only", false, "include only files present in the Git index")
	var includes stringList
	var excludes stringList
	flags.Var(&includes, "include", "relative path to include (repeatable)")
	flags.Var(&excludes, "exclude", "relative path to exclude (repeatable)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	epoch, err := parseEpoch(*epochValue)
	if err != nil {
		return err
	}
	if *root == "" || *output == "" || *prefix == "" || len(includes) == 0 {
		return errors.New("--root, --output, --prefix, and at least one --include are required")
	}
	if filepath.IsAbs(*prefix) || strings.Contains(filepath.ToSlash(*prefix), "/") || *prefix == "." || *prefix == ".." {
		return errors.New("--prefix must be one path segment")
	}
	var entries []string
	if *trackedOnly {
		entries, err = collectTrackedEntries(*root, includes, excludes)
	} else {
		entries, err = collectEntries(*root, includes, excludes, *output)
	}
	if err != nil {
		return err
	}
	return writeTarGzip(*root, *output, *prefix, epoch, entries)
}

func collectEntries(root string, includes, excludes []string, output string) ([]string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	excluded := make(map[string]struct{}, len(excludes))
	for _, value := range excludes {
		clean, err := cleanRelative(value)
		if err != nil {
			return nil, fmt.Errorf("exclude %q: %w", value, err)
		}
		excluded[clean] = struct{}{}
	}
	for _, include := range includes {
		clean := "."
		if include != "." {
			var err error
			clean, err = cleanRelative(include)
			if err != nil {
				return nil, fmt.Errorf("include %q: %w", include, err)
			}
		}
		start := filepath.Join(root, filepath.FromSlash(clean))
		if _, err := os.Lstat(start); err != nil {
			return nil, fmt.Errorf("include %q: %w", include, err)
		}
		err = filepath.WalkDir(start, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			if relative != "." && isExcluded(relative, excluded) {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if path == output {
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("refuse non-regular bundle input %s", path)
			}
			seen[relative] = struct{}{}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	entries := make([]string, 0, len(seen))
	for entry := range seen {
		entries = append(entries, entry)
	}
	sort.Strings(entries)
	return entries, nil
}

func collectTrackedEntries(root string, includes, excludes []string) ([]string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	wanted := make(map[string]struct{}, len(includes))
	for _, include := range includes {
		if include == "." {
			wanted[include] = struct{}{}
			continue
		}
		clean, err := cleanRelative(include)
		if err != nil {
			return nil, fmt.Errorf("include %q: %w", include, err)
		}
		wanted[clean] = struct{}{}
	}
	excluded := make(map[string]struct{}, len(excludes))
	for _, value := range excludes {
		clean, err := cleanRelative(value)
		if err != nil {
			return nil, fmt.Errorf("exclude %q: %w", value, err)
		}
		excluded[clean] = struct{}{}
	}
	command := exec.Command("git", "-C", root, "ls-files", "-z")
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("list Git-tracked bundle inputs: %w", err)
	}
	seen := make(map[string]struct{})
	matched := make(map[string]bool, len(wanted))
	for _, raw := range strings.Split(string(output), "\x00") {
		if raw == "" {
			continue
		}
		path := filepath.ToSlash(raw)
		if isExcluded(path, excluded) {
			continue
		}
		for include := range wanted {
			if include != "." && path != include && !strings.HasPrefix(path, include+"/") {
				continue
			}
			info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path)))
			if err != nil {
				return nil, err
			}
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("refuse non-regular tracked bundle input %s", path)
			}
			seen[path] = struct{}{}
			matched[include] = true
			break
		}
	}
	for include := range wanted {
		if !matched[include] {
			return nil, fmt.Errorf("include %q has no Git-tracked files", include)
		}
	}
	entries := make([]string, 0, len(seen))
	for entry := range seen {
		entries = append(entries, entry)
	}
	sort.Strings(entries)
	return entries, nil
}

func isExcluded(path string, excluded map[string]struct{}) bool {
	for value := range excluded {
		if path == value || strings.HasPrefix(path, value+"/") {
			return true
		}
	}
	return false
}

func writeTarGzip(root, output, prefix string, epoch time.Time, entries []string) error {
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
	gz.Header.ModTime = epoch
	gz.Header.OS = 255
	writer := tar.NewWriter(gz)
	for _, entry := range entries {
		path := filepath.Join(root, filepath.FromSlash(entry))
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		mode := int64(0o644)
		if info.Mode()&0o111 != 0 {
			mode = 0o755
		}
		header := &tar.Header{
			Name:       filepath.ToSlash(filepath.Join(prefix, filepath.FromSlash(entry))),
			Mode:       mode,
			Uid:        0,
			Gid:        0,
			Size:       info.Size(),
			ModTime:    epoch,
			AccessTime: time.Time{},
			ChangeTime: time.Time{},
			Typeflag:   tar.TypeReg,
			Format:     tar.FormatPAX,
		}
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, input)
		closeErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
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

func runManifest(args []string) error {
	flags := flag.NewFlagSet("manifest", flag.ContinueOnError)
	root := flags.String("root", "", "candidate root")
	repository := flags.String("repository", "", "repository identity")
	commit := flags.String("commit", "", "Git commit")
	version := flags.String("version", "", "release version without v")
	epochValue := flags.String("source-date-epoch", "", "Unix timestamp")
	subjectsPath := flags.String("subjects", "", "subject plan JSON")
	output := flags.String("output", "", "manifest output")
	checksum := flags.String("checksum", "", "detached checksum output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	epoch, err := parseEpoch(*epochValue)
	if err != nil {
		return err
	}
	if *repository != repositoryIdentity {
		return fmt.Errorf("repository must be %q", repositoryIdentity)
	}
	if !commitPattern.MatchString(*commit) {
		return errors.New("commit must be 40 lowercase hexadecimal characters")
	}
	if !versionPattern.MatchString(*version) {
		return errors.New("version does not match release contract")
	}
	if *root == "" || *subjectsPath == "" || *output == "" || *checksum == "" {
		return errors.New("--root, --subjects, --output, and --checksum are required")
	}
	plansData, err := os.ReadFile(*subjectsPath)
	if err != nil {
		return err
	}
	var plans []subjectPlan
	decoder := json.NewDecoder(strings.NewReader(string(plansData)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plans); err != nil {
		return fmt.Errorf("decode subjects: %w", err)
	}
	manifestName := filepath.Base(*output)
	subjects := make([]subject, 0, len(plans))
	seen := make(map[string]struct{})
	for _, plan := range plans {
		if err := validatePlan(plan, manifestName); err != nil {
			return err
		}
		key := plan.Kind + "\x00" + plan.Name + "\x00" + plan.Platform
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate subject %s/%s/%s", plan.Kind, plan.Name, plan.Platform)
		}
		seen[key] = struct{}{}
		path := filepath.Join(*root, filepath.FromSlash(plan.Path))
		digest, size, err := digestFile(path)
		if err != nil {
			return fmt.Errorf("subject %s: %w", plan.Name, err)
		}
		subjects = append(subjects, subject{Kind: plan.Kind, Name: plan.Name, Locator: plan.Locator, SHA256: digest, Size: size, Platform: plan.Platform, MediaType: plan.MediaType})
	}
	sort.Slice(subjects, func(i, j int) bool {
		left, right := subjects[i], subjects[j]
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		return left.Platform < right.Platform
	})
	manifest := candidateManifest{SchemaVersion: 1, Repository: *repository, Commit: *commit, Version: *version, SourceDateEpoch: epoch.Unix(), Subjects: subjects}
	contents, err := marshalCanonical(manifest)
	if err != nil {
		return err
	}
	if err := atomicWrite(*output, contents, 0o644); err != nil {
		return err
	}
	digest := sha256.Sum256(contents)
	line := fmt.Sprintf("%s  %s\n", hex.EncodeToString(digest[:]), filepath.Base(*output))
	return atomicWrite(*checksum, []byte(line), 0o644)
}

func validatePlan(plan subjectPlan, manifestName string) error {
	allowed := map[string]bool{"archive": true, "chart": true, "bundle": true, "sbom": true, "metadata": true, "oci-index": true, "oci-platform-manifest": true}
	if !allowed[plan.Kind] {
		return fmt.Errorf("invalid subject kind %q", plan.Kind)
	}
	if plan.Name == "" || plan.Locator == "" || plan.Path == "" {
		return errors.New("subject kind, name, locator, and path are required")
	}
	if _, err := cleanRelative(plan.Locator); err != nil {
		return fmt.Errorf("subject locator %q: %w", plan.Locator, err)
	}
	if _, err := cleanRelative(plan.Path); err != nil {
		return fmt.Errorf("subject path %q: %w", plan.Path, err)
	}
	if filepath.ToSlash(filepath.Clean(plan.Path)) != filepath.ToSlash(filepath.Clean(plan.Locator)) {
		return errors.New("subject path and locator must identify the same candidate file")
	}
	if filepath.Base(plan.Locator) == manifestName || strings.HasSuffix(plan.Locator, manifestName+".sha256") {
		return errors.New("canonical manifest cannot name itself or its detached checksum")
	}
	if plan.Kind == "oci-platform-manifest" && !regexp.MustCompile(`^linux/(amd64|arm64)$`).MatchString(plan.Platform) {
		return errors.New("OCI platform manifest requires linux/amd64 or linux/arm64 platform")
	}
	return nil
}

func runVerify(args []string) error {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	root := flags.String("root", "", "consumer directory")
	manifestPath := flags.String("manifest", "", "relative manifest path")
	checksumPath := flags.String("checksum", "", "relative checksum path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *root == "" || *manifestPath == "" || *checksumPath == "" {
		return errors.New("--root, --manifest, and --checksum are required")
	}
	manifestRelative, err := cleanRelative(*manifestPath)
	if err != nil {
		return err
	}
	checksumRelative, err := cleanRelative(*checksumPath)
	if err != nil {
		return err
	}
	manifestData, err := os.ReadFile(filepath.Join(*root, filepath.FromSlash(manifestRelative)))
	if err != nil {
		return err
	}
	checksumData, err := os.ReadFile(filepath.Join(*root, filepath.FromSlash(checksumRelative)))
	if err != nil {
		return err
	}
	fields := strings.Fields(string(checksumData))
	if len(fields) != 2 || fields[1] != filepath.Base(manifestRelative) || !digestPattern.MatchString(fields[0]) {
		return errors.New("invalid detached manifest checksum")
	}
	digest := sha256.Sum256(manifestData)
	if hex.EncodeToString(digest[:]) != fields[0] {
		return errors.New("manifest checksum mismatch")
	}
	var manifest candidateManifest
	decoder := json.NewDecoder(strings.NewReader(string(manifestData)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}
	canonical, err := marshalCanonical(manifest)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, manifestData) {
		return errors.New("manifest is not canonical JSON")
	}
	if err := validateManifestIdentity(manifest); err != nil {
		return err
	}
	previous := ""
	seen := make(map[string]struct{})
	seenLocators := make(map[string]struct{})
	for _, item := range manifest.Subjects {
		if err := validateSubject(item); err != nil {
			return err
		}
		key := item.Kind + "\x00" + item.Name + "\x00" + item.Platform
		if key < previous {
			return errors.New("manifest subjects are not canonically ordered")
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate manifest subject %s/%s/%s", item.Kind, item.Name, item.Platform)
		}
		seen[key] = struct{}{}
		if _, exists := seenLocators[item.Locator]; exists {
			return fmt.Errorf("duplicate manifest locator %s", item.Locator)
		}
		seenLocators[item.Locator] = struct{}{}
		previous = key
		if filepath.Base(item.Locator) == filepath.Base(manifestRelative) || filepath.Base(item.Locator) == filepath.Base(checksumRelative) {
			return errors.New("manifest recursively names itself or its checksum")
		}
		locator, err := cleanRelative(item.Locator)
		if err != nil {
			return err
		}
		actualDigest, actualSize, err := digestFile(filepath.Join(*root, filepath.FromSlash(locator)))
		if err != nil {
			return fmt.Errorf("verify %s: %w", item.Locator, err)
		}
		if actualDigest != item.SHA256 || actualSize != item.Size {
			return fmt.Errorf("subject mismatch for %s", item.Locator)
		}
	}
	return nil
}

func validateSubject(item subject) error {
	allowed := map[string]bool{"archive": true, "chart": true, "bundle": true, "sbom": true, "metadata": true, "oci-index": true, "oci-platform-manifest": true}
	if !allowed[item.Kind] || item.Name == "" || item.Locator == "" || !digestPattern.MatchString(item.SHA256) || item.Size < 0 {
		return fmt.Errorf("manifest subject %q violates release contract", item.Name)
	}
	if item.Platform != "" && item.Platform != "linux/amd64" && item.Platform != "linux/arm64" {
		return fmt.Errorf("manifest subject %q has invalid platform", item.Name)
	}
	if item.Kind == "oci-platform-manifest" && item.Platform == "" {
		return fmt.Errorf("OCI platform manifest %q lacks platform", item.Name)
	}
	return nil
}

func validateManifestIdentity(manifest candidateManifest) error {
	if manifest.SchemaVersion != 1 || manifest.Repository != repositoryIdentity || !commitPattern.MatchString(manifest.Commit) || !versionPattern.MatchString(manifest.Version) || manifest.SourceDateEpoch < 1 || len(manifest.Subjects) == 0 {
		return errors.New("manifest identity violates release contract")
	}
	return nil
}

func runNormalizeSBOM(args []string) error {
	flags := flag.NewFlagSet("normalize-sbom", flag.ContinueOnError)
	input := flags.String("input", "", "raw SPDX JSON")
	output := flags.String("output", "", "normalized SPDX JSON")
	subjectDigest := flags.String("subject-sha256", "", "subject SHA-256")
	epochValue := flags.String("source-date-epoch", "", "Unix timestamp")
	if err := flags.Parse(args); err != nil {
		return err
	}
	epoch, err := parseEpoch(*epochValue)
	if err != nil {
		return err
	}
	if *input == "" || *output == "" || !digestPattern.MatchString(*subjectDigest) {
		return errors.New("--input, --output, and valid --subject-sha256 are required")
	}
	contents, err := os.ReadFile(*input)
	if err != nil {
		return err
	}
	var document map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return err
	}
	document["documentNamespace"] = "https://xisnove.dev/sbom/" + *subjectDigest
	creation, ok := document["creationInfo"].(map[string]any)
	if !ok {
		creation = make(map[string]any)
		document["creationInfo"] = creation
	}
	creation["created"] = epoch.Format(time.RFC3339)
	normalized, err := marshalCanonical(document)
	if err != nil {
		return err
	}
	return atomicWrite(*output, normalized, 0o644)
}

type licensePolicy struct {
	SchemaVersion int      `json:"schemaVersion"`
	Allow         []string `json:"allow"`
	Deny          []string `json:"deny"`
}

type licenseRecord struct {
	Package    string `json:"package"`
	Version    string `json:"version,omitempty"`
	License    string `json:"license"`
	Status     string `json:"status"`
	SourceSBOM string `json:"sourceSbom"`
}

type licenseInventory struct {
	SchemaVersion int             `json:"schemaVersion"`
	Records       []licenseRecord `json:"records"`
}

func runLicenses(args []string) error {
	flags := flag.NewFlagSet("licenses", flag.ContinueOnError)
	policyPath := flags.String("policy", "", "license policy JSON")
	output := flags.String("output", "", "inventory output")
	var sboms stringList
	flags.Var(&sboms, "sbom", "SPDX JSON input (repeatable)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *policyPath == "" || *output == "" || len(sboms) == 0 {
		return errors.New("--policy, --output, and at least one --sbom are required")
	}
	policyData, err := os.ReadFile(*policyPath)
	if err != nil {
		return err
	}
	var policy licensePolicy
	if err := decodeStrict(policyData, &policy); err != nil {
		return fmt.Errorf("decode policy: %w", err)
	}
	if policy.SchemaVersion != 1 || len(policy.Allow) == 0 {
		return errors.New("license policy must have schemaVersion 1 and non-empty allow list")
	}
	allow := toSet(policy.Allow)
	deny := toSet(policy.Deny)
	var records []licenseRecord
	for _, sbomPath := range sboms {
		data, err := os.ReadFile(sbomPath)
		if err != nil {
			return err
		}
		var sbom struct {
			Packages []struct {
				Name             string `json:"name"`
				Version          string `json:"versionInfo"`
				LicenseDeclared  string `json:"licenseDeclared"`
				LicenseConcluded string `json:"licenseConcluded"`
			} `json:"packages"`
		}
		if err := json.Unmarshal(data, &sbom); err != nil {
			return fmt.Errorf("decode SBOM %s: %w", sbomPath, err)
		}
		for _, pkg := range sbom.Packages {
			license := preferredLicense(pkg.LicenseConcluded, pkg.LicenseDeclared)
			status := "unknown"
			if _, denied := deny[license]; denied {
				status = "denied"
			} else if _, allowed := allow[license]; allowed {
				status = "allowed"
			}
			records = append(records, licenseRecord{Package: pkg.Name, Version: pkg.Version, License: license, Status: status, SourceSBOM: filepath.Base(sbomPath)})
		}
	}
	sort.Slice(records, func(i, j int) bool {
		left, right := records[i], records[j]
		if left.Package != right.Package {
			return left.Package < right.Package
		}
		if left.Version != right.Version {
			return left.Version < right.Version
		}
		if left.License != right.License {
			return left.License < right.License
		}
		return left.SourceSBOM < right.SourceSBOM
	})
	contents, err := marshalCanonical(licenseInventory{SchemaVersion: 1, Records: records})
	if err != nil {
		return err
	}
	if err := atomicWrite(*output, contents, 0o644); err != nil {
		return err
	}
	for _, record := range records {
		switch record.Status {
		case "denied":
			return fmt.Errorf("denied license %q for %s", record.License, record.Package)
		case "unknown":
			return fmt.Errorf("unknown license %q for %s", record.License, record.Package)
		}
	}
	return nil
}

func preferredLicense(concluded, declared string) string {
	if concluded != "" && concluded != "NOASSERTION" && concluded != "NONE" {
		return concluded
	}
	if declared != "" {
		return declared
	}
	return "NOASSERTION"
}

func toSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func cleanRelative(value string) (string, error) {
	if value == "" || filepath.IsAbs(value) {
		return "", errors.New("path must be non-empty and relative")
	}
	clean := filepath.ToSlash(filepath.Clean(value))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("path escapes root")
	}
	return clean, nil
}

func parseEpoch(value string) (time.Time, error) {
	epoch, err := strconv.ParseInt(value, 10, 64)
	if err != nil || epoch < 1 {
		return time.Time{}, errors.New("source date epoch must be a positive Unix timestamp")
	}
	return time.Unix(epoch, 0).UTC(), nil
}

func digestFile(path string) (string, int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, errors.New("subject must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func marshalCanonical(value any) ([]byte, error) {
	var buffer strings.Builder
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return []byte(buffer.String()), nil
}

func decodeStrict(contents []byte, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	return decoder.Decode(destination)
}

func atomicWrite(path string, contents []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, contents, mode); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}
