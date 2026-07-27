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
		return errors.New("expected command: bundle, manifest, verify, normalize-sbom, licenses, or propose-ubuntu-lock")
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
	case "propose-ubuntu-lock":
		return runProposeUbuntuLock(args[1:])
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
	allowed := map[string]bool{"archive": true, "chart": true, "bundle": true, "sbom": true, "metadata": true, "oci-index": true, "oci-manifest": true, "oci-platform-manifest": true}
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
	allowed := map[string]bool{"archive": true, "chart": true, "bundle": true, "sbom": true, "metadata": true, "oci-index": true, "oci-manifest": true, "oci-platform-manifest": true}
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
	var firstPartyDigests stringList
	flags.Var(&firstPartyDigests, "first-party-sha256", "additional exact first-party payload SHA-256 (repeatable)")
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
	for _, digest := range firstPartyDigests {
		if !digestPattern.MatchString(digest) {
			return errors.New("--first-party-sha256 must be a valid SHA-256")
		}
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
	applyFirstPartyLicense(document, append([]string{*subjectDigest}, firstPartyDigests...))
	normalized, err := marshalCanonical(document)
	if err != nil {
		return err
	}
	return atomicWrite(*output, normalized, 0o644)
}

func applyFirstPartyLicense(document map[string]any, firstPartyDigests []string) {
	packages, _ := document["packages"].([]any)
	for _, value := range packages {
		pkg, _ := value.(map[string]any)
		if !packageHasFirstPartyPURL(pkg) && !packageHasAnySHA256(pkg, firstPartyDigests) {
			continue
		}
		declared, _ := pkg["licenseDeclared"].(string)
		concluded, _ := pkg["licenseConcluded"].(string)
		if !unknownLicense(declared) || !unknownLicense(concluded) {
			continue
		}
		pkg["licenseDeclared"] = "Apache-2.0"
		pkg["licenseConcluded"] = "Apache-2.0"
	}
}

func packageHasAnySHA256(pkg map[string]any, digests []string) bool {
	for _, digest := range digests {
		if packageHasSHA256(pkg, digest) {
			return true
		}
	}
	return false
}

func packageHasFirstPartyPURL(pkg map[string]any) bool {
	const prefix = "pkg:golang/github.com/araihu/xisnove"
	for _, purl := range packagePURLs(pkg) {
		if purl == prefix || strings.HasPrefix(purl, prefix+"/") || strings.HasPrefix(purl, prefix+"@") {
			return true
		}
	}
	return false
}

func packagePURLs(pkg map[string]any) []string {
	references, _ := pkg["externalRefs"].([]any)
	var purls []string
	for _, value := range references {
		reference, _ := value.(map[string]any)
		referenceType, _ := reference["referenceType"].(string)
		locator, _ := reference["referenceLocator"].(string)
		if referenceType == "purl" && locator != "" {
			purls = append(purls, locator)
		}
	}
	return purls
}

func packageHasSHA256(pkg map[string]any, subjectDigest string) bool {
	checksums, _ := pkg["checksums"].([]any)
	for _, value := range checksums {
		checksum, _ := value.(map[string]any)
		algorithm, _ := checksum["algorithm"].(string)
		digest, _ := checksum["checksumValue"].(string)
		if strings.EqualFold(algorithm, "SHA256") && digest == subjectDigest {
			return true
		}
	}
	return false
}

func unknownLicense(value string) bool {
	return value == "" || value == "NOASSERTION" || value == "NONE"
}

type licenseProfile struct {
	Allow     []string          `json:"allow"`
	Deny      []string          `json:"deny"`
	Overrides []licenseOverride `json:"overrides,omitempty"`
}

type licenseOverride struct {
	PURL            string   `json:"purl"`
	ReportedLicense string   `json:"reportedLicense"`
	ResolvedLicense string   `json:"resolvedLicense"`
	EvidenceFile    string   `json:"evidenceFile"`
	EvidenceSHA256  string   `json:"evidenceSHA256"`
	Obligations     []string `json:"obligations"`
}

type ubuntuLicenseProfile struct {
	Distro   string `json:"distro"`
	Snapshot string `json:"snapshot"`
	Lock     string `json:"lock"`
}

type licensePolicy struct {
	SchemaVersion int                  `json:"schemaVersion"`
	GlobalDeny    []string             `json:"globalDeny,omitempty"`
	Default       licenseProfile       `json:"default"`
	Golang        licenseProfile       `json:"golang"`
	Ubuntu        ubuntuLicenseProfile `json:"ubuntu"`
}

