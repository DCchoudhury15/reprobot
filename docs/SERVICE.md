# reprobot service

`reprobot serve` runs the intake API and the result pages. `reprobot worker`
takes queued reports and checks them with the runner. Both use one Postgres
database and the same data directory.

## No authentication in v0

There is no authentication and no rate limit. Anyone who can reach the
server can submit reports and read every result, including reporter names.
`serve` listens on `127.0.0.1:8080` by default. Keep it on loopback, or put
it behind a reverse proxy that authenticates users. `serve` logs a warning
when `--addr` is not a loopback address.

## Running

```sh
reprobot serve \
  --db postgres://reprobot@localhost/reprobot \
  --data /var/lib/reprobot \
  --projects /etc/reprobot/projects \
  --addr 127.0.0.1:8080

reprobot worker \
  --db postgres://reprobot@localhost/reprobot \
  --data /var/lib/reprobot \
  --projects /etc/reprobot/projects \
  --cache /var/cache/reprobot \
  --concurrency 2 \
  --lease 5m
```

- `--projects` holds one `<project>.yaml` per project. The file name is the
  project id: lowercase letters, digits, `.`, `_` and `-`, at most 64
  characters.
- `--cache` defaults to `DATA/cache`.
- Both commands apply the database migrations at start. An advisory lock
  makes it safe to start several processes at once.
- Workers read reports from the paths the server stored, so `serve` and all
  workers must see the data directory at the same path.
- Stop with SIGINT or SIGTERM. `serve` finishes open requests (at most 30
  seconds). `worker` cancels the jobs it is running and puts them back in
  the queue.

## API

`POST /v1/reports` takes `multipart/form-data`:

| Field | |
|---|---|
| `repro` | the `repro.yaml` (required) |
| `file` | a reporter file, zero or more; stored next to `repro.yaml` under its own name |
| `reporter` | optional name, at most 256 bytes |

- Header `Idempotency-Key` is optional. Sending the same key again returns
  `200` with the ids of the first report and stores nothing.
- The body limit is 60 MB; a larger body gets `413`.
- File names must not be empty, contain `/` or `\`, start with `.`, or be
  `repro.yaml`.
- Only the `project` field of `repro.yaml` is checked here: it must name a
  file in `--projects`. The worker validates the rest.
- Success is `202` with `{"report_id", "job_id", "status_url"}`. Errors are
  `{"error": "..."}` with status `400`, `413` or `500`.

```sh
curl -H 'Idempotency-Key: report-42' \
  -F repro=@repro.yaml -F file=@input.bin -F reporter=alice \
  http://127.0.0.1:8080/v1/reports
```

`GET /v1/reports/{id}` returns the report, its job state, attempts,
`last_error`, the result once there is one, and `duplicates`: ids of other
reports in the same project with the same crash signature. Unknown or
malformed ids give `404`.

`GET /reports/{id}` is the same as an HTML page, with no JavaScript.

`GET /healthz` returns `ok`, or `503` when the database does not answer.

## Jobs

A job is `queued`, `running`, `done` or `failed`.

- A worker claims a job with a lease and renews it every lease/3 while the
  job runs. If the worker dies, the lease expires and another worker takes
  the job. A worker that lost its lease cannot write a result.
- A runner error (Docker down, disk full) puts the job back in the queue
  after a backoff of 1s * 2^attempts (2s, then 4s; at most 5 minutes). After
  3 attempts the job is `failed`. Bad input is not an error: it gives a
  result with state `INVALID_INPUT`.
- A job whose lease expires on its last attempt becomes `failed`.

## Data layout

```
DATA/reports/<id>/repro.yaml   reporter files next to it (dirs 0700, files 0600)
DATA/tmp/                      uploads in progress
DATA/cache/                    default worker cache
```

## Tests

The store, worker and server tests need Postgres. By default they start a
`postgres:18-alpine` container with Docker and remove it at the end. Set
`REPROBOT_TEST_DATABASE_URL` to use an existing server instead (the user
must be allowed to create databases). Without either, these tests are
skipped.
