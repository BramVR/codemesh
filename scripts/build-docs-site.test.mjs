import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(scriptDir, "..");

test("docs-site builds static artifact from public allowlist", () => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "codemesh-docs-site-"));
  const outDir = path.join(tempRoot, "dist", "docs-site");
  fs.cpSync(path.join(root, "docs"), path.join(tempRoot, "docs"), { recursive: true });
  fs.mkdirSync(path.join(tempRoot, "docs", "research"), { recursive: true });
  fs.writeFileSync(
    path.join(tempRoot, "docs", "research", "internal.md"),
    ["# Internal Research", "", "INTERNAL_ONLY_SENTINEL_CODEMESH_PRIVATE_WORKSPACE", ""].join("\n"),
    "utf8",
  );
  fs.appendFileSync(
    path.join(tempRoot, "docs", "index.md"),
    "\n\n[Unsafe link](javascript:alert(1))\n[Unsafe control link](java\tscript:alert(1))\n[Safe link](https://example.com?a=1&b=2)\n\n```md\n[Custom scheme example](codemesh:thing)\n{: .keep }\n```\n",
    "utf8",
  );

  try {
    execFileSync("make", ["docs-site"], {
      cwd: root,
      env: {
        ...process.env,
        CODEMESH_DOCS_SITE_ROOT: tempRoot,
        CODEMESH_DOCS_SITE_OUT: outDir,
        TMPDIR: os.tmpdir(),
      },
      encoding: "utf8",
      stdio: "pipe",
    });

    const index = fs.readFileSync(path.join(outDir, "index.html"), "utf8");
    const quickstart = fs.readFileSync(path.join(outDir, "quickstart.html"), "utf8");
    const commands = fs.readFileSync(path.join(outDir, "commands.html"), "utf8");
    const trust = fs.readFileSync(path.join(outDir, "trust.html"), "utf8");
    const llmsFull = fs.readFileSync(path.join(outDir, "llms-full.txt"), "utf8");

    for (const rel of [
      "index.html",
      "install.html",
      "quickstart.html",
      "commands.html",
      "trust.html",
      "project-policy.html",
      "state.html",
      "mvp.html",
      "e2e.html",
      "sitemap.xml",
      "robots.txt",
      "llms.txt",
      "llms-full.txt",
      "favicon.svg",
      "assets/codemesh-hero.svg",
      "assets/social-card.svg",
      ".nojekyll",
    ]) {
      assert.ok(fs.existsSync(path.join(outDir, rel)), `${rel} should exist`);
    }

    assert.match(index, /CodeMesh/);
    assert.match(index, /href="quickstart\.html"/);
    assert.match(index, /href="install\.html"/);
    assert.match(index, /href="commands\.html"/);
    assert.match(index, /id="doc-search"/);
    assert.match(index, /data-theme-toggle/);
    assert.match(index, /class="nav-toggle"/);
    assert.match(index, /assets\/codemesh-hero\.svg/);
    assert.match(index, /<table>/);
    assert.match(commands, /<pre><code class="language-sh">/);
    assert.match(trust, /id="secret-free-readiness"/);
    assert.match(trust, /<table>/);
    assert.match(quickstart, /CODEMESH_HOME/);
    assert.match(quickstart, /git clone --bare/);
    assert.match(quickstart, /href="commands\.html"/);
    assert.match(quickstart, /<small>Previous<\/small>Install/);
    assert.match(quickstart, /<small>Next<\/small>Command Catalog/);
    assert.doesNotMatch(index + llmsFull, /javascript:alert/);
    assert.doesNotMatch(index + llmsFull, /java\tscript:alert/);
    assert.match(index, /href="https:\/\/example\.com\?a=1&amp;b=2"/);
    assert.doesNotMatch(index, /&amp;amp;b=2/);
    assert.match(index, /\[Custom scheme example\]\(codemesh:thing\)/);
    assert.match(index, /\{:\s\.keep\s\}/);
    assert.doesNotMatch(index + llmsFull, /INTERNAL_ONLY_SENTINEL_CODEMESH_PRIVATE_WORKSPACE/);
    assert.ok(!fs.existsSync(path.join(outDir, "adr", "0001-product-boundary.html")));
    assert.ok(!fs.existsSync(path.join(outDir, "research", "internal.html")));

    const quickstartCode = [...quickstart.matchAll(/<pre><code[^>]*>([\s\S]*?)<\/code><\/pre>/g)]
      .map((match) => match[1])
      .join("\n");
    assert.doesNotMatch(quickstartCode, /\/Users\/bram|~\/Projects|~\/\.codemesh|GITHUB_TOKEN|GH_TOKEN|SECRET|TOKEN=/);
    assert.match(quickstartCode, /CODEMESH_HOME/);
  } finally {
    fs.rmSync(tempRoot, { recursive: true, force: true });
  }
});

