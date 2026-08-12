import {
  argument,
  dag,
  Container,
  Directory,
  File,
  func,
  object,
  ReturnType,
  Secret,
  Service,
} from "@dagger.io/dagger";

const GO_IMAGE =
  "golang:1.26.1-bookworm@sha256:ab3d6955bbc813a0f3fdf220c1d817dd89c0b3f283777db8ece4a32fe7858edd";
const SITE_GO_IMAGE =
  "golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd";
const NODE_IMAGE =
  "node:22.13.0-bookworm-slim@sha256:f5a0871ab03b035c58bdb3007c3d177b001c2145c18e81817b71624dcf7d8bff";
const JQ_IMAGE =
  "ghcr.io/jqlang/jq:1.8.2@sha256:b9c68867e5766576263a222e91db3de422d802069c7af70440e667a95344e486";
const POSTGRES_IMAGE =
  "postgres:18.3-alpine3.23@sha256:54451ecb8ab38c24c3ec123f2fd501303a3a1856a5c66e98cecf2460d5e1e9d7";

const SOURCE_EXCLUDES = [
  ".git",
  ".git/**",
  ".dagger/node_modules",
  ".dagger/node_modules/**",
  ".dagger/sdk",
  ".dagger/sdk/**",
  ".dagger-inputs",
  ".dagger-inputs/**",
  "x9-site-output",
  "x9-site-output/**",
  "turso-output",
  "turso-output/**",
  ".worktrees",
  ".worktrees/**",
  "site/public",
  "site/public/**",
  "site/.wrangler",
  "site/.wrangler/**",
  "__pycache__",
  "**/__pycache__/**",
];

type CachePartition =
  | "trusted-main"
  | "trusted-site"
  | "trusted-turso"
  | "untrusted-pr"
  | "local";

type CIInput = {
  cachePartition: CachePartition;
  eventName: string;
  hasBaseline: boolean;
  runNonce: string;
};

@object()
export class Xisnove {
  /** Root code generation, OpenAPI, database, test, and backup/restore gates. */
  @func({ cache: "never" })
  async rootCi(
    @argument({ defaultPath: ".", ignore: SOURCE_EXCLUDES }) source: Directory,
    input: File,
    baseline: File,
  ): Promise<string> {
    const config = await this.ciInput(input);
    this.requirePartition(
      config.cachePartition,
      ["trusted-main", "untrusted-pr", "local"],
      "root CI",
    );
    return this.rootChecks(source, config, baseline);
  }

  /** Standalone agent generation, drift, vet, and race-test gates. */
  @func({ cache: "never" })
  async agentCi(
    @argument({ defaultPath: ".", ignore: SOURCE_EXCLUDES }) source: Directory,
    input: File,
  ): Promise<string> {
    const config = await this.ciInput(input);
    this.requirePartition(
      config.cachePartition,
      ["trusted-main", "untrusted-pr", "local"],
      "agent CI",
    );
    return this.agentChecks(source, config);
  }

  /** Root workspace proof that exercises the nested agent package. */
  @func({ cache: "never" })
  async agentWorkspaceCi(
    @argument({ defaultPath: ".", ignore: SOURCE_EXCLUDES }) source: Directory,
    input: File,
  ): Promise<string> {
    const config = await this.ciInput(input);
    this.requirePartition(
      config.cachePartition,
      ["trusted-main", "untrusted-pr", "local"],
      "agent workspace CI",
    );
    return this.agentWorkspaceChecks(source, config);
  }

  /** Build, test, and dry-run the X-9 site; returns exactly the upload artifact. */
  @func({ cache: "never" })
  async siteValidate(
    @argument({ defaultPath: ".", ignore: SOURCE_EXCLUDES }) source: Directory,
    input: File,
  ): Promise<Directory> {
    const config = await this.ciInput(input);
    this.requirePartition(
      config.cachePartition,
      ["trusted-site", "untrusted-pr", "local"],
      "site validation",
    );
    await this.auditDagger(source, config.cachePartition);
    return this.siteProject(source, config.cachePartition, config.runNonce)
      .withExec([
        "go",
        "run",
        "github.com/a-h/templ/cmd/templ@v0.3.1020",
        "generate",
      ])
      .withExec(["go", "run", "./cmd/x9-site", "build"])
      .withExec(["go", "test", "./...", "-count=1"])
      .withExec(["npx", "--yes", "wrangler@4.114.0", "deploy", "--dry-run"])
      .directory("/src/site/public");
  }

