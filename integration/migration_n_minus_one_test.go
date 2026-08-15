package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	postgresmigrations "github.com/araihu/xisnove/db/migrations/postgres"
	sqlitemigrations "github.com/araihu/xisnove/db/migrations/sqlite"
	"github.com/araihu/xisnove/internal/adapters/database"
	"github.com/pressly/goose/v3"
)

const frozenNMinusOneFixturePath = "integration/testdata/migration-n-minus-one/v2"
const frozenNMinusOneManifestSHA256 = "18c20daf125ef09d5f7e5596b25c520b1c2bde602f3639776a130158a7cf3708"
const postgresNMinusOneFixturePath = "integration/testdata/migration-n-minus-one/v3-postgres"
const postgresNMinusOneManifestSHA256 = "5858296af09d58cb2177b82e58ae956e1bb0719c2bc215fcfbc4bb981d87b857"

type frozenNMinusOneManifest struct {
	FormatVersion  int               `json:"format_version"`
	RuntimeVersion string            `json:"runtime_version"`
	Source         string            `json:"source"`
	Schema         frozenSchemaRange `json:"schema"`
	SHA256         map[string]string `json:"sha256"`
}

type frozenSchemaRange struct {
	Baseline int64 `json:"baseline"`
	Expand   int64 `json:"expand"`
	Minimum  int64 `json:"minimum"`
	Maximum  int64 `json:"maximum"`
}

func TestNMinusOneBinaryRemainsReadyAfterExpandMigration(t *testing.T) {
	repositoryRoot := nMinusOneRepositoryRoot(t)
	fixtureRoot := filepath.Join(repositoryRoot, filepath.FromSlash(frozenNMinusOneFixturePath))
	manifest := loadFrozenNMinusOneManifest(t, fixtureRoot)

	if manifest.FormatVersion != 2 || manifest.RuntimeVersion != "m6.2-future-n-minus-one-baseline-v3" {
		t.Fatalf("unexpected frozen fixture identity: format=%d runtime=%q", manifest.FormatVersion, manifest.RuntimeVersion)
	}
	if manifest.Schema != (frozenSchemaRange{Baseline: 12, Expand: 13, Minimum: 12, Maximum: 13}) {
		t.Fatalf("frozen schema range = %+v", manifest.Schema)
	}
	if sqlitemigrations.LatestVersion != manifest.Schema.Expand {
		t.Fatalf("current schema version = %d, fixture expand version = %d", sqlitemigrations.LatestVersion, manifest.Schema.Expand)
	}

	probe := buildFrozenNMinusOneProbe(t, fixtureRoot, manifest)
	assertFrozenNMinusOneNotReady(t, probe, manifest.Schema.Minimum-1)
	assertFrozenNMinusOneNotReady(t, probe, manifest.Schema.Maximum+1)
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "compatibility.db")
	handle, err := database.Open(ctx, database.Config{Profile: database.ProfileSQLite, URL: databasePath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })

	provider, err := goose.NewProvider(goose.DialectSQLite3, handle.DB, sqlitemigrations.Files, goose.WithTableName("schema_migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, manifest.Schema.Baseline); err != nil {
		t.Fatalf("create frozen schema %d: %v", manifest.Schema.Baseline, err)
	}
	assertSQLiteSchemaVersion(t, handle, manifest.Schema.Baseline)
	assertFrozenNMinusOneReady(t, probe, manifest.Schema.Baseline)
	if err := handle.Ready(ctx); err != nil {
		t.Fatalf("current runtime rejected frozen schema %d: %v", manifest.Schema.Baseline, err)
	}

	if err := handle.Migrate(ctx); err != nil {
		t.Fatalf("expand schema to %d: %v", manifest.Schema.Expand, err)
	}
	if err := handle.Ready(ctx); err != nil {
		t.Fatalf("current runtime rejected expanded schema %d: %v", manifest.Schema.Expand, err)
	}
	assertSQLiteSchemaVersion(t, handle, manifest.Schema.Expand)
	assertFrozenNMinusOneReady(t, probe, manifest.Schema.Expand)
}

