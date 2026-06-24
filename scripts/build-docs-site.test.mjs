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
  fs.appendFileSync(
    path.join(tempRoot, "docs", "commands.md"),
    "\n\n## Entity Heading `<path>`\n\n### Supporting Anchor\n\n```sh {linenos}\necho rich-fence\n[Custom scheme inside rich fence](codemesh:thing)\n```\n\nAfter rich fence prose.\n",
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
    const commandRefs = [
      "commands/init/index.html",
      "commands/add/index.html",
      "commands/scan/index.html",
      "commands/tree/index.html",
      "commands/status/index.html",
      "commands/hydrate/index.html",
      "commands/agent-prepare/index.html",
      "commands/runs/index.html",
      "commands/clean/index.html",
    ];
    const commandRefText = commandRefs.map((rel) => fs.readFileSync(path.join(outDir, rel), "utf8")).join("\n");
    const llmsFull = fs.readFileSync(path.join(outDir, "llms-full.txt"), "utf8");
    const heroSvg = fs.readFileSync(path.join(outDir, "assets", "codemesh-hero.svg"), "utf8");
    const socialSvg = fs.readFileSync(path.join(outDir, "assets", "social-card.svg"), "utf8");
    const favicon = fs.readFileSync(path.join(outDir, "favicon.svg"), "utf8");

    for (const rel of [
      "index.html",
      "install.html",
      "quickstart.html",
      "commands.html",
      ...commandRefs,
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
      "assets/social-card.png",
      "assets/social-card.svg",
      ".nojekyll",
    ]) {
      assert.ok(fs.existsSync(path.join(outDir, rel)), `${rel} should exist`);
    }

    assert.match(index, /CodeMesh/);
    assert.match(index, /<meta name="theme-color" content="#f7f8f4">/);
    assert.match(index, /<meta name="application-name" content="CodeMesh">/);
    assert.match(index, /"@type":"SoftwareApplication"/);
    assert.match(index, /"applicationCategory":"DeveloperApplication"/);
    assert.match(index, /document\.documentElement\.dataset\.theme=t==="dark"\?"dark":"light"/);
    assert.match(index, /href="quickstart\.html"/);
    assert.match(index, /href="install\.html"/);
    assert.match(index, /href="commands\.html"/);
    assert.match(index, /href="commands\/init\/index\.html"/);
    assert.match(index, /href="commands\/agent-prepare\/index\.html"/);
    assert.match(index, /id="doc-search"/);
    assert.match(index, /data-theme-toggle/);
    assert.match(index, /class="nav-toggle"/);
    assert.match(index, /assets\/codemesh-hero\.svg/);
    assert.match(index, /https:\/\/bramvr\.github\.io\/codemesh\/assets\/social-card\.png/);
    assert.match(heroSvg, /CodeMesh graph of projects, machines, temp agent workspaces, and readiness signals/);
    assert.match(heroSvg, /Canonical workspace/);
    assert.match(heroSvg, /Project Registry/);
    assert.match(heroSvg, /temp clone/);
    assert.match(heroSvg, /readiness report/);
    assert.match(heroSvg, /env readiness/);
    assert.match(heroSvg, /local SQLite state/);
    assert.match(heroSvg, /#f6b13a/);
    assert.match(socialSvg, /Graphite\/ink docs shell with cyan and spring-green signals/);
    assert.match(socialSvg, /Project Registry \| Readiness \| Agent Prep/);
    assert.match(socialSvg, /no secret values/);
    assert.match(favicon, /#2dd4ff/);
    assert.match(favicon, /#68f36f/);
    assert.match(index, /<table>/);
    assert.match(commands, /<a class="toc-l2" href="#entity-heading-path">Entity Heading &lt;path&gt;<\/a>/);
    assert.match(commands, /href="commands\/init\/index\.html"/);
    assert.match(commands, /href="commands\/clean\/index\.html"/);
    assert.match(commandRefText, /<h1 id="codemesh-init">codemesh init<\/h1>/);
    assert.match(commandRefText, /<h1 id="codemesh-agent-prepare">codemesh agent prepare<\/h1>/);
    assert.match(commandRefText, /<code>CODEMESH_HOME<\/code>/);
    assert.match(commandRefText, /git clone --bare/);
    assert.match(commandRefText, /Current Limitations/);
    assert.match(commandRefText, /href="\.\.\/\.\.\/commands\.html"/);
    assert.doesNotMatch(commands, /Entity Heading &amp;lt;path&amp;gt;/);
    assert.match(commands, /<pre><code class="language-sh">echo rich-fence/);
    assert.match(commands, /\[Custom scheme inside rich fence\]\(codemesh:thing\)/);
    assert.match(commands, /<p>After rich fence prose\.<\/p>/);
    assert.match(commands, /<pre><code class="language-sh">/);
    assert.match(trust, /id="secret-free-readiness"/);
    assert.match(trust, /<table>/);
    assert.match(quickstart, /CODEMESH_HOME/);
    assert.match(quickstart, /git clone --bare/);
    assert.match(quickstart, /href="commands\.html"/);
    assert.match(quickstart, /href="commands\/status\/index\.html"/);
    assert.match(quickstart, /<small>Previous<\/small>Install/);
    assert.match(quickstart, /<small>Next<\/small>Command Catalog/);
    assert.doesNotMatch(index + llmsFull + commandRefText, /javascript:alert/);
    assert.doesNotMatch(index + llmsFull + commandRefText, /java\tscript:alert/);
    assert.doesNotMatch(commandRefText, /\/Users\/bram|~\/Projects|~\/\.codemesh|GITHUB_TOKEN|GH_TOKEN|TOKEN=|git@github\.com/);
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

test("docs-site emits LLM metadata with canonical and source URLs", () => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "codemesh-docs-site-llms-"));
  const outDir = path.join(tempRoot, "dist", "docs-site");
  fs.cpSync(path.join(root, "docs"), path.join(tempRoot, "docs"), { recursive: true });

  try {
    execFileSync("node", [path.join(root, "scripts", "build-docs-site.mjs")], {
      cwd: root,
      env: {
        ...process.env,
        CODEMESH_DOCS_SITE_ROOT: tempRoot,
        CODEMESH_DOCS_SITE_OUT: outDir,
      },
      encoding: "utf8",
      stdio: "pipe",
    });

    const llms = fs.readFileSync(path.join(outDir, "llms.txt"), "utf8");
    const llmsFull = fs.readFileSync(path.join(outDir, "llms-full.txt"), "utf8");
    assert.match(llms, /Canonical public documentation URLs:/);
    assert.match(llms, /- CodeMesh: https:\/\/bramvr\.github\.io\/codemesh\//);
    assert.match(llms, /Public Markdown source URLs:/);
    assert.match(llms, /- CodeMesh: https:\/\/github\.com\/BramVR\/codemesh\/blob\/main\/docs\/index\.md/);
    assert.match(llms, /Install\/build hint:/);
    assert.match(llms, /Source repository: https:\/\/github\.com\/BramVR\/codemesh/);
    assert.match(llmsFull, /Canonical: https:\/\/bramvr\.github\.io\/codemesh\//);
    assert.match(llmsFull, /Source: https:\/\/github\.com\/BramVR\/codemesh\/blob\/main\/docs\/index\.md/);
    assert.match(llmsFull, /Canonical: https:\/\/bramvr\.github\.io\/codemesh\/commands\/init\//);
    assert.match(llmsFull, /Source: https:\/\/github\.com\/BramVR\/codemesh\/blob\/main\/docs\/commands\/agent-prepare\.md/);
  } finally {
    fs.rmSync(tempRoot, { recursive: true, force: true });
  }
});

test("pages workflow builds, smoke-tests, and safely skips disabled Pages", () => {
  const workflow = fs.readFileSync(path.join(root, ".github", "workflows", "pages.yml"), "utf8");

  for (const expected of [
    "pull_request:",
    "push:",
    "branches:",
    "- main",
    "workflow_dispatch:",
    '"docs/**"',
    '"scripts/build-docs-site.mjs"',
    '"scripts/build-docs-site.test.mjs"',
    '"scripts/docs-site-assets.mjs"',
    '"Makefile"',
    '".github/workflows/pages.yml"',
    "run: make docs-site",
    "run: make docs-site-test",
    "actions/setup-go@4a3601121dd01d1626a1e23e37211e3254c1c06c",
    "go-version-file: go.mod",
    "run: make test",
    "run: make e2e",
    "run: make e2e-packaged",
    "dist/docs-site/index.html",
    "dist/docs-site/sitemap.xml",
    "dist/docs-site/robots.txt",
    "dist/docs-site/llms.txt",
    "dist/docs-site/llms-full.txt",
    "dist/docs-site/favicon.svg",
    "dist/docs-site/assets/codemesh-hero.svg",
    "dist/docs-site/assets/social-card.png",
    "dist/docs-site/assets/social-card.svg",
    "dist/docs-site/.nojekyll",
    'grep -q "CodeMesh" dist/docs-site/index.html',
    'grep -q "https://bramvr.github.io/codemesh/" dist/docs-site/index.html',
    'grep -q "application/ld+json" dist/docs-site/index.html',
    'grep -q "SoftwareApplication" dist/docs-site/index.html',
    "application/ld+json",
    "SoftwareApplication",
    "gh api \"repos/${GITHUB_REPOSITORY}/pages\"",
    "HTTP 404",
    "Pages is not enabled; built and smoke-tested docs-site artifact without deploying.",
    "Pages configuration check failed unexpectedly; not deploying.",
    "actions/configure-pages@45bfe0192ca1faeb007ade9deae92b16b8254a0d",
    "actions/upload-pages-artifact@fc324d3547104276b827a68afc52ff2a11cc49c9",
    "actions/deploy-pages@cd2ce8fcbc39b97be8ca5fce6e763baed58fa128",
  ]) {
    assert.ok(workflow.includes(expected), `workflow should include ${expected}`);
  }

  assert.match(workflow, /if: github\.event_name != 'pull_request' && github\.ref == 'refs\/heads\/main'/);
  assert.match(workflow, /permissions:\n\s+contents: read\n\s+pages: write\n\s+id-token: write/);
});

test("docs-site keeps non-public docs and traversal links out of public artifacts", () => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "codemesh-docs-site-non-public-"));
  const outDir = path.join(tempRoot, "dist", "docs-site");
  const internalSentinel = privateSentinel("INTERNAL", "ONLY", "SENTINEL", "CODEMESH", "PRIVATE", "WORKSPACE");
  const adrSentinel = privateSentinel("ADR", "ONLY", "SENTINEL", "CODEMESH", "PRIVATE", "WORKSPACE");
  const agentSentinel = privateSentinel("AGENT", "ONLY", "SENTINEL", "CODEMESH", "PRIVATE", "WORKSPACE");
  fs.cpSync(path.join(root, "docs"), path.join(tempRoot, "docs"), { recursive: true });
  fs.mkdirSync(path.join(tempRoot, "docs", "research"), { recursive: true });
  fs.writeFileSync(path.join(tempRoot, "docs", "research", "internal.md"), ["# Internal Research", "", internalSentinel, ""].join("\n"), "utf8");
  fs.appendFileSync(path.join(tempRoot, "docs", "adr", "0001-product-boundary.md"), `\n\n${adrSentinel}\n`, "utf8");
  fs.writeFileSync(path.join(tempRoot, "docs", "agent-notes.md"), ["# Agent Notes", "", agentSentinel, ""].join("\n"), "utf8");
  fs.appendFileSync(path.join(tempRoot, "docs", "index.md"), "\n\n[Unsafe traversal](../private.md)\n", "utf8");

  try {
    execFileSync("node", [path.join(root, "scripts", "build-docs-site.mjs")], {
      cwd: root,
      env: {
        ...process.env,
        CODEMESH_DOCS_SITE_ROOT: tempRoot,
        CODEMESH_DOCS_SITE_OUT: outDir,
      },
      encoding: "utf8",
      stdio: "pipe",
    });

    const index = fs.readFileSync(path.join(outDir, "index.html"), "utf8");
    const publicArtifactText = readPublicTextArtifacts(outDir);
    assert.match(index, /<a href="#">Unsafe traversal<\/a>/);
    assert.doesNotMatch(publicArtifactText, new RegExp(escapeRegExp(internalSentinel)));
    assert.doesNotMatch(publicArtifactText, new RegExp(escapeRegExp(adrSentinel)));
    assert.doesNotMatch(publicArtifactText, new RegExp(escapeRegExp(agentSentinel)));
    assert.ok(!fs.existsSync(path.join(outDir, "adr", "0001-product-boundary.html")));
    assert.ok(!fs.existsSync(path.join(outDir, "research", "internal.html")));
    assert.ok(!fs.existsSync(path.join(outDir, "agent-notes.html")));
  } finally {
    fs.rmSync(tempRoot, { recursive: true, force: true });
  }
});

test("docs-site fails generated link validation for missing local Markdown links", () => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "codemesh-docs-site-missing-link-"));
  const outDir = path.join(tempRoot, "dist", "docs-site");
  fs.cpSync(path.join(root, "docs"), path.join(tempRoot, "docs"), { recursive: true });
  fs.appendFileSync(path.join(tempRoot, "docs", "index.md"), "\n\n[Missing local doc](missing-public-page.md)\n", "utf8");

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
      /broken docs links:\nindex\.html: missing-public-page\.html missing/,
    );
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