  /** Deploy only validated X-9 static bytes; credentials enter at this final effect boundary. */
  @func({ cache: "never" })
  async siteDeploy(
    @argument({ defaultPath: ".", ignore: SOURCE_EXCLUDES }) source: Directory,
    artifact: Directory,
    input: File,
    cloudflareApiToken: Secret,
  ): Promise<string> {
    const data = await this.exactInput(input, [
      "account_id",
      "cache_partition",
      "effect_nonce",
    ]);
    const cachePartition = this.cachePartition(data.cache_partition);
    this.requirePartition(
      cachePartition,
      ["trusted-site", "local"],
      "site deployment",
    );
    this.nonempty(data.account_id, "account_id");
    this.nonce(data.effect_nonce);
    await this.auditDagger(source, cachePartition);
    return this.siteProject(source, cachePartition, data.effect_nonce)
      .withDirectory("/src/site/public", artifact)
      .withSecretVariable("CLOUDFLARE_API_TOKEN", cloudflareApiToken)
      .withEnvVariable("CLOUDFLARE_ACCOUNT_ID", data.account_id)
      .withExec([
        "bash",
        "-euo",
        "pipefail",
        "-c",
        "test -n \"$CLOUDFLARE_API_TOKEN\" || { echo 'Missing CLOUDFLARE_API_TOKEN Actions secret' >&2; exit 1; }; test -n \"$CLOUDFLARE_ACCOUNT_ID\" || { echo 'Missing CLOUDFLARE_ACCOUNT_ID Actions variable' >&2; exit 1; }",
      ])
      .withExec(["npx", "--yes", "wrangler@4.114.0", "deploy"])
      .stdout();
  }

  /** Protected managed-Turso conformance; always returns a JUnit/status directory. */
  @func({ cache: "never" })
  async tursoConformance(
    @argument({ defaultPath: ".", ignore: SOURCE_EXCLUDES }) source: Directory,
    input: File,
    tursoApiKey: Secret,
  ): Promise<Directory> {
    const data = await this.exactInput(input, [
      "cache_partition",
      "run_nonce",
      "turso_group",
      "turso_org",
    ]);
    const cachePartition = this.cachePartition(data.cache_partition);
    this.requirePartition(
      cachePartition,
      ["trusted-turso", "local"],
      "managed Turso",
    );
    this.nonce(data.run_nonce);
    this.nonempty(data.turso_group, "turso_group");
    if (data.turso_org.length > 255) throw new Error("turso_org is too long");

    await this.auditDagger(source, cachePartition);

    const toolchain = this.project(
      source,
      cachePartition,
      data.run_nonce,
    ).withExec(["go", "install", "gotest.tools/gotestsum@v1.13.0"]);

    const script = [
      "set -Eeuo pipefail",
      "mkdir -p /out",
      "finish() {",
      "  rc=$?",
      "  trap - EXIT",
      "  set +e",
      "  test -f /out/managed-turso-storage-matrix.xml || : > /out/managed-turso-storage-matrix.xml",
      "  printf '%s\\n' \"$rc\" > /out/status",
      "  exit 0",
      "}",
      "trap finish EXIT",
      "go test -race ./internal/adapters/tursocloud -run Conformance -count=1",
      "gotestsum --junitfile /out/managed-turso-storage-matrix.xml -- -race ./integration -run '^TestStorageMatrix/TursoCloud$' -count=1",
    ].join("\n");

    return toolchain
      .withSecretVariable("TURSO_API_KEY", tursoApiKey)
      .withEnvVariable("TURSO_ORG", data.turso_org)
      .withEnvVariable("TURSO_GROUP", data.turso_group)
      .withExec(["bash", "-c", script], { expect: ReturnType.Any })
      .directory("/out");
  }

  /** Local equivalent of normal root, agent, workspace, and X-9 validation. */
  @func({ cache: "never" })
  async localCi(
    @argument({ defaultPath: ".", ignore: SOURCE_EXCLUDES }) source: Directory,
    runNonce: string,
  ): Promise<string> {
    this.nonce(runNonce);
    const config: CIInput = {
      cachePartition: "local",
      eventName: "local",
      hasBaseline: false,
      runNonce,
    };
    await this.rootChecks(source, config);
    await this.agentChecks(source, config);
    await this.agentWorkspaceChecks(source, config);
    await this.auditDagger(source, config.cachePartition);
    await this.siteProject(source, config.cachePartition, config.runNonce)
      .withExec([
        "go",
        "run",
        "github.com/a-h/templ/cmd/templ@v0.3.1020",
        "generate",
      ])
      .withExec(["go", "run", "./cmd/x9-site", "build"])
      .withExec(["go", "test", "./...", "-count=1"])
      .withExec(["npx", "--yes", "wrangler@4.114.0", "deploy", "--dry-run"])
      .stdout();
    return "local CI passed";
  }

