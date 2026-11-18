# reprobot service

`reprobot serve` runs the intake API and the result pages. `reprobot worker`
takes queued reports and checks them with the runner. Both use one Postgres
database and the same data directory.

## Tokens

Reports describe bugs that are not public yet, so the API should not be
open. Make a token per person or system:

```sh
reprobot token alice >> /etc/reprobot/tokens
```

The token is printed once, on stderr. The file line, on stdout, is the name
and the sha256 of the token, so the tokens file holds no secrets. Start
`serve` with `--tokens /etc/reprobot/tokens`. Then every route except
`/healthz` needs a token:

- `Authorization: Bearer TOKEN`, or
- Basic auth with the token as the password (any user name), so a browser
  can open the result pages.

A missing or wrong token gets `401`, before the upload is read. To remove a
token, delete its line and restart `serve`.

Any token can submit a report for any project. Who can read a report
depends on the token:

- A reporter token (a plain `NAME SHA256` line) reads only the reports it
  submitted.
- A maintainer token has a third field, the projects whose reports it
  reads: `NAME SHA256 curl,libxml2`, or `NAME SHA256 *` for every project.
  Add it by hand after `reprobot token` prints the line.

A report the token may not read gets `404`, the same as an id that does not
exist. The duplicate and related lists on a report only show reports the
token may read, so a reporter does not learn that someone else sent the
same crash. An `Idempotency-Key` is unique per token: two reporters who
pick the same key get two reports. Reports sent while the server ran
without tokens have no owner, so only maintainer tokens read them.

Each report costs a build and three sandboxed runs, so a token may send
at most `--reports-per-hour` reports (default 20, `0` turns it off) in any
hour. Over that, `POST /v1/reports` gets `429` with a `Retry-After` header
in seconds. A request without an `Idempotency-Key` is refused before its
upload is read; repeating an earlier request with the same key returns the
earlier report and does not count. The count lives in the database, so it
holds across restarts and several `serve` processes. Without `--tokens`
there is no limit.

Without `--tokens` the API is open;
`serve` then refuses a non-loopback `--addr` unless `--insecure-no-auth` is
given, for use behind a reverse proxy that authenticates users. Serve over
TLS (for example behind that proxy) so tokens are not sent in clear text.

## Running

```sh
reprobot serve \
  --db postgres://reprobot@localhost/reprobot \
  --data /var/lib/reprobot \
  --projects /etc/reprobot/projects \
  --tokens /etc/reprobot/tokens \
  --sign-pub /etc/reprobot/signing.pub \
  --addr 127.0.0.1:8080

reprobot worker \
  --db postgres://reprobot@localhost/reprobot \
  --data /var/lib/reprobot \
  --projects /etc/reprobot/projects \
  --cache /var/cache/reprobot \
  --concurrency 2 \
  --lease 5m \
  --sign-key /etc/reprobot/signing.key
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
  `{"error": "..."}` with status `400`, `401`, `413`, `429` or `500`.

```sh
curl -H "Authorization: Bearer $REPROBOT_TOKEN" \
  -H 'Idempotency-Key: report-42' \
  -F repro=@repro.yaml -F file=@input.bin -F reporter=alice \
  http://127.0.0.1:8080/v1/reports
```

`GET /v1/reports/{id}` returns the report, its job state, attempts,
`last_error`, the result once there is one, and `duplicates`: ids of other
reports in the same project with the same crash signature and crash line,
and `related`: ids with the same signature that crash on a different line
(see Dedup in DESIGN.md). Each list holds at most 100 ids. Unknown or
malformed ids give `404`. `repro_hash` is the same value the result later
shows as `inputs.reproducer_hash`: a hash of `repro.yaml` and the files it
lists. Uploaded files it does not list are not mounted and not hashed. It is
missing when `repro.yaml` is not valid; the result then says why.

`GET /reports/{id}` is the same as an HTML page, with no JavaScript.

`GET /v1/reports/{id}/signed` returns the signed result (see Signed results
below), with the same access rules as the report. It is `404` when the
report has no signed result. The report JSON has `"signed": true` when it
has one.

`GET /v1/signing-key` returns the public key from `--sign-pub` as PEM. It
needs no token, and is `404` when no key is set.

`GET /healthz` returns `ok`, or `503` when the database does not answer.

## Signed results

Make a key with `reprobot keygen /etc/reprobot` and start every worker with
`--sign-key /etc/reprobot/signing.key`. Each result is then signed when its
job finishes, with the report id set in the result, so a signed verdict
names the report it is for. The envelope is stored exactly as signed (in a
`text` column, since `jsonb` would rewrite it). Give `serve` the public key
with `--sign-pub` to publish it.

Anyone can then check a result; fetching the key needs no token:

```sh
curl -s http://127.0.0.1:8080/v1/signing-key > signing.pub
curl -s -H "Authorization: Bearer $REPROBOT_TOKEN" \
  http://127.0.0.1:8080/v1/reports/$ID/signed | reprobot verify --key signing.pub -
```

Results finished before the worker had a key are not signed afterwards.

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
