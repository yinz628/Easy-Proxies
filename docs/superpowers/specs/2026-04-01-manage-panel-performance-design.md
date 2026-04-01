# Manage Panel Performance Design

**Goal:** Keep the management page smooth with about `20,000` nodes, with fast first paint, responsive filtering, and preserved batch-selection workflows.

## Success Criteria

- Opening the management page issues one list query instead of two full-data queries.
- The browser only renders the current page of rows, not the full node set.
- Filtering and sorting no longer run on the full dataset in the browser.
- Cross-page selection remains available for batch probe, batch quality check, batch enable/disable, batch delete, and export selected nodes.
- On a dataset of about `20,000` nodes:
  - initial page load remains responsive
  - changing filters or sort order feels smooth
  - batch actions do not require preloading every matching row into the browser

## Scope

- Redesign management-page list loading around a dedicated paginated query API.
- Move merge, filter, sort, and pagination logic from the browser to the backend.
- Preserve current row actions and batch actions.
- Preserve the existing management-page summary cards and filter controls.
- Keep quality-check detail and row expansion as on-demand operations.

## Non-Goals

- No redesign of node probe logic or quality-check scoring rules.
- No change to node persistence format.
- No requirement to make every existing API paginated; this work only targets the management-page path.

## Existing Problems

- The management page currently loads config nodes and runtime nodes separately, then merges them in the browser.
- The browser computes `mergedNodes -> filteredNodes -> sortedNodes` from the full dataset on every relevant state change.
- The table renders every filtered row at once.
- The backend endpoints used by the page return full datasets:
  - `/api/nodes`
  - `/api/nodes/config`
- Runtime snapshot generation also performs a full copy and sort before responding.

This creates three stacked bottlenecks:

1. Full-response JSON serialization and transfer.
2. Full-data merge, filter, and sort work in React.
3. Large DOM render and reconciliation cost.

## Recommended Architecture

### 1. Add a dedicated management query API

Add a new endpoint:

- `GET /api/nodes/manage`

Query parameters:

- `page`
- `page_size`
- `keyword`
- `status`
- `region`
- `source`
- `sort_key`
- `sort_dir`

The endpoint returns already-merged management rows plus pagination and summary metadata.

Example response shape:

```json
{
  "items": [],
  "page": 1,
  "page_size": 100,
  "total": 20000,
  "filtered_total": 3560,
  "summary": {
    "normal": 12000,
    "pending": 3000,
    "unavailable": 4200,
    "blacklisted": 500,
    "disabled": 300
  },
  "facets": {
    "regions": ["HK", "JP", "US"],
    "sources": ["manual", "sub-a", "sub-b"]
  }
}
```

`items` should include the row fields already needed by the management page, including:

- config fields: `name`, `uri`, `port`, `username`, `password`, `source`, `disabled`
- runtime fields: `tag`, `runtime_status`, `latency_ms`, `region`, `country`, `active_connections`, `success_count`, `failure_count`
- quality summary fields already stored in config

The management page should stop calling the current full-data pair for its main list path.

### 2. Move list computation to the backend

The backend should own these operations for the management page:

- config/runtime merge
- status derivation
- keyword filtering
- region/source/status filtering
- sort ordering
- page slicing
- summary counts
- filter facet generation

This removes the current browser-side full-data pipeline and makes the list query proportional to current page size instead of total dataset size.

For the first implementation, the backend may still build the query result from in-memory config/runtime snapshots per request, because `20,000` in-memory rows is reasonable in Go. The important change is that only the filtered page is serialized back to the browser. If profiling later shows backend query latency becoming a bottleneck, a second-stage in-memory cached index can be added behind the same API without changing the frontend contract.

### 3. Convert the frontend to query-driven state

`ManagePanel` should stop storing full `configNodes` and full `monitorData` for the list view. Instead it should hold:

- current query state
- current page result
- loading/error state for the current query
- a small page cache keyed by `query + page`
- local caches for detail-only data such as quality-check detail

Behavior rules:

- initial load requests page `1`
- changing filters resets to page `1`
- keyword input uses a debounce window of about `200-300ms`
- new queries cancel in-flight older requests
- sort changes trigger a new server query rather than local array sorting
- row detail panels continue to load on demand

### 4. Preserve batch-selection behavior across pages