  private async rootChecks(
    source: Directory,
    config: CIInput,
    baseline?: File,
  ): Promise<string> {
    await this.auditDagger(source, config.cachePartition);
    const postgres = this.postgres();
    const postgresReady = this.postgresReady(postgres, config.runNonce);
    const project = this.project(source, config.cachePartition, config.runNonce)
      .withServiceBinding("postgres", postgres)
      .withFile("/tmp/postgres-ready", postgresReady)
      .withEnvVariable(
        "XISNOVE_TEST_POSTGRES_URL",
        "postgres://postgres:xisnove@postgres:5432/xisnove?sslmode=disable",
      );

    await project
      .withExec(["go", "tool", "vacuum", "lint", "-d", "api/openapi.yaml"])
      .withExec(["go", "generate", "./..."])
      .withExec(["go", "tool", "sqlc", "generate"])
      .withExec(["go", "tool", "sqlc", "diff"])
      .withExec(["git", "diff", "--exit-code"])
      .withExec(["go", "vet", "./..."])
      .withExec(["go", "test", "-race", "./..."])
      .withExec([
        "go",
        "test",
        "-race",
        "./integration",
        "-run",
        "TestStorageMatrix",
        "-count=1",
      ])
      .stdout();

    await this.postgresBackupRestore(project, postgres);

    if (!config.hasBaseline)
      return "root CI passed; no OpenAPI compatibility baseline exists";
    if (baseline === undefined)
      throw new Error("OpenAPI baseline was declared but not supplied");
    return project
      .withFile("/tmp/base-openapi.yaml", baseline)
      .withExec([
        "go",
        "tool",
        "oasdiff",
        "breaking",
        "/tmp/base-openapi.yaml",
        "api/openapi.yaml",
      ])
      .stdout();
  }

  private async agentChecks(
    source: Directory,
    config: CIInput,
  ): Promise<string> {
    await this.auditDagger(source, config.cachePartition);
    return this.project(source, config.cachePartition, config.runNonce)
      .withWorkdir("/src/agent")
      .withEnvVariable("GOWORK", "off")
      .withExec(["go", "generate", "./..."])
      .withExec(["git", "diff", "--exit-code"])
      .withExec(["go", "vet", "./..."])
      .withExec(["go", "test", "-race", "./..."])
      .stdout();
  }

  private async agentWorkspaceChecks(
    source: Directory,
    config: CIInput,
  ): Promise<string> {
    await this.auditDagger(source, config.cachePartition);
    return this.project(source, config.cachePartition, config.runNonce)
      .withExec(["go", "test", "-race", "./agent/..."])
      .stdout();
  }

  private async postgresBackupRestore(
    project: Container,
    postgres: Service,
  ): Promise<void> {
    const migrated = project.withExec([
      "go",
      "run",
      "./cmd/xisnove-server",
      "db",
      "migrate",
      "--database-profile",
      "postgres",
      "--database-url",
      "postgres://postgres:xisnove@postgres:5432/xisnove?sslmode=disable",
    ]);

    const restored = this.postgresClient(postgres)
      .withFile("/tmp/migration-complete", migrated.file("/src/go.mod"))
      .withExec([
        "sh",
        "-euc",
        "psql --host postgres --port 5432 --username postgres --dbname xisnove --set ON_ERROR_STOP=1 --command \"INSERT INTO admins (id,email,password_hash,created_at) VALUES ('00000000-0000-4000-8000-000000000202','restore-smoke@example.com','hash',CURRENT_TIMESTAMP) ON CONFLICT DO NOTHING\"; pg_dump --host postgres --port 5432 --username postgres --format custom xisnove > /tmp/xisnove-postgres-smoke.dump; dropdb --host postgres --port 5432 --username postgres --if-exists xisnove_restore_smoke; createdb --host postgres --port 5432 --username postgres xisnove_restore_smoke; pg_restore --host postgres --port 5432 --username postgres --exit-on-error --single-transaction --no-owner --dbname xisnove_restore_smoke /tmp/xisnove-postgres-smoke.dump; : > /tmp/restore-complete",
      ]);

    const postRestoreTest = project
      .withServiceBinding("postgres", postgres)
      .withFile("/tmp/restore-complete", restored.file("/tmp/restore-complete"))
      .withEnvVariable(
        "XISNOVE_TEST_POSTGRES_URL",
        "postgres://postgres:xisnove@postgres:5432/xisnove_restore_smoke?sslmode=disable",
      )
      .withExec([
        "go",
        "test",
        "./internal/adapters/postgres",
        "-run",
        "TestMigrateAndReady",
        "-count=1",
      ]);

    await this.postgresClient(postgres)
      .withFile("/tmp/post-restore-test", postRestoreTest.file("/src/go.mod"))
      .withExec([
        "sh",
        "-euc",
        'restored_count=$(psql --host postgres --port 5432 --username postgres --dbname xisnove_restore_smoke --tuples-only --no-align --command "SELECT COUNT(*) FROM admins WHERE email=\'restore-smoke@example.com\'"); test "$restored_count" = 1',
      ])
      .stdout();
  }

