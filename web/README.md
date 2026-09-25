# tux-to-any viewer — shadcn UI over the tuxconv CLI

Drag & drop a `.pc` / `.pcf` file (or pick a bundled sample) and watch it break down:

Overview KPIs + charts → IR tables → flow coverage → dispatch-axis scenarios +
scenarioFilter playground → editable mapping drafts → converted tree
(**Tux → Go · Python · C#**) → gentest gap report + generated tests → live CLI logs.

No database, no auth, no LLM in the browser. Every run shells out to the
`tuxconv` CLI inside a disposable tmpdir (`TUXCONV_BIN` or `go run` fallback),
always with `-no-llm` / deterministic drafts. Jobs are in-memory tmpdir
snapshots — a server restart drops them (re-upload; runs are seconds).

## Run it

```sh
# Build the CLI once (preferred: instant runs, no `go run` latency):
go build -o bin/tuxconv ./cmd/tuxconv
export TUXCONV_BIN=$PWD/bin/tuxconv   # optional; else `go run` fallback

cd web
npm install
npm run dev     # http://localhost:3000
npm run build && npm run start   # production
```

Docker (multi-stage: Go binary + standalone Next):

```sh
docker build -f web/Dockerfile -t tux-web .
# Keep it loopback-only — no LAN exposure:
docker run -p 127.0.0.1:3000:3000 tux-web
```

## Privacy — local-only, no phone-home

This viewer is a local tool. Runtime network behavior, verified:

- Browser → only same-origin `/api/*` (upload, discover, convert, …).
  No analytics SDK, no tracking pixel, no Google Fonts, no CDN scripts,
  no Vercel Analytics, no `next/image` remotes. `grep -r "https://"`
  in `web/{app,components,hooks,lib}` hits nothing but comments.
- `tuxconv` CLI → no network in viewer mode: every call runs with
  `-no-llm` / deterministic drafts. The Go `internal/telemetry` package
  is local `slog` file logging (`conversion_logs/logs/`) despite the name.
- Uploaded `.pc` files stay in disposable tmpdirs (`tux-web-*`) on your
  machine; nothing leaves it.

Next.js anonymous telemetry (build counts, not your code) is disabled
three ways — any one suffices, all three are set so clones/CI/containers
are covered:

1. `NEXT_TELEMETRY_DISABLED=1` in `package.json` scripts, `.env.example`,
   and `Dockerfile` (`ENV` + `npx next telemetry disable` at build).
2. Machine-wide opt-out: `npx next telemetry disable`
   (already run here — `npx next telemetry status` → Disabled).
3. Verify anytime: `npm run telemetry:status`.

### Alternatives / mitigations

- **Fully offline after install:** `npm ci --offline` works once the
  cache is warm; `next build` / `next start` need no network. Disconnect
  and the viewer still runs — try it before a demo.
- **No-Next alternative:** the viewer is a thin shell over `tuxconv`.
  Every button maps to one CLI command (`extract`, `flow -scenarios`,
  `discover -list-axes/-filter`, `convertgo/convertbatchpy/convertcs`,
  `gentest`), so a locked-down review can skip the browser entirely.
- **Air-gapped Docker:** multi-stage build needs the network once;
  the runtime image makes no egress. Run with `--network none`
  (after pulling base images) to prove it.

### Dependency risk

Direct deps are minimal and reputable (`npm ls --depth=0`):
`next`, `react`, `react-dom`, six `@radix-ui/*` primitives,
`class-variance-authority`, `clsx`, `tailwind-merge`, `lucide-react`
(icons), plus dev-only `tailwindcss`, `postcss`, `autoprefixer`,
`typescript`, `@types/*`. No analytics, no axios/fetch wrappers, no
obfuscated bundles. `package-lock.json` contains zero `postinstall`
scripts; the only install script is `fsevents` (macOS file-watching,
pulled by Next itself).

`npm audit` on Next 14.2.35 reports GHSA advisories (cache poisoning,
middleware/i18n bypass, Server-Action SSRF/DoS, Image-Optimization RCE,
postcss source-map reads). Exposure here is minimal: the server binds
`127.0.0.1` (dev/start scripts), uses no middleware/i18n/Server Actions/
`next/image` optimization, and only serves trusted-local route handlers.
Mitigations: keep it on loopback, don't expose it to untrusted networks,
and upgrade Next when a non-breaking patched 14.x lands
(`npm audit fix --force` would jump to Next 16 — breaking — so it is
intentionally not applied).

## Architecture (frontend)

Clean separation — routes stay thin, logic lives in `lib`, state in `hooks`:

```text
web/
  app/
    page.tsx              # composition only: header + stepper + tabs + panels
    layout.tsx            # metadata + TooltipProvider
    api/
      jobs/route.ts                 # POST upload → extract + flow + flow.json
      jobs/[id]/scenarios/route.ts  # POST flow -scenarios + discover -list-axes
      jobs/[id]/filter-preview/     # POST discover -filter "<expr>" (read-only)
      jobs/[id]/discover/           # POST draft Go / C# mappings
      jobs/[id]/mapping/            # PUT save edited draft
      jobs/[id]/convert/            # POST convertgo / convertbatchpy / convertcs
      jobs/[id]/lineage/            # GET source→output trace (this part became that part)
      jobs/[id]/gentest/            # POST gap report / generate
      jobs/[id]/files|logs|route    # file read, log tail, job fetch
      samples{,/load}/route.ts      # curated fixtures for one-click demos
  components/
    ui/                   # shadcn primitives (Radix): button, tabs, select,
                          # progress, tooltip, table, card, badge, alert…
    site-header.tsx       # branding + job status
    pipeline-steps.tsx    # Upload → Overview → Scenarios → Mapping → Convert → Tests
    dropzone.tsx          # validated drag & drop (.pc/.pcf, 5MB)
    samples-panel.tsx     # one-click fixtures
    overview-dashboard.tsx# KPI cards + SVG donut/bars + coverage
    ir-explorer.tsx       # searchable condition/query/function/FML tables
    flow-explorer.tsx     # per-function coverage + hints + raw text
    scenario-explorer.tsx # axis registry, slice picker, shared-md, filter playground
    mapping-editor.tsx    # draft Go/C#, edit, save & continue
    convert-panel.tsx     # Tux→Go/Python/C# target cards + file tree + code view + provenance banner
    lineage-explorer.tsx  # Trace tab: pick a source part → see every file it became, with evidence
    tests-panel.tsx       # gap report / generate (Go trees)
    files.tsx             # filterable file tree + copy/download code view
  hooks/use-job.ts        # job + log polling
  hooks/use-lineage.ts    # source→output trace fetch (refetches per converted tree)
  lib/
    api-client.ts         # single home for browser → API calls
    jobs.ts               # Job / FlowReport / ScenarioBundle types + store
    lineage.ts            # evidence-based IR→file matcher (exact/derived/related)
    targets.ts            # Tux→Go/Python/C# catalog
    ir.ts                 # case-tolerant IR accessors
    tuxconv.ts            # CLI spawn helper
```

Every button runs something real: upload, sample, draft, build scenarios,
filter preview, save mapping, convert (per target), gap report, generate,
file open, copy/download, log tail. Busy states disable actions; errors
surface inline; draft-and-stop converts bounce you to the Mapping tab.
