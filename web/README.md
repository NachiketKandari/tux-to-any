# tux-to-any viewer — light Next.js + shadcn-style UI over the tuxconv CLI.
#
# Drag & drop a .pc/.pcf file and watch it break down:
# IR tables (conditions / queries / functions) → flow text → editable
# mapping drafts → converted file tree (Go / Python / C#) → gentest gap
# report + generated tests → live CLI logs.
#
# No database, no auth, no LLM in the browser. Every run shells out to the
# tuxconv CLI inside a disposable tmpdir (TUXCONV_BIN or `go run`), always
# with -no-llm / deterministic drafts. Jobs are in-memory — a server
# restart drops them (re-upload; runs are seconds).

# Build the CLI once (preferred: instant runs, no `go run` latency):
#   go build -o bin/tuxconv ./cmd/tuxconv
#   export TUXCONV_BIN=$PWD/bin/tuxconv   # optional; else `go run` fallback

npm install
npm run dev     # http://localhost:3000
npm run build && npm run start   # production

# Docker (multi-stage: Go binary + standalone Next):
#   docker build -f web/Dockerfile -t tux-web .
#   docker run -p 3000:3000 tux-web