  private postgres(): Service {
    return dag
      .container()
      .from(POSTGRES_IMAGE)
      .withEnvVariable("POSTGRES_DB", "xisnove")
      .withEnvVariable("POSTGRES_PASSWORD", "xisnove")
      .withExposedPort(5432)
      .asService({ useEntrypoint: true });
  }

  private postgresClient(postgres: Service): Container {
    return dag
      .container()
      .from(POSTGRES_IMAGE)
      .withServiceBinding("postgres", postgres)
      .withEnvVariable("PGPASSWORD", "xisnove");
  }

  private postgresReady(postgres: Service, runNonce: string): File {
    return this.postgresClient(postgres)
      .withEnvVariable("XISNOVE_RUN_NONCE", runNonce)
      .withExec([
        "sh",
        "-euc",
        "attempt=0; until psql --host postgres --port 5432 --username postgres --dbname xisnove --set ON_ERROR_STOP=1 --command 'SELECT 1' >/dev/null; do attempt=$((attempt + 1)); test \"$attempt\" -lt 40 || exit 1; sleep 1; done; : > /tmp/postgres-ready",
      ])
      .file("/tmp/postgres-ready");
  }

  private async auditDagger(
    source: Directory,
    partition: CachePartition,
  ): Promise<void> {
    await this.nodeProject(source, partition)
      .withExec(["bash", "scripts/check-dagger-runner-contract.sh"])
      .withExec([
        "jq",
        "-e",
        '.lockfileVersion == 3 and .packages[""].dependencies.typescript == "5.9.3" and (.packages[""].dependencies["@dagger.io/dagger"] | not)',
        "/src/.dagger/package-lock.json",
      ])
      .withExec([
        "npm",
        "audit",
        "--prefix",
        "/src/.dagger",
        "--package-lock-only",
        "--omit=dev",
        "--audit-level=high",
      ])
      .stdout();
  }

  private project(
    source: Directory,
    partition: CachePartition,
    runNonce: string,
  ): Container {
    return this.base(source, partition).withEnvVariable(
      "XISNOVE_RUN_NONCE",
      runNonce,
    );
  }

  private nodeProject(
    source: Directory,
    partition: CachePartition,
    goImage = GO_IMAGE,
    goVersion = "go1.26.1",
  ): Container {
    const node = dag.container().from(NODE_IMAGE);
    const project = this.base(source, partition, goImage, goVersion)
      .withFile("/usr/local/bin/node", node.file("/usr/local/bin/node"), {
        permissions: 0o755,
      })
      .withDirectory(
        "/usr/local/lib/node_modules",
        node.directory("/usr/local/lib/node_modules"),
      )
      .withExec([
        "ln",
        "-sf",
        "../lib/node_modules/npm/bin/npm-cli.js",
        "/usr/local/bin/npm",
      ])
      .withExec([
        "ln",
        "-sf",
        "../lib/node_modules/npm/bin/npx-cli.js",
        "/usr/local/bin/npx",
      ])
      .withExec(["node", "--version"]);
    const namespace = this.cacheNamespace(partition);
    return project.withMountedCache(
      "/root/.npm",
      dag.cacheVolume(this.volume(namespace, "npm-node22")),
    );
  }

  private siteProject(
    source: Directory,
    partition: CachePartition,
    runNonce: string,
  ): Container {
    return this.nodeProject(source, partition, SITE_GO_IMAGE, "go1.26.5")
      .withWorkdir("/src/site")
      .withEnvVariable("GOWORK", "off")
      .withEnvVariable("XISNOVE_RUN_NONCE", runNonce);
  }

