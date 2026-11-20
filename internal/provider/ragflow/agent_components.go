package ragflow

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

// componentCatalogTTL is how long a fetched component catalog is trusted. The
// engine only changes it when it is redeployed, so a few minutes of staleness is
// free; the cache exists to keep authoring validation off the hot path.
const componentCatalogTTL = 5 * time.Minute

// ComponentCatalog returns the component names the live engine registers.
//
// Validation uses it to reject `component_name: "Answer"`-style mistakes at save
// time instead of at the first chat ("Internal storage error while accessing the
// agent"). Names are lowercased because the engine's registry is
// case-insensitive, and callers only ever compare that way.
//
// A transport failure is returned as-is: the caller must degrade to "cannot
// check component names", never to "component is unknown" (that would fail a
// valid canvas because the engine hiccuped).
func (c *HTTPClient) ComponentCatalog(ctx context.Context) (map[string]bool, error) {
	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()
	if len(c.catalog) > 0 && time.Since(c.catalogAt) < componentCatalogTTL {
		out := make(map[string]bool, len(c.catalog))
		for name := range c.catalog {
			out[name] = true
		}
		return out, nil
	}
	var rows []struct {
		Name     string `json:"name"`
		Category string `json:"category"`
	}
	if err := c.do(ctx, http.MethodGet, "/components", nil, "", &rows); err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(rows))
	for _, row := range rows {
		name := strings.ToLower(strings.TrimSpace(row.Name))
		if name != "" {
			names[name] = true
		}
	}
	if len(names) == 0 {
		// An empty catalog would reject every node; treat it as "unavailable".
		return nil, nil
	}
	c.catalog, c.catalogAt = names, time.Now()
	out := make(map[string]bool, len(names))
	for name := range names {
		out[name] = true
	}
	return out, nil
}

// catalogCache is embedded in HTTPClient so the catalog outlives one request.
type catalogCache struct {
	catalogMu sync.Mutex
	catalog   map[string]bool
	catalogAt time.Time
}