func TestNMinusOneFixtureRejectsChecksumTampering(t *testing.T) {
	fixtureRoot := filepath.Join(nMinusOneRepositoryRoot(t), filepath.FromSlash(frozenNMinusOneFixturePath))
	manifest := loadFrozenNMinusOneManifest(t, fixtureRoot)
	source, err := os.ReadFile(filepath.Join(fixtureRoot, filepath.FromSlash(manifest.Source)))
	if err != nil {
		t.Fatal(err)
	}
	tamperedRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tamperedRoot, "probe"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(fixtureRoot, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tamperedRoot, "manifest.json"), manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	source[0] ^= 0xff
	if err := os.WriteFile(filepath.Join(tamperedRoot, filepath.FromSlash(manifest.Source)), source, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readFrozenNMinusOneManifest(tamperedRoot); err == nil || !strings.Contains(err.Error(), "source checksum") {
		t.Fatalf("tampered source verification error = %v", err)
	}

	manifestBytes[len(manifestBytes)-2] ^= 0x01
	if err := os.WriteFile(filepath.Join(tamperedRoot, "manifest.json"), manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readFrozenNMinusOneManifest(tamperedRoot); err == nil || !strings.Contains(err.Error(), "manifest checksum") {
		t.Fatalf("tampered manifest verification error = %v", err)
	}
}

func TestPostgresNMinusOneFixtureRemainsReadyAfterPrecisionExpand(t *testing.T) {
	fixtureRoot := filepath.Join(nMinusOneRepositoryRoot(t), filepath.FromSlash(postgresNMinusOneFixturePath))
	manifest, err := readFrozenNMinusOneManifestWithChecksum(fixtureRoot, postgresNMinusOneManifestSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != 3 || manifest.RuntimeVersion != "m6.3-future-n-minus-one-postgres-v4" {
		t.Fatalf("unexpected PostgreSQL frozen fixture identity: format=%d runtime=%q", manifest.FormatVersion, manifest.RuntimeVersion)
	}
	if manifest.Schema != (frozenSchemaRange{Baseline: 13, Expand: 14, Minimum: 13, Maximum: 14}) {
		t.Fatalf("PostgreSQL frozen schema range = %+v", manifest.Schema)
	}
	if postgresmigrations.LatestVersion != manifest.Schema.Expand {
		t.Fatalf("PostgreSQL schema version = %d, fixture expand version = %d", postgresmigrations.LatestVersion, manifest.Schema.Expand)
	}

	probe := buildFrozenNMinusOneProbe(t, fixtureRoot, manifest)
	assertFrozenNMinusOneNotReadyForInterval(t, probe, manifest.Schema.Minimum-1, manifest.Schema.Minimum, manifest.Schema.Maximum)
	assertFrozenNMinusOneNotReadyForInterval(t, probe, manifest.Schema.Maximum+1, manifest.Schema.Minimum, manifest.Schema.Maximum)
	assertFrozenNMinusOneReadyForInterval(t, probe, manifest.Schema.Baseline, manifest.Schema.Minimum, manifest.Schema.Maximum)
	assertFrozenNMinusOneReadyForInterval(t, probe, manifest.Schema.Expand, manifest.Schema.Minimum, manifest.Schema.Maximum)
}

func loadFrozenNMinusOneManifest(t *testing.T, fixtureRoot string) frozenNMinusOneManifest {
	t.Helper()
	manifest, err := readFrozenNMinusOneManifest(fixtureRoot)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func readFrozenNMinusOneManifest(fixtureRoot string) (frozenNMinusOneManifest, error) {
	return readFrozenNMinusOneManifestWithChecksum(fixtureRoot, frozenNMinusOneManifestSHA256)
}

func readFrozenNMinusOneManifestWithChecksum(fixtureRoot, expectedManifestSHA256 string) (frozenNMinusOneManifest, error) {
	manifestBytes, err := os.ReadFile(filepath.Join(fixtureRoot, "manifest.json"))
	if err != nil {
		return frozenNMinusOneManifest{}, err
	}
	if got := sha256Hex(manifestBytes); got != expectedManifestSHA256 {
		return frozenNMinusOneManifest{}, fmt.Errorf("frozen manifest checksum = %s, want %s", got, expectedManifestSHA256)
	}
	var manifest frozenNMinusOneManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return frozenNMinusOneManifest{}, fmt.Errorf("decode frozen manifest: %w", err)
	}
	sourcePath := filepath.Join(fixtureRoot, filepath.FromSlash(manifest.Source))
	sourceBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		return frozenNMinusOneManifest{}, err
	}
	want, ok := manifest.SHA256[manifest.Source]
	if !ok || want == "" {
		return frozenNMinusOneManifest{}, fmt.Errorf("manifest lacks checksum for %q", manifest.Source)
	}
	if got := sha256Hex(sourceBytes); got != want {
		return frozenNMinusOneManifest{}, fmt.Errorf("frozen source checksum = %s, want %s", got, want)
	}
	return manifest, nil
}

func buildFrozenNMinusOneProbe(t *testing.T, fixtureRoot string, manifest frozenNMinusOneManifest) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(fixtureRoot, filepath.FromSlash(manifest.Source)))
	if err != nil {
		t.Fatal(err)
	}
	buildRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(buildRoot, "main.go"), source, 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(buildRoot, "xisnove-n-minus-one-probe")
	command := exec.Command("go", "build", "-trimpath", "-o", output, "main.go")
	command.Dir = buildRoot
	command.Env = append(os.Environ(), "GO111MODULE=off", "GOPROXY=off", "GOSUMDB=off")
	combined, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build frozen N-1 probe without network: %v\n%s", err, combined)
	}
	return output
}