type ubuntuPackageApproval struct {
	PURL                    string   `json:"purl"`
	PackageVerificationCode string   `json:"packageVerificationCode"`
	ReportedLicense         string   `json:"reportedLicense"`
	ResolvedLicense         string   `json:"resolvedLicense"`
	EvidenceSHA256          string   `json:"evidenceSHA256"`
	Obligations             []string `json:"obligations,omitempty"`
}

type ubuntuPackageLock struct {
	SchemaVersion int                     `json:"schemaVersion"`
	Packages      []ubuntuPackageApproval `json:"packages"`
}

func runProposeUbuntuLock(args []string) error {
	flags := flag.NewFlagSet("propose-ubuntu-lock", flag.ContinueOnError)
	output := flags.String("output", "", "proposed Ubuntu lock output")
	var sboms stringList
	flags.Var(&sboms, "sbom", "SPDX JSON input (repeatable)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *output == "" || len(sboms) == 0 {
		return errors.New("--output and at least one --sbom are required")
	}
	proposals := map[string]ubuntuPackageApproval{}
	for _, sbomPath := range sboms {
		contents, err := os.ReadFile(sbomPath)
		if err != nil {
			return err
		}
		var sbom struct {
			Packages []struct {
				Name             string `json:"name"`
				LicenseDeclared  string `json:"licenseDeclared"`
				LicenseConcluded string `json:"licenseConcluded"`
				ExternalRefs     []struct {
					ReferenceType    string `json:"referenceType"`
					ReferenceLocator string `json:"referenceLocator"`
				} `json:"externalRefs"`
				PackageVerificationCode struct {
					Value string `json:"packageVerificationCodeValue"`
				} `json:"packageVerificationCode"`
			} `json:"packages"`
		}
		if err := json.Unmarshal(contents, &sbom); err != nil {
			return fmt.Errorf("decode SBOM %s: %w", sbomPath, err)
		}
		for _, pkg := range sbom.Packages {
			var purls []string
			for _, reference := range pkg.ExternalRefs {
				if reference.ReferenceType == "purl" && strings.HasPrefix(reference.ReferenceLocator, "pkg:deb/ubuntu/") {
					purls = append(purls, reference.ReferenceLocator)
				}
			}
			if len(purls) == 0 {
				continue
			}
			if len(purls) != 1 {
				return fmt.Errorf("Ubuntu package %s must have exactly one purl", pkg.Name)
			}
			if pkg.PackageVerificationCode.Value == "" {
				return fmt.Errorf("Ubuntu package %s missing packageVerificationCode", pkg.Name)
			}
			reported := preferredLicense(pkg.LicenseConcluded, pkg.LicenseDeclared)
			evidence := sha256.Sum256([]byte(purls[0] + "\x00" + pkg.PackageVerificationCode.Value + "\x00" + reported))
			resolved := reported
			if unknownLicense(resolved) {
				resolved = "LicenseRef-Ubuntu-" + hex.EncodeToString(evidence[:])
			}
			obligations := []string{"retain-license-notice"}
			if expressionHasCopyleft(resolved) {
				obligations = []string{"provide-corresponding-source-reference", "retain-license-notice"}
			}
			proposal := ubuntuPackageApproval{PURL: purls[0], PackageVerificationCode: pkg.PackageVerificationCode.Value, ReportedLicense: reported, ResolvedLicense: resolved, EvidenceSHA256: hex.EncodeToString(evidence[:]), Obligations: obligations}
			if previous, exists := proposals[purls[0]]; exists && !sameUbuntuApproval(previous, proposal) {
				return fmt.Errorf("conflicting Ubuntu package evidence for %q", purls[0])
			}
			proposals[purls[0]] = proposal
		}
	}
	packages := make([]ubuntuPackageApproval, 0, len(proposals))
	for _, proposal := range proposals {
		packages = append(packages, proposal)
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].PURL < packages[j].PURL })
	contents, err := marshalCanonical(ubuntuPackageLock{SchemaVersion: 1, Packages: packages})
	if err != nil {
		return err
	}
	return atomicWrite(*output, contents, 0o644)
}

func sameUbuntuApproval(left, right ubuntuPackageApproval) bool {
	return left.PURL == right.PURL &&
		left.PackageVerificationCode == right.PackageVerificationCode &&
		left.ReportedLicense == right.ReportedLicense &&
		left.ResolvedLicense == right.ResolvedLicense &&
		left.EvidenceSHA256 == right.EvidenceSHA256 &&
		strings.Join(left.Obligations, "\x00") == strings.Join(right.Obligations, "\x00")
}