test("docs-site fails when the public manifest references a missing page", () => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "codemesh-docs-site-missing-page-"));
  const outDir = path.join(tempRoot, "dist", "docs-site");
  fs.cpSync(path.join(root, "docs"), path.join(tempRoot, "docs"), { recursive: true });
  const manifestPath = path.join(tempRoot, "docs", "public-docs.json");
  const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
  manifest.sections[0].pages.push("missing.md");
  fs.writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`, "utf8");

  try {
    assert.throws(
      () =>
        execFileSync("node", [path.join(root, "scripts", "build-docs-site.mjs")], {
          cwd: root,
          env: {
            ...process.env,
            CODEMESH_DOCS_SITE_ROOT: tempRoot,
            CODEMESH_DOCS_SITE_OUT: outDir,
          },
          encoding: "utf8",
          stdio: "pipe",
        }),
      /docs-site manifest references missing pages: missing\.md/,
    );
  } finally {
    fs.rmSync(tempRoot, { recursive: true, force: true });
  }
});

test("docs-site refuses output paths outside the build root or inside docs", () => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "codemesh-docs-site-safe-out-"));
  fs.cpSync(path.join(root, "docs"), path.join(tempRoot, "docs"), { recursive: true });
  const outside = fs.mkdtempSync(path.join(os.tmpdir(), "codemesh-docs-site-outside-"));

  try {
    assert.throws(
      () =>
        execFileSync("node", [path.join(root, "scripts", "build-docs-site.mjs")], {
          cwd: root,
          env: {
            ...process.env,
            CODEMESH_DOCS_SITE_ROOT: tempRoot,
            CODEMESH_DOCS_SITE_OUT: outside,
          },
          encoding: "utf8",
          stdio: "pipe",
        }),
      /unsafe docs-site output directory/,
    );
    assert.throws(
      () =>
        execFileSync("node", [path.join(root, "scripts", "build-docs-site.mjs")], {
          cwd: root,
          env: {
            ...process.env,
            CODEMESH_DOCS_SITE_ROOT: tempRoot,
            CODEMESH_DOCS_SITE_OUT: path.join(tempRoot, "docs", "site"),
          },
          encoding: "utf8",
          stdio: "pipe",
        }),
      /unsafe docs-site output directory/,
    );
    assert.throws(
      () =>
        execFileSync("node", [path.join(root, "scripts", "build-docs-site.mjs")], {
          cwd: root,
          env: {
            ...process.env,
            CODEMESH_DOCS_SITE_ROOT: tempRoot,
            CODEMESH_DOCS_SITE_OUT: path.join(tempRoot, "scripts"),
          },
          encoding: "utf8",
          stdio: "pipe",
        }),
      /unsafe docs-site output directory/,
    );
    const symlinkTarget = fs.mkdtempSync(path.join(os.tmpdir(), "codemesh-docs-site-symlink-target-"));
    fs.symlinkSync(symlinkTarget, path.join(tempRoot, "dist"), "dir");
    assert.throws(
      () =>
        execFileSync("node", [path.join(root, "scripts", "build-docs-site.mjs")], {
          cwd: root,
          env: {
            ...process.env,
            CODEMESH_DOCS_SITE_ROOT: tempRoot,
            CODEMESH_DOCS_SITE_OUT: path.join(tempRoot, "dist", "docs-site"),
          },
          encoding: "utf8",
          stdio: "pipe",
        }),
      /unsafe docs-site output parent symlink/,
    );
    fs.rmSync(path.join(tempRoot, "dist"), { force: true });
    fs.rmSync(symlinkTarget, { recursive: true, force: true });
  } finally {
    fs.rmSync(tempRoot, { recursive: true, force: true });
    fs.rmSync(outside, { recursive: true, force: true });
  }
});

test("docs-site refuses permalink traversal", () => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "codemesh-docs-site-permalink-"));
  fs.cpSync(path.join(root, "docs"), path.join(tempRoot, "docs"), { recursive: true });

  try {
    for (const permalink of ["../../escape", "..\\..\\escape", "C:\\escape"]) {
      fs.writeFileSync(
        path.join(tempRoot, "docs", "install.md"),
        ["---", "title: Install", `permalink: ${permalink}`, "---", "", "# Install", ""].join("\n"),
        "utf8",
      );
      assert.throws(
        () =>
          execFileSync("node", [path.join(root, "scripts", "build-docs-site.mjs")], {
            cwd: root,
            env: {
              ...process.env,
              CODEMESH_DOCS_SITE_ROOT: tempRoot,
              CODEMESH_DOCS_SITE_OUT: path.join(tempRoot, "dist", "docs-site", "permalink"),
            },
            encoding: "utf8",
            stdio: "pipe",
          }),
        /unsafe docs-site page output path/,
      );
    }
  } finally {
    fs.rmSync(tempRoot, { recursive: true, force: true });
  }
});
