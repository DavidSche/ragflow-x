// Package webfs serves the built frontend (web/dist) from the server binary
// via go:embed, so a single release binary provides both the API and the UI.
//
// The build environment selects which asset set gets embedded at compile time:
//
//   - dist_placeholder.go (default, no build tag): embeds placeholder/, a
//     tiny placeholder page. Keeps `go build ./...`, `go vet` and the baseline
//     test pipeline working before the frontend has been built locally
//     (web/dist is gitignored, so it usually does not exist).
//   - dist_embedded.go (tag `webfs_embed`): embeds all:dist — the real Vite
//     production build. CI and release builds set `-tags=webfs_embed` AFTER
//     `npm run build` has produced web/dist.
//
// Handler() wires the selected FS into a Gin no-route handler with SPA
// fallback: unknown non-/api paths fall back to index.html so deep links like
// /datasets/123 work after a hard refresh.
package webfs
