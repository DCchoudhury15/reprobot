`advisory_reported.json` is a `repository_advisory` delivery with action
`reported`, written by hand from the payload schema in GitHub's docs
(webhook events and the repository advisories REST API). It is not a
captured delivery: replace it with one once the App is registered. In the
description, `COMMIT` and `INPUT` are filled in by the server tests with the
tinyparse fixture's vulnerable commit and its input, in base64.
