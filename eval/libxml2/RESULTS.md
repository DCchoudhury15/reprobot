# libxml2 results

Written by `go test -tags eval ./eval/`. Times include the build when it was not cached.

## Real bugs

| Bug | Vulnerable commit | Verdict | Crash | Signature | Crash line | Time | Fix commit | Verdict | Time |
|---|---|---|---|---|---|---|---|---|---|
| CVE-2025-49794 | `24d7e15` | REPRODUCED_AS_CLAIMED | heap-use-after-free in xmlSchematronFormatReport | `6eb90cbbb7b55c98` | `if ((node->ns == NULL) \|\| (node->ns->prefix == NULL))` | 38s | `71e1e8a` | NOT_REPRODUCED | 25s |
| CVE-2025-49796 | `24d7e15` | REPRODUCED_AS_CLAIMED | SEGV in xmlSchematronFormatReport | `2d093e7202494122` | `if ((node->ns == NULL) \|\| (node->ns->prefix == NULL))` | 1s (cached build) | `71e1e8a` | NOT_REPRODUCED | 1s (cached build) |
| CVE-2025-49795 | `069bcda` | REPRODUCED_AS_CLAIMED | SEGV in xmlSchematronFormatReport | `2d093e7202494122` | `switch (eval->type) {` | 25s | `499bcb7` | NOT_REPRODUCED | 26s |
| CVE-2025-24928 | `5880a9a` | REPRODUCED_AS_CLAIMED | stack-buffer-overflow in xmlSnprintfElements | `00f99f190f5aa435` | `strcat(buf, (char *) cur->name);` | 27s | `8c8753a` | NOT_REPRODUCED | 28s |
| CVE-2023-28484 | `cb1b8b8` | REPRODUCED_AS_CLAIMED | SEGV in xmlSchemaFixupComplexType | `57624be594b90e0c` | `(xmlSchemaTreeItemPtr) xmlSchemaAddParticle(pctxt,` | 29s | `e4f85f1` | NOT_REPRODUCED | 28s |

## Reports about bugs that do not exist

| Report | Verdict | Claim findings | Time |
|---|---|---|---|
| harmless long text, claims a heap overflow in a real function | NOT_REPRODUCED |  | 1s (cached build) |
| claims a stack overflow in a function that does not exist | NOT_REPRODUCED | function "xmlParseAttributeBuffer" does not appear anywhere in the code at 71e1e8a | 1s (cached build) |
| the input is a pasted sanitizer report that xmllint prints back | NOT_REPRODUCED |  | 1s (cached build) |
