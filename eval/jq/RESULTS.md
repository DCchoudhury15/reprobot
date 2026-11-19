# jq results

Written by `go test -tags eval ./eval/`. Times include the build when it was not cached.

## Real bugs

| Bug | Vulnerable commit | Verdict | Crash | Signature | Crash line | Time | Fix commit | Verdict | Time |
|---|---|---|---|---|---|---|---|---|---|
| CVE-2023-50246 | `c9a5156` | REPRODUCED_AS_CLAIMED | heap-buffer-overflow in decToString | `37277a99860961d5` | `*c='\0'; // terminate the string (all paths)` | 27s | `71c2ab5` | NOT_REPRODUCED | 28s |
| CVE-2023-50268 | `c5fd64b` | REPRODUCED_AS_CLAIMED | stack-buffer-overflow in decNumberCopy | `3bcf0a0095071c74` | `for (s=src->lsu+1; s<smsup; s++, d++) *d=*s;` | 28s | `c9a5156` | NOT_REPRODUCED | 2s (cached build) |

## Reports about bugs that do not exist

| Report | Verdict | Claim findings | Time |
|---|---|---|---|
| a harmless number, claims the CVE-2023-50246 heap overflow | NOT_REPRODUCED |  | 2s (cached build) |
| claims a heap overflow in a function that does not exist | NOT_REPRODUCED | function "jvp_number_scientific_print" does not appear anywhere in the code at c9a5156 | 2s (cached build) |