func assertFrozenNMinusOneReady(t *testing.T, probe string, schemaVersion int64) {
	assertFrozenNMinusOneReadyForInterval(t, probe, schemaVersion, 12, 13)
}

func assertFrozenNMinusOneReadyForInterval(t *testing.T, probe string, schemaVersion, minimum, maximum int64) {
	t.Helper()
	command := exec.Command(probe, "--schema-version", strconv.FormatInt(schemaVersion, 10))
	combined, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("frozen N-1 probe rejected schema %d: %v\n%s", schemaVersion, err, combined)
	}
	want := fmt.Sprintf("ready schema=%d interval=[%d,%d]", schemaVersion, minimum, maximum)
	if got := strings.TrimSpace(string(combined)); got != want {
		t.Fatalf("frozen N-1 output = %q, want %q", got, want)
	}
}

func assertFrozenNMinusOneNotReady(t *testing.T, probe string, schemaVersion int64) {
	assertFrozenNMinusOneNotReadyForInterval(t, probe, schemaVersion, 12, 13)
}

func assertFrozenNMinusOneNotReadyForInterval(t *testing.T, probe string, schemaVersion, minimum, maximum int64) {
	t.Helper()
	command := exec.Command(probe, "--schema-version", strconv.FormatInt(schemaVersion, 10))
	combined, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("frozen N-1 probe accepted incompatible schema %d: %s", schemaVersion, combined)
	}
	exitError, ok := err.(*exec.ExitError)
	if !ok || exitError.ExitCode() != 1 {
		t.Fatalf("frozen N-1 rejection exit = %v, want 1", err)
	}
	want := fmt.Sprintf("not ready schema=%d interval=[%d,%d]", schemaVersion, minimum, maximum)
	if got := strings.TrimSpace(string(combined)); got != want {
		t.Fatalf("frozen N-1 rejection = %q, want %q", got, want)
	}
}

func assertSQLiteSchemaVersion(t *testing.T, handle *database.Handle, want int64) {
	t.Helper()
	var got int64
	if err := handle.DB.QueryRowContext(context.Background(), `
		SELECT COALESCE(MAX(version_id), 0)
		FROM schema_migrations
		WHERE is_applied = 1
	`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("database schema version = %d, want %d", got, want)
	}
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func nMinusOneRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve integration test path")
	}
	return filepath.Dir(filepath.Dir(filename))
}
