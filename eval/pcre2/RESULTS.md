# pcre2 results

Written by `go test -tags eval ./eval/`. Times include the build when it was not cached.

## Real bugs

| Bug | Vulnerable commit | Verdict | Crash | Signature | Crash line | Time | Fix commit | Verdict | Time |
|---|---|---|---|---|---|---|---|---|---|
| CVE-2026-86145 | `ec9c286` | REPRODUCED_AS_CLAIMED | heap-buffer-overflow in internal_dfa_match | `05a5f6f7c62d7784` | `ADD_NEW((int)(end_code - start_code + length), 0);` | 2s (cached build) | `c932e70` | NOT_REPRODUCED | 3s (cached build) |

## Reports about bugs that do not exist

| Report | Verdict | Claim findings | Time |
|---|---|---|---|
| the CVE-2026-86145 pattern without the heap limit, claims the same overflow | NOT_REPRODUCED |  | 3s (cached build) |
| claims a heap overflow in a function that does not exist | NOT_REPRODUCED | function "dfa_workspace_grow" does not appear anywhere in the code at ec9c286 | 3s (cached build) |
