# Box Module

`box` is Watcher's configurable information-surface component.

It is the public example for non-agent modules:

```text
box = sources + datasets + views + signals
```

## Positioning

Box lets a self-hosted owner describe information sources and mobile views with
configuration. Android does not need a rebuild when a box is added or reshaped;
it fetches the latest catalog, view schema, and dataset records from service.

The first public example is an LLM leaderboard fixture:

- [examples/llm_leaderboard.box.json](examples/llm_leaderboard.box.json)
- [examples/llm_leaderboard.fixture.json](examples/llm_leaderboard.fixture.json)

Private scraper-backed boxes can use the same schema under `modules/box/private/`.
That directory is not part of the public export.

## Box Definition

A `.box.json` file defines:

- `sources`: where records come from, such as `fixture_json` or an internal
  `adapter_query`
- `datasets`: normalized record collections with stable IDs
- `views`: presentation schemas such as `ranking`, `table`, or `list`
- `default_views`: view order for Android
- `signals`: short facts that can later feed ShellHome

Service scans these directories at query time:

- `modules/box/examples/`
- `modules/box/private/`

Adding or editing a `.box.json` file is a hot update for Android. The next
refresh uses the new catalog.

## API Surface

Box keeps its query API intentionally small:

- `GET /api/v2/box/adapters`
- `GET /api/v2/box/query/{box_id}/catalog`
- `POST /api/v2/box/query/{box_id}/dataset`
- `POST /api/v2/box/query/{box_id}/view`
- `GET /api/v2/box/query/{box_id}/signals`

Legacy task/feed endpoints still exist while older task tooling is being
absorbed into the new box shape.

## Android Contract

Android understands generic presentation data only:

- adapter catalog
- dataset records
- view columns
- grouping field
- view type

The top-level Android surface is the source catalog. A user enters a box source
first, then sees that source's default views. A leaderboard is only one possible
view type inside a source; it is not the Box module entry.

Android must not know source-specific fields such as a contest name, private
scraper cache layout, or provider-specific API details.

## Non-Goals

- custom transport
- component-owned auth or sync
- Android screens per source
- public export of private scrapers
- a general low-code dashboard builder