The current `selectedNodes: Set<string>` model is only safe when the page has the full filtered list. With pagination, selection must become query-aware.

Use a selection model with two modes:

```ts
type SelectionState =
  | { mode: 'names'; names: Set<string> }
  | { mode: 'filter'; filter: ManageQuery; excludeNames: Set<string> }
```

Rules:

- Row checkbox toggles add or remove explicit node names.
- Header checkbox selects or clears only the current page.
- The batch bar offers an additional action: select all rows matching the current filter.
- In `filter` mode, unchecking one row only adds that row to `excludeNames`.

This keeps the current batch UX while avoiding the need to download or store every matching row name in the browser.

### 5. Upgrade batch-action payloads to support selection semantics

Batch APIs that currently require `names[]` or `tags[]` should accept a shared `selection` payload.

Example request:

```json
{
  "selection": {
    "mode": "filter",
    "filter": {
      "keyword": "hk",
      "status": "normal",
      "region": "HK",
      "source": "sub-a"
    },
    "exclude_names": ["node-1", "node-9"]
  }
}
```

Applies to:

- batch probe
- batch quality check
- batch enable/disable
- batch delete
- export selected nodes

For explicit selections, the payload may still carry names directly. For filter-wide selections, the backend resolves the matching node set using the same query logic as `/api/nodes/manage`.

### 6. Keep detail and quality data lazy

Large-list optimization should not force every row to load full detail eagerly.

Rules:

- row detail remains collapsed by default
- quality summary renders from list data
- detailed quality items load only when the user opens a node detail panel and no local cache is available
- batch probe and batch quality completion invalidate only affected page caches instead of reloading the full page model

## API Contract Details

### Query semantics

- `total` is the total configured node count before list filters.
- `filtered_total` is the count after applying all active list filters and before pagination.
- `summary` is calculated from the active query while ignoring pagination and sort parameters.
- `facets` are calculated from the active query while ignoring pagination and sort parameters.
- Batch-selection resolution uses only filter fields and never depends on sort or page parameters.

### List API

`GET /api/nodes/manage`

Request:

- pagination params are optional and validated to safe bounds
- unsupported sort keys fall back to default sort

Response:

- `items`
- `page`
- `page_size`
- `total`
- `filtered_total`
- `summary`
- `facets`

### Batch APIs

Support both of these shapes:

```json
{ "selection": { "mode": "names", "names": ["a", "b"] } }
```

```json
{
  "selection": {
    "mode": "filter",
    "filter": { "status": "normal", "region": "HK" },
    "exclude_names": ["a"]
  }
}
```

The backend should validate that:

- selection payload is present
- names are non-empty when using `names` mode
- sort and page fields are ignored for batch selection resolution
- resolved node count is returned in the batch response for operator clarity

## Error Handling

- Invalid pagination or sort parameters return `400`.
- If a query references a now-missing filter value, the list returns an empty result instead of failing.
- Batch actions should report how many nodes were resolved and how many succeeded or failed.
- If a batch action resolves zero nodes, return a user-facing validation error instead of silently succeeding.

## Rollout Plan

### Phase 1

- Add `GET /api/nodes/manage`
- Switch the management page list to this endpoint
- Replace browser-side full merge/filter/sort with server-driven query state
- Add page navigation and current-page selection

### Phase 2

- Add filter-wide selection mode
- Upgrade batch APIs to shared `selection` payloads
- Upgrade export-selected to backend-resolved selection export

### Phase 3

- Add page-result cache invalidation rules after probe, quality, enable/disable, delete, and import
- Measure backend query cost and add an internal cached index only if profiling justifies it

## Risks

- The current management page is large, so the list-query refactor should extract focused helpers instead of adding more inline logic.
- Batch APIs currently assume explicit names or tags; migrating them needs careful compatibility handling.
- Summary counts and filter facets must stay aligned with the active query semantics, or the UI will feel inconsistent.
- Export-selected currently works from locally loaded rows; it must move to backend selection resolution to remain correct after pagination.

## Verification

- Add backend tests for manage query filtering, sorting, pagination, and summary counts.
- Add frontend tests for query-state helpers and selection-state helpers where practical without introducing unnecessary dependencies.
- Run frontend build verification after the list-path refactor.
- Run backend tests covering the new query and batch-selection resolution path.
- Perform a manual smoke check against a seeded large dataset, targeting about `20,000` nodes.