test("docs-site rejects symlinked markdown and asset sources", () => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "codemesh-docs-site-symlink-source-"));
  const outDir = path.join(tempRoot, "dist", "docs-site");
  fs.cpSync(path.join(root, "docs"), path.join(tempRoot, "docs"), { recursive: true });
  fs.mkdirSync(path.join(tempRoot, "docs", "research"), { recursive: true });
  fs.writeFileSync(path.join(tempRoot, "docs", "research", "internal.md"), "# Internal\n", "utf8");

  try {
    fs.rmSync(path.join(tempRoot, "docs", "index.md"));
    fs.symlinkSync(path.join(tempRoot, "docs", "research", "internal.md"), path.join(tempRoot, "docs", "index.md"));
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
      /unsafe docs-site markdown symlink/,
    );

    fs.rmSync(path.join(tempRoot, "docs", "index.md"));
    fs.copyFileSync(path.join(root, "docs", "index.md"), path.join(tempRoot, "docs", "index.md"));
    fs.rmSync(path.join(tempRoot, "docs", "assets", "codemesh-hero.svg"));
    fs.symlinkSync(path.join(tempRoot, "docs", "research", "internal.md"), path.join(tempRoot, "docs", "assets", "codemesh-hero.svg"));
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
      /unsafe docs-site asset source is not a regular file/,
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

test("docs-site refuses attribute-breaking permalinks", () => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "codemesh-docs-site-permalink-attr-"));
  const outDir = path.join(tempRoot, "dist", "docs-site");
  fs.cpSync(path.join(root, "docs"), path.join(tempRoot, "docs"), { recursive: true });
  fs.writeFileSync(
    path.join(tempRoot, "docs", "index.md"),
    ['---', 'title: "Unsafe Permalink"', 'permalink: /x" onmouseover="alert(1)', '---', '', '# Unsafe'].join("\n"),
    "utf8",
  );

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
      /unsafe docs-site page output path/,
    );
  } finally {
    fs.rmSync(tempRoot, { recursive: true, force: true });
  }
});

function readPublicTextArtifacts(dir) {
  return fs
    .readdirSync(dir, { withFileTypes: true })
    .flatMap((entry) => {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) return readPublicTextArtifacts(full);
      if (!/\.(html|txt|xml|svg)$/i.test(entry.name) && entry.name !== ".nojekyll") return [];
      return fs.readFileSync(full, "utf8");
    })
    .join("\n");
}

function privateSentinel(...parts) {
  return parts.join("_");
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