func expressionHasCopyleft(expression string) bool {
	tokens, err := tokenizeSPDX(expression)
	if err != nil {
		return false
	}
	for _, token := range tokens {
		if strings.HasPrefix(token, "GPL-") || strings.HasPrefix(token, "LGPL-") || strings.HasPrefix(token, "AGPL-") {
			return true
		}
	}
	return false
}

type licenseRecord struct {
	Package         string   `json:"package"`
	Version         string   `json:"version,omitempty"`
	PURL            string   `json:"purl,omitempty"`
	Ecosystem       string   `json:"ecosystem"`
	ReportedLicense string   `json:"reportedLicense,omitempty"`
	License         string   `json:"license"`
	Status          string   `json:"status"`
	Rule            string   `json:"rule"`
	Obligations     []string `json:"obligations,omitempty"`
	SourceSBOM      string   `json:"sourceSbom"`
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
	if err := validateLicensePolicy(policy); err != nil {
		return err
	}
	ubuntuLock, err := loadUbuntuLock(filepath.Dir(*policyPath), policy.Ubuntu.Lock)
	if err != nil {
		return err
	}
	globalDeny := toSet(policy.GlobalDeny)
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
				ExternalRefs     []struct {
					ReferenceType    string `json:"referenceType"`
					ReferenceLocator string `json:"referenceLocator"`
				} `json:"externalRefs"`
				PackageVerificationCode struct {
					Value string `json:"packageVerificationCodeValue"`
				} `json:"packageVerificationCode"`
			} `json:"packages"`
		}
		if err := json.Unmarshal(data, &sbom); err != nil {
			return fmt.Errorf("decode SBOM %s: %w", sbomPath, err)
		}
		for _, pkg := range sbom.Packages {
			license := preferredLicense(pkg.LicenseConcluded, pkg.LicenseDeclared)
			purls := make([]string, 0, len(pkg.ExternalRefs))
			for _, reference := range pkg.ExternalRefs {
				if reference.ReferenceType == "purl" && reference.ReferenceLocator != "" {
					purls = append(purls, reference.ReferenceLocator)
				}
			}
			if len(purls) > 1 {
				return fmt.Errorf("package %s must have exactly one purl, got %d", pkg.Name, len(purls))
			}
			purl := ""
			if len(purls) == 1 {
				purl = purls[0]
			}
			ecosystem, status, rule, effectiveLicense, obligations, decisionErr := evaluatePackageLicense(
				filepath.Dir(*policyPath), policy, ubuntuLock, globalDeny,
				purl, pkg.PackageVerificationCode.Value, license,
			)
			if decisionErr != nil {
				return fmt.Errorf("%w for %s", decisionErr, pkg.Name)
			}
			reportedLicense := ""
			if effectiveLicense != license {
				reportedLicense = license
			}
			records = append(records, licenseRecord{Package: pkg.Name, Version: pkg.Version, PURL: purl, Ecosystem: ecosystem, ReportedLicense: reportedLicense, License: effectiveLicense, Status: status, Rule: rule, Obligations: obligations, SourceSBOM: filepath.Base(sbomPath)})
		}
	}
	sort.Slice(records, func(i, j int) bool {
		left, right := records[i], records[j]
		if left.PURL != right.PURL {
			return left.PURL < right.PURL
		}
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
	contents, err := marshalCanonical(licenseInventory{SchemaVersion: 2, Records: records})
	if err != nil {
		return err
	}
	if err := atomicWrite(*output, contents, 0o644); err != nil {
		return err
	}
	return nil
}

func validateLicensePolicy(policy licensePolicy) error {
	if policy.SchemaVersion != 2 || len(policy.Default.Allow) == 0 || len(policy.Golang.Allow) == 0 {
		return errors.New("license policy must have schemaVersion 2 and non-empty default and golang allow lists")
	}
	if policy.Ubuntu.Distro == "" || policy.Ubuntu.Snapshot == "" || policy.Ubuntu.Lock == "" {
		return errors.New("license policy Ubuntu distro, snapshot, and lock are required")
	}
	if filepath.IsAbs(policy.Ubuntu.Lock) || strings.Contains(filepath.ToSlash(filepath.Clean(policy.Ubuntu.Lock)), "../") {
		return errors.New("license policy Ubuntu lock must be a relative contained path")
	}
	return nil
}

func loadUbuntuLock(policyDir, lockPath string) (map[string]ubuntuPackageApproval, error) {
	contents, err := os.ReadFile(filepath.Join(policyDir, lockPath))
	if err != nil {
		return nil, fmt.Errorf("read Ubuntu license lock: %w", err)
	}
	var lock ubuntuPackageLock
	if err := decodeStrict(contents, &lock); err != nil {
		return nil, fmt.Errorf("decode Ubuntu license lock: %w", err)
	}
	if lock.SchemaVersion != 1 {
		return nil, errors.New("Ubuntu license lock must have schemaVersion 1")
	}
	approved := make(map[string]ubuntuPackageApproval, len(lock.Packages))
	for _, pkg := range lock.Packages {
		if pkg.PURL == "" || pkg.PackageVerificationCode == "" || pkg.ReportedLicense == "" || unknownLicense(pkg.ResolvedLicense) || !digestPattern.MatchString(pkg.EvidenceSHA256) {
			return nil, errors.New("Ubuntu license lock entries require purl, packageVerificationCode, reportedLicense, resolvedLicense, and evidenceSHA256")
		}
		if _, duplicate := approved[pkg.PURL]; duplicate {
			return nil, fmt.Errorf("duplicate Ubuntu license lock purl %q", pkg.PURL)
		}
		approved[pkg.PURL] = pkg
	}
	return approved, nil
}

func evaluatePackageLicense(policyDir string, policy licensePolicy, ubuntuLock map[string]ubuntuPackageApproval, globalDeny map[string]struct{}, purl, verificationCode, license string) (string, string, string, string, []string, error) {
	if deniedExpressionAtom(license, globalDeny) {
		return ecosystemForPURL(purl), "denied", "global-deny", license, nil, fmt.Errorf("denied license %q", license)
	}
	if strings.HasPrefix(purl, "pkg:deb/ubuntu/") {
		if !strings.Contains(purl, "distro="+policy.Ubuntu.Distro) {
			return "ubuntu", "unknown", "ubuntu-lock", license, nil, fmt.Errorf("Ubuntu package distro mismatch for %q", purl)
		}
		approved, ok := ubuntuLock[purl]
		if !ok {
			return "ubuntu", "unknown", "ubuntu-lock", license, nil, fmt.Errorf("Ubuntu package not locked: %q", purl)
		}
		if approved.PackageVerificationCode != verificationCode || approved.ReportedLicense != license {
			return "ubuntu", "unknown", "ubuntu-lock", license, nil, fmt.Errorf("Ubuntu lock mismatch for %q", purl)
		}
		if deniedExpressionAtom(approved.ResolvedLicense, globalDeny) {
			return "ubuntu", "denied", "global-deny", approved.ResolvedLicense, nil, fmt.Errorf("denied resolved license %q", approved.ResolvedLicense)
		}
		return "ubuntu", "allowed", "ubuntu-lock", approved.ResolvedLicense, approved.Obligations, nil
	}
	profile := policy.Default
	ecosystem := ecosystemForPURL(purl)
	rule := "default-expression"
	if ecosystem == "golang" {
		profile = policy.Golang
		rule = "golang-expression"
		for _, override := range profile.Overrides {
			if override.PURL == purl && override.ReportedLicense == license {
				if err := verifyLicenseEvidence(policyDir, override); err != nil {
					return ecosystem, "unknown", "golang-override", license, nil, err
				}
				if unknownLicense(override.ResolvedLicense) || len(override.Obligations) == 0 {
					return ecosystem, "unknown", "golang-override", license, nil, errors.New("license override requires resolvedLicense and obligations")
				}
				if deniedExpressionAtom(override.ResolvedLicense, globalDeny) {
					return ecosystem, "denied", "global-deny", override.ResolvedLicense, nil, fmt.Errorf("denied resolved license %q", override.ResolvedLicense)
				}
				return ecosystem, "allowed", "golang-override", override.ResolvedLicense, override.Obligations, nil
			}
		}
	}
	decision, err := evaluateSPDXExpression(license, toSet(profile.Allow), toSet(profile.Deny))
	if err != nil || decision == licenseUnknown {
		return ecosystem, "unknown", rule, license, nil, fmt.Errorf("unknown license %q", license)
	}
	if decision == licenseDenied {
		return ecosystem, "denied", rule, license, nil, fmt.Errorf("denied license %q", license)
	}
	return ecosystem, "allowed", rule, license, nil, nil
}

func verifyLicenseEvidence(policyDir string, override licenseOverride) error {
	if override.EvidenceFile == "" || !digestPattern.MatchString(override.EvidenceSHA256) {
		return errors.New("license override requires evidenceFile and lowercase SHA-256")
	}
	clean, err := cleanRelative(override.EvidenceFile)
	if err != nil {
		return fmt.Errorf("license override evidence: %w", err)
	}
	digest, _, err := digestFile(filepath.Join(policyDir, filepath.FromSlash(clean)))
	if err != nil {
		return fmt.Errorf("license override evidence: %w", err)
	}
	if digest != override.EvidenceSHA256 {
		return errors.New("license override evidence SHA-256 mismatch")
	}
	return nil
}

func ecosystemForPURL(purl string) string {
	switch {
	case strings.HasPrefix(purl, "pkg:golang/"):
		return "golang"
	case strings.HasPrefix(purl, "pkg:deb/ubuntu/"):
		return "ubuntu"
	case purl == "":
		return "artifact"
	default:
		return "default"
	}
}

type licenseDecision uint8

const (
	licenseUnknown licenseDecision = iota
	licenseAllowed
	licenseDenied
)

type spdxParser struct {
	tokens []string
	index  int
	allow  map[string]struct{}
	deny   map[string]struct{}
}

func evaluateSPDXExpression(expression string, allow, deny map[string]struct{}) (licenseDecision, error) {
	tokens, err := tokenizeSPDX(expression)
	if err != nil {
		return licenseUnknown, err
	}
	parser := spdxParser{tokens: tokens, allow: allow, deny: deny}
	decision, err := parser.parseOR()
	if err != nil {
		return licenseUnknown, err
	}
	if parser.index != len(tokens) {
		return licenseUnknown, errors.New("trailing SPDX tokens")
	}
	return decision, nil
}

func tokenizeSPDX(expression string) ([]string, error) {
	if unknownLicense(expression) {
		return nil, errors.New("unknown SPDX expression")
	}
	var tokens []string
	for index := 0; index < len(expression); {
		switch expression[index] {
		case ' ', '\t', '\r', '\n':
			index++
		case '(', ')':
			tokens = append(tokens, expression[index:index+1])
			index++
		default:
			start := index
			for index < len(expression) && !strings.ContainsRune(" ()\t\r\n", rune(expression[index])) {
				index++
			}
			tokens = append(tokens, expression[start:index])
		}
	}
	if len(tokens) == 0 {
		return nil, errors.New("empty SPDX expression")
	}
	return tokens, nil
}

func (parser *spdxParser) parseOR() (licenseDecision, error) {
	left, err := parser.parseAND()
	for err == nil && parser.consume("OR") {
		var right licenseDecision
		right, err = parser.parseAND()
		left = combineOR(left, right)
	}
	return left, err
}

func (parser *spdxParser) parseAND() (licenseDecision, error) {
	left, err := parser.parseWITH()
	for err == nil && parser.consume("AND") {
		var right licenseDecision
		right, err = parser.parseWITH()
		left = combineAND(left, right)
	}
	return left, err
}

func (parser *spdxParser) parseWITH() (licenseDecision, error) {
	left, err := parser.parsePrimary()
	if err == nil && parser.consume("WITH") {
		var right licenseDecision
		right, err = parser.parsePrimary()
		left = combineAND(left, right)
	}
	return left, err
}

func (parser *spdxParser) parsePrimary() (licenseDecision, error) {
	if parser.consume("(") {
		decision, err := parser.parseOR()
		if err != nil || !parser.consume(")") {
			return licenseUnknown, errors.New("unbalanced SPDX expression")
		}
		return decision, nil
	}
	if parser.index >= len(parser.tokens) || parser.tokens[parser.index] == ")" {
		return licenseUnknown, errors.New("missing SPDX license identifier")
	}
	identifier := parser.tokens[parser.index]
	parser.index++
	if _, denied := parser.deny[identifier]; denied {
		return licenseDenied, nil
	}
	if _, allowed := parser.allow[identifier]; allowed {
		return licenseAllowed, nil
	}
	return licenseUnknown, nil
}

func (parser *spdxParser) consume(token string) bool {
	if parser.index < len(parser.tokens) && parser.tokens[parser.index] == token {
		parser.index++
		return true
	}
	return false
}

func combineAND(left, right licenseDecision) licenseDecision {
	if left == licenseDenied || right == licenseDenied {
		return licenseDenied
	}
	if left == licenseAllowed && right == licenseAllowed {
		return licenseAllowed
	}
	return licenseUnknown
}

func combineOR(left, right licenseDecision) licenseDecision {
	if left == licenseAllowed || right == licenseAllowed {
		return licenseAllowed
	}
	if left == licenseDenied && right == licenseDenied {
		return licenseDenied
	}
	return licenseUnknown
}

func deniedExpressionAtom(expression string, deny map[string]struct{}) bool {
	tokens, err := tokenizeSPDX(expression)
	if err != nil {
		return false
	}
	for _, token := range tokens {
		if _, denied := deny[token]; denied {
			return true
		}
	}
	return false
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
