# curl results

Written by `go test -tags eval ./eval/`. Times include the build when it was not cached.

## Real bugs

| Bug | Vulnerable commit | Verdict | Crash | Signature | Crash line | Time | Fix commit | Verdict | Time |
|---|---|---|---|---|---|---|---|---|---|
| CVE-2022-42915 | `9d0869d` | REPRODUCED_AS_CLAIMED | double-free in Curl_free_request_state | `861da6a6bfae0086` | `Curl_safefree(data->req.p.http);` | 1s (cached build) | `55e1875` | NOT_REPRODUCED | 1s (cached build) |

## Reports about bugs that do not exist

| Report | Verdict | Claim findings | Time |
|---|---|---|---|
| the CVE-2022-42915 refused proxy with an http URL, which was never affected, claims the same double free | NOT_REPRODUCED |  | 1s (cached build) |
| a SOCKS5 redirect to a 200 byte hostname, claims the CVE-2023-38545 heap overflow | NOT_REPRODUCED |  | 1s (cached build) |
