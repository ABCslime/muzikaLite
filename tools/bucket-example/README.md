# muzika similarity bucket plugins — author guide

muzika v0.6 loads similarity buckets from two places:

1. **Built-ins** — Go code in `internal/similarity/buckets/`, compiled into the muzika binary.
2. **Plugins** — external executables discovered at startup under `MUZIKA_BUCKET_PLUGIN_DIR`. This directory is what the guide is about.

Plugins run as long-lived child processes, speak JSON-RPC over stdio, and register on the same `similarity.Bucket` interface the built-ins implement. The engine treats both identically.

## Quick start

```
# 1. Put your plugin under the scan dir, one subdirectory per plugin.
mkdir -p ~/.muzika/buckets/example

# 2. Compile the reference plugin into that subdirectory.
#    The executable MUST be named "bucket" (no extension).
go build -o ~/.muzika/buckets/example/bucket ./tools/bucket-example

# 3. Start muzika with the scan dir pointed at that tree.
MUZIKA_BUCKET_PLUGIN_DIR=~/.muzika/buckets muzika
```

On startup you should see:

```
INFO plugin loader: registered plugin=example bucket_id=example.echo_artist default_weight=1
INFO similarity: registered Discogs buckets buckets=5   # 5 built-ins
```

Your plugin now shows up in Settings → Discovery weights as a slider, and starts receiving `candidates` calls whenever the active user has similar mode on.

## Directory layout

```
$MUZIKA_BUCKET_PLUGIN_DIR/
  plugin-name-one/
    bucket          ← required; any executable
    README.md       ← optional; muzika ignores it
    data.db         ← plugin's private state, if any
  plugin-name-two/
    bucket
```

Only the executable named `bucket` is scanned. Anything else — config files, state, logs, READMEs — is the plugin's private business. muzika never writes into a plugin's directory.

## Wire protocol (v1)

JSON-RPC 2.0 messages, newline-delimited, over the child's stdio:

- **stdin**: muzika writes one request per line
- **stdout**: plugin writes one response per line
- **stderr**: inherited by muzika — your fprintf to stderr shows up in muzika's journal

Every message fits on exactly one line. The scanner's buffer is 1 MB per message; plugins that exceed that should chunk or paginate their output.

### `hello`

Called once at spawn time.

**Request:**

```json
{"jsonrpc":"2.0","id":1,"method":"hello","params":{"muzika_version":"0.6.0","protocol_version":"1"}}
```

**Response:**

```json
{"jsonrpc":"2.0","id":1,"result":{
  "id":"yourns.your_bucket",
  "label":"Human-friendly name",
  "description":"One-sentence description for the Settings UI.",
  "default_weight":2.0
}}
```

Fields:

| field | type | required | notes |
|---|---|---|---|
| `id` | string | yes | Stable. Use `yourns.name`. Persisted as the key in the user's bucket-weight JSON — renaming breaks saved tuning. |
| `label` | string | yes | Shown in Settings. |
| `description` | string | no | Shown as subtitle in Settings. |
| `default_weight` | number | no | 0–10 convention. Zero = disabled until user opts in. Missing defaults to 1.0. |

`hello` has a **5-second timeout**. A plugin that hasn't replied within 5s is marked failed and skipped. JIT startup, cold caches, slow imports are fine — 5s is generous for normal plugin startup.

### `candidates`

Called on every refill cycle while similar mode is active and your bucket's user weight is > 0.

**Request:**

```json
{"jsonrpc":"2.0","id":42,"method":"candidates","params":{
  "seed":{
    "title":"One More Time",
    "artist":"Daft Punk",
    "discogs_release_id":82147,
    "discogs_artist_id":1289,
    "discogs_label_id":23528,
    "year":2000,
    "styles":["House","Disco"],
    "genres":["Electronic"],
    "collaborators":[99001,99002]
  }
}}
```

Every seed field is optional. Plugins should bail out gracefully (return empty) when the fields they need are missing. A Bandcamp-only seed arrives with `title` + `artist` set and all Discogs IDs at zero.

**Response:**

```json
{"jsonrpc":"2.0","id":42,"result":{
  "candidates":[
    {"title":"Aerodynamic","artist":"Daft Punk","confidence":0.9},
    {"title":"D.A.N.C.E.","artist":"Justice","confidence":0.6,"edge":{"festival":"Sónar 2007"}}
  ]
}}
```

Candidate fields:

| field | type | required | notes |
|---|---|---|---|
| `title` | string | yes | Empty = dropped by muzika. |
| `artist` | string | yes | Empty = dropped. Case-insensitive dedup key with title. |
| `confidence` | number | no | 0–1. Unset treated as 1.0. Multiplied by the user's bucket weight. |
| `image_url` | string | no | Cover art URL; flows into the queue row. |
| `edge` | object | no | Provenance metadata for the v0.7 graph view. Arbitrary keys; muzika doesn't interpret. |

`candidates` has a **2-second timeout**. A plugin that hasn't replied within 2s returns empty for this cycle; muzika moves on without stalling. Slow upstream APIs should cache aggressively or return partial results.

Empty `candidates` array is a valid "nothing for this seed" response, not an error.

## Error handling

Return a JSON-RPC error for protocol-level failures:

```json
{"jsonrpc":"2.0","id":42,"error":{"code":-32602,"message":"bad seed shape"}}
```

muzika treats a well-formed error as "no candidates this cycle" — same as an empty list. Don't use errors for "this seed isn't a good fit"; use an empty candidates list.

## Lifecycle + crash handling

- **Startup**: muzika forks your process once. Stay alive for the lifetime of muzika.
- **Shutdown**: muzika closes your stdin. The canonical exit sequence is "stdin EOF → flush stdout → exit 0". Two-second grace period before muzika escalates to SIGKILL.
- **Crash**: if your process exits unexpectedly, muzika's supervisor respawns it. Backoff schedule: 1s, 5s, 30s, 5 min, 5 min, then the plugin is marked dead until muzika restarts. Five consecutive crashes within the backoff window is the cap.
- **Respawn reset**: one successful `hello` + first candidates response resets the crash counter. A plugin that crashes every hour hits the cap once per week, not once per hour.

## Security

Plugins run as the muzika user, no sandbox. The trust model is "you dropped this binary on your own server." Don't install plugins from people you don't trust.

If you need to ship plugins as a package, distribute the source (like this reference) rather than pre-compiled binaries — users who trust the source review it and compile locally.

## Versioning

The `protocol_version` field in the `hello` params is muzika's contract version. v0.6 ships `"1"`. Safe changes within v1:

- muzika adds new optional fields to the seed.
- plugins add new optional fields to the candidate (muzika drops unknown ones).

Breaking changes bump to v2 and muzika's own minor version; plugins branch on `protocol_version` in their hello response handling if they need to support multiple muzika releases.

## See also

- `internal/similarity/plugin/protocol.go` — the authoritative wire types.
- `internal/similarity/plugin/manager.go` — loader + supervisor implementation.
- `tools/bucket-example/main.go` — this plugin's source, under 150 lines, the copy-paste starting point for your own bucket.