  private base(
    source: Directory,
    partition: CachePartition,
    goImage = GO_IMAGE,
    goVersion = "go1.26.1",
  ): Container {
    const jq = dag.container().from(JQ_IMAGE);
    const project = dag
      .container()
      .from(goImage)
      .withFile("/usr/local/bin/jq", jq.file("/jq"), { permissions: 0o755 })
      .withDirectory("/src", source)
      .withWorkdir("/src")
      .withEnvVariable("GOMODCACHE", "/go/pkg/mod")
      .withEnvVariable("GOCACHE", "/root/.cache/go-build")
      .withEnvVariable("GOBIN", "/tools/bin")
      .withEnvVariable(
        "PATH",
        "/tools/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
      )
      .withExec(["mkdir", "-p", "/tools/bin"])
      .withExec([
        "bash",
        "-euo",
        "pipefail",
        "-c",
        "git init -q; git config user.name Dagger; git config user.email dagger@invalid; git add -A; git commit -qm source-baseline",
      ])
      .withExec(["sh", "-euc", 'test "$(jq --version)" = jq-1.8.2']);

    const namespace = this.cacheNamespace(partition);
    return project
      .withMountedCache(
        "/go/pkg/mod",
        dag.cacheVolume(this.volume(namespace, "go-mod-" + goVersion)),
      )
      .withMountedCache(
        "/root/.cache/go-build",
        dag.cacheVolume(this.volume(namespace, "go-build-" + goVersion)),
      )
      .withMountedCache(
        "/tools",
        dag.cacheVolume(this.volume(namespace, "go-tools-" + goVersion)),
      );
  }

  private volume(namespace: string, purpose: string): string {
    return "araihu-ci-v1-xisnove-" + namespace + "-" + purpose;
  }

  private cacheNamespace(partition: CachePartition): string {
    // The host-owned runner lane selects the Engine trust domain. This input
    // only guards cache use; it never grants access to a trusted Engine.
    switch (partition) {
      case "trusted-main":
        return "main";
      case "trusted-site":
        return "site";
      case "trusted-turso":
        return "turso";
      case "local":
        return "local";
      case "untrusted-pr":
        return "pr";
    }
    throw new Error("invalid cache partition");
  }

  private async ciInput(file: File): Promise<CIInput> {
    const data = await this.exactInput(file, [
      "cache_partition",
      "event_name",
      "has_baseline",
      "run_nonce",
    ]);
    const cachePartition = this.cachePartition(data.cache_partition);
    const hasBaseline = data.has_baseline === "true";
    if (!hasBaseline && data.has_baseline !== "false")
      throw new Error("has_baseline must be true or false");
    if (
      !/^(pull_request|push|workflow_dispatch|schedule|release|local)$/.test(
        data.event_name,
      )
    )
      throw new Error("unsupported event_name");
    if (
      data.event_name === "pull_request" &&
      cachePartition !== "untrusted-pr"
    ) {
      throw new Error(
        "pull_request inputs must select the untrusted-pr cache partition",
      );
    }
    this.nonce(data.run_nonce);
    return {
      cachePartition,
      eventName: data.event_name,
      hasBaseline,
      runNonce: data.run_nonce,
    };
  }

  private cachePartition(value: string): CachePartition {
    if (
      value === "trusted-main" ||
      value === "trusted-site" ||
      value === "trusted-turso" ||
      value === "untrusted-pr" ||
      value === "local"
    )
      return value;
    throw new Error("invalid cache partition");
  }

  private requirePartition(
    actual: CachePartition,
    allowed: CachePartition[],
    operation: string,
  ): void {
    if (!allowed.includes(actual))
      throw new Error(operation + " received an invalid cache partition");
  }

  private nonce(value: string): void {
    if (value === "local") return;
    if (!/^[1-9][0-9]*-[1-9][0-9]*$/.test(value))
      throw new Error("invalid run nonce");
  }

  private nonempty(value: string, field: string): void {
    if (value.length === 0 || value.length > 255)
      throw new Error(field + " must be non-empty and at most 255 characters");
  }

  private async exactInput(
    file: File,
    keys: string[],
  ): Promise<Record<string, string>> {
    let value: unknown;
    try {
      value = JSON.parse(await file.contents());
    } catch {
      throw new Error("metadata must be valid JSON");
    }
    if (value === null || Array.isArray(value) || typeof value !== "object")
      throw new Error("metadata must be a JSON object");
    const record = value as Record<string, unknown>;
    if (Object.keys(record).sort().join("\0") !== [...keys].sort().join("\0"))
      throw new Error("metadata fields do not match the exact schema");
    for (const key of keys) {
      if (typeof record[key] !== "string")
        throw new Error("metadata field " + key + " must be a string");
    }
    return record as Record<string, string>;
  }
}
