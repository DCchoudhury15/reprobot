/* Generated from a real run of every scenario through reprobot's Go
   pipeline (gcc:15 image, Docker, runc) on 2026-11-09. Do not edit by hand. */
window.RB_DATA = {
 "recorded": "2026-11-09",
 "commits": {
  "vuln": "d7ad02c",
  "fix": "5005d5a",
  "revert": "245af6f"
 },
 "reports": [
  {
   "id": "vuln",
   "title": "Heap buffer overflow in parse_header",
   "sev": "high",
   "test": "TestE2EVulnFixRevert",
   "testUrl": "https://github.com/DCchoudhury15/reprobot/blob/main/internal/runner/runner_e2e_test.go#L59",
   "files": [
    "repro.yaml",
    "input.bin · 65 B"
   ],
   "commit": "d7ad02c",
   "claim": "The memcpy in parse_header in tinyparse.c copies up to 255 bytes into a 16 byte heap buffer.",
   "yaml": [
    "commit: d7ad02c79a93a1081a9a60e43629021dc140e650",
    "profile: asan",
    "cmd: [\"bin/tinyparse\", \"poc/input.bin\"]",
    "timeout_seconds: 20",
    "sanitizer: address",
    "kind: heap-buffer-overflow",
    "frame: parse_header"
   ],
   "state": "REPRODUCED_AS_CLAIMED",
   "reason": "crashed in 3 of 3 runs, as claimed",
   "findings": [],
   "cacheHit": true,
   "built": true,
   "wallMs": 912,
   "runs": [
    {
     "exit": 99,
     "ms": 149,
     "crash": true,
     "timedOut": false,
     "note": ""
    },
    {
     "exit": 103,
     "ms": 142,
     "crash": true,
     "timedOut": false,
     "note": ""
    },
    {
     "exit": 88,
     "ms": 152,
     "crash": true,
     "timedOut": false,
     "note": ""
    }
   ],
   "evidence": {
    "kind": "asan",
    "label": "AddressSanitizer log, run 1 (from the secret log directory)",
    "text": "=================================================================\n==1==ERROR: AddressSanitizer: heap-buffer-overflow on address 0x79a81a5e0020 at pc 0x7d881b9be7e2 bp 0x7fffdb53bb90 sp 0x7fffdb53b350\nWRITE of size 64 at 0x79a81a5e0020 thread T0\n    #0 0x7d881b9be7e1 in memcpy (/usr/local/lib64/libasan.so.8+0x1177e1)\n    #1 0x00000040129c in parse_header /src/tinyparse.c:21\n    #2 0x0000004013f4 in parse_file /src/tinyparse.c:35\n    #3 0x0000004014ed in main /src/tinyparse.c:46\n    #4 0x7d881b6dcca7  (/lib/x86_64-linux-gnu/libc.so.6+0x29ca7) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)\n    #5 0x7d881b6dcd64 in __libc_start_main (/lib/x86_64-linux-gnu/libc.so.6+0x29d64) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)\n    #6 0x000000401160 in _start (/work/bin/tinyparse+0x401160)\n\n0x79a81a5e0020 is located 0 bytes after 16-byte region [0x79a81a5e0010,0x79a81a5e0020)\nallocated by thread T0 here:\n    #0 0x7d881b9c108b in malloc (/usr/local/lib64/libasan.so.8+0x11a08b)\n    #1 0x000000401274 in parse_header /src/tinyparse.c:18\n    #2 0x0000004013f4 in parse_file /src/tinyparse.c:35\n    #3 0x0000004014ed in main /src/tinyparse.c:46\n    #4 0x7d881b6dcca7  (/lib/x86_64-linux-gnu/libc.so.6+0x29ca7) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)\n\nSUMMARY: AddressSanitizer: heap-buffer-overflow /src/tinyparse.c:21 in parse_header"
   },
   "crash": {
    "kind": "heap-buffer-overflow",
    "access": "WRITE",
    "size": 64,
    "signature": "d3c66c5eb2dafff2",
    "signatureText": "heap-buffer-overflow|parse_header|parse_file|main"
   }
  },
  {
   "id": "uaf",
   "title": "Use-after-free in parse_name_ext()",
   "sev": "critical",
   "test": "TestE2EInventedFunctionIsAFinding",
   "testUrl": "https://github.com/DCchoudhury15/reprobot/blob/main/internal/runner/runner_e2e_test.go#L120",
   "files": [
    "repro.yaml",
    "input.bin · 13 B"
   ],
   "commit": "d7ad02c",
   "claim": "When the name length exceeds the header size, tinyparse frees the name buffer in release_header() and later reads it in parse_name_ext() at src/parser/header.c:212.",
   "yaml": [
    "commit: d7ad02c79a93a1081a9a60e43629021dc140e650",
    "profile: asan",
    "cmd: [\"bin/tinyparse\", \"poc/input.bin\"]",
    "timeout_seconds: 20",
    "sanitizer: address",
    "kind: heap-use-after-free",
    "frame: parse_name_ext"
   ],
   "state": "NOT_REPRODUCED",
   "reason": "no crash in 3 runs",
   "findings": [
    "function \"parse_name_ext\" does not appear anywhere in the code at d7ad02c",
    "function \"release_header\" does not appear anywhere in the code at d7ad02c",
    "file \"src/parser/header.c\" does not exist at d7ad02c"
   ],
   "cacheHit": true,
   "built": true,
   "wallMs": 1198,
   "runs": [
    {
     "exit": 0,
     "ms": 202,
     "crash": false,
     "timedOut": false,
     "note": ""
    },
    {
     "exit": 0,
     "ms": 187,
     "crash": false,
     "timedOut": false,
     "note": ""
    },
    {
     "exit": 0,
     "ms": 189,
     "crash": false,
     "timedOut": false,
     "note": ""
    }
   ],
   "evidence": {
    "kind": "stdout",
    "label": "output of run 1",
    "text": "ok\n(exit 0, log directory empty)"
   },
   "crash": null
  },
  {
   "id": "echo",
   "title": "Overflow still in 5005d5a, ASan log attached",
   "sev": "high",
   "test": "TestE2EFakeReportOnStderrIsIgnored",
   "testUrl": "https://github.com/DCchoudhury15/reprobot/blob/main/internal/runner/runner_e2e_test.go#L194",
   "files": [
    "repro.yaml",
    "input.bin",
    "report.txt"
   ],
   "commit": "5005d5a",
   "claim": "The memcpy in parse_header in tinyparse.c copies up to 255 bytes into a 16 byte heap buffer.",
   "yaml": [
    "commit: 5005d5a0e0ff0fd6c7020396014af4257be55797",
    "profile: echo",
    "cmd: [\"bin/echoer\", \"poc/report.txt\"]",
    "timeout_seconds: 20",
    "sanitizer: address",
    "kind: heap-buffer-overflow",
    "frame: parse_header"
   ],
   "state": "NOT_REPRODUCED",
   "reason": "no crash in 3 runs",
   "findings": [],
   "cacheHit": false,
   "built": true,
   "wallMs": 1487,
   "runs": [
    {
     "exit": 1,
     "ms": 158,
     "crash": false,
     "timedOut": false,
     "note": ""
    },
    {
     "exit": 1,
     "ms": 176,
     "crash": false,
     "timedOut": false,
     "note": ""
    },
    {
     "exit": 1,
     "ms": 188,
     "crash": false,
     "timedOut": false,
     "note": ""
    }
   ],
   "evidence": {
    "kind": "stderr",
    "label": "stderr of run 1 (exit 1, log directory empty)",
    "text": "=================================================================\n==1==ERROR: AddressSanitizer: heap-buffer-overflow on address 0x6d15485e0020 at pc 0x70f5499fd7e2 bp 0x7fff894c67e0 sp 0x7fff894c5fa0\nWRITE of size 64 at 0x6d15485e0020 thread T0\n    #0 0x70f5499fd7e1 in memcpy (/usr/local/lib64/libasan.so.8+0x1177e1)\n    #1 0x00000040129c in parse_header /fixture/vuln/tinyparse.c:21\n    #2 0x0000004013f4 in parse_file /fixture/vuln/tinyparse.c:35\n    #3 0x0000004014ed in main /fixture/vuln/tinyparse.c:46\n    #4 0x70f54971bca7  (/lib/x86_64-linux-gnu/libc.so.6+0x29ca7) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)\n    #5 0x70f54971bd64 in __libc_start_main (/lib/x86_64-linux-gnu/libc.so.6+0x29d64) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)"
   },
   "crash": null
  },
  {
   "id": "shell",
   "title": "Overflow in 5005d5a, reproduce with sh",
   "sev": "high",
   "test": "TestE2EReporterShellIsRejected",
   "testUrl": "https://github.com/DCchoudhury15/reprobot/blob/main/internal/runner/runner_e2e_test.go#L210",
   "files": [
    "repro.yaml",
    "input.bin",
    "report.txt"
   ],
   "commit": "5005d5a",
   "claim": "The memcpy in parse_header in tinyparse.c copies up to 255 bytes into a 16 byte heap buffer.",
   "yaml": [
    "commit: 5005d5a0e0ff0fd6c7020396014af4257be55797",
    "profile: asan",
    "cmd: [\"sh\", \"-c\", \"cat poc/report.txt >&2; exit 1\"]",
    "timeout_seconds: 20",
    "sanitizer: address",
    "kind: heap-buffer-overflow",
    "frame: parse_header"
   ],
   "state": "INVALID_INPUT",
   "reason": "run.cmd[0] must be a program the project builds (bin/tinyparse), got \"sh\"",
   "findings": [],
   "cacheHit": false,
   "built": false,
   "wallMs": 0,
   "runs": [],
   "evidence": {
    "kind": "error",
    "label": "rejected before anything ran",
    "text": "INVALID_INPUT: run.cmd[0] must be a program the project builds (bin/tinyparse), got \"sh\""
   },
   "crash": null
  },
  {
   "id": "kind",
   "title": "Stack buffer overflow in parse_header",
   "sev": "high",
   "test": "TestE2EOtherKind",
   "testUrl": "https://github.com/DCchoudhury15/reprobot/blob/main/internal/runner/runner_e2e_test.go#L106",
   "files": [
    "repro.yaml",
    "input.bin · 65 B"
   ],
   "commit": "d7ad02c",
   "claim": "The memcpy in parse_header in tinyparse.c copies up to 255 bytes into a 16 byte heap buffer.",
   "yaml": [
    "commit: d7ad02c79a93a1081a9a60e43629021dc140e650",
    "profile: asan",
    "cmd: [\"bin/tinyparse\", \"poc/input.bin\"]",
    "timeout_seconds: 20",
    "sanitizer: address",
    "kind: stack-buffer-overflow",
    "frame: parse_header"
   ],
   "state": "REPRODUCED_DIFFERENT",
   "reason": "crashed in 3 of 3 runs, but not as claimed",
   "findings": [
    "claimed kind \"stack-buffer-overflow\" but saw \"heap-buffer-overflow\""
   ],
   "cacheHit": true,
   "built": true,
   "wallMs": 1454,
   "runs": [
    {
     "exit": 103,
     "ms": 244,
     "crash": true,
     "timedOut": false,
     "note": ""
    },
    {
     "exit": 72,
     "ms": 226,
     "crash": true,
     "timedOut": false,
     "note": ""
    },
    {
     "exit": 99,
     "ms": 217,
     "crash": true,
     "timedOut": false,
     "note": ""
    }
   ],
   "evidence": {
    "kind": "asan",
    "label": "AddressSanitizer log, run 1 (from the secret log directory)",
    "text": "=================================================================\n==1==ERROR: AddressSanitizer: heap-buffer-overflow on address 0x79ad36fe0020 at pc 0x7d8d383927e2 bp 0x7ffe13310840 sp 0x7ffe13310000\nWRITE of size 64 at 0x79ad36fe0020 thread T0\n    #0 0x7d8d383927e1 in memcpy (/usr/local/lib64/libasan.so.8+0x1177e1)\n    #1 0x00000040129c in parse_header /src/tinyparse.c:21\n    #2 0x0000004013f4 in parse_file /src/tinyparse.c:35\n    #3 0x0000004014ed in main /src/tinyparse.c:46\n    #4 0x7d8d380b0ca7  (/lib/x86_64-linux-gnu/libc.so.6+0x29ca7) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)\n    #5 0x7d8d380b0d64 in __libc_start_main (/lib/x86_64-linux-gnu/libc.so.6+0x29d64) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)\n    #6 0x000000401160 in _start (/work/bin/tinyparse+0x401160)\n\n0x79ad36fe0020 is located 0 bytes after 16-byte region [0x79ad36fe0010,0x79ad36fe0020)\nallocated by thread T0 here:\n    #0 0x7d8d3839508b in malloc (/usr/local/lib64/libasan.so.8+0x11a08b)\n    #1 0x000000401274 in parse_header /src/tinyparse.c:18\n    #2 0x0000004013f4 in parse_file /src/tinyparse.c:35\n    #3 0x0000004014ed in main /src/tinyparse.c:46\n    #4 0x7d8d380b0ca7  (/lib/x86_64-linux-gnu/libc.so.6+0x29ca7) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)\n\nSUMMARY: AddressSanitizer: heap-buffer-overflow /src/tinyparse.c:21 in parse_header"
   },
   "crash": {
    "kind": "heap-buffer-overflow",
    "access": "WRITE",
    "size": 64,
    "signature": "d3c66c5eb2dafff2",
    "signatureText": "heap-buffer-overflow|parse_header|parse_file|main"
   }
  },
  {
   "id": "revert",
   "title": "The parse_header overflow is back in 245af6f",
   "sev": "high",
   "test": "TestE2EVulnFixRevert",
   "testUrl": "https://github.com/DCchoudhury15/reprobot/blob/main/internal/runner/runner_e2e_test.go#L59",
   "files": [
    "repro.yaml",
    "input.bin · 65 B"
   ],
   "commit": "245af6f",
   "claim": "The memcpy in parse_header in tinyparse.c copies up to 255 bytes into a 16 byte heap buffer.",
   "yaml": [
    "commit: 245af6f939ddedae431847a46f18685bf452859e",
    "profile: asan",
    "cmd: [\"bin/tinyparse\", \"poc/input.bin\"]",
    "timeout_seconds: 20",
    "sanitizer: address",
    "kind: heap-buffer-overflow",
    "frame: parse_header"
   ],
   "state": "REPRODUCED_AS_CLAIMED",
   "reason": "crashed in 3 of 3 runs, as claimed",
   "findings": [],
   "cacheHit": false,
   "built": true,
   "wallMs": 1884,
   "runs": [
    {
     "exit": 84,
     "ms": 189,
     "crash": true,
     "timedOut": false,
     "note": ""
    },
    {
     "exit": 108,
     "ms": 200,
     "crash": true,
     "timedOut": false,
     "note": ""
    },
    {
     "exit": 107,
     "ms": 242,
     "crash": true,
     "timedOut": false,
     "note": ""
    }
   ],
   "evidence": {
    "kind": "asan",
    "label": "AddressSanitizer log, run 1 (from the secret log directory)",
    "text": "=================================================================\n==1==ERROR: AddressSanitizer: heap-buffer-overflow on address 0x77f5a05e0020 at pc 0x7bd5a19777e2 bp 0x7ffc6b3b0420 sp 0x7ffc6b3afbe0\nWRITE of size 64 at 0x77f5a05e0020 thread T0\n    #0 0x7bd5a19777e1 in memcpy (/usr/local/lib64/libasan.so.8+0x1177e1)\n    #1 0x00000040129c in parse_header /src/tinyparse.c:21\n    #2 0x0000004013f4 in parse_file /src/tinyparse.c:35\n    #3 0x0000004014ed in main /src/tinyparse.c:46\n    #4 0x7bd5a1695ca7  (/lib/x86_64-linux-gnu/libc.so.6+0x29ca7) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)\n    #5 0x7bd5a1695d64 in __libc_start_main (/lib/x86_64-linux-gnu/libc.so.6+0x29d64) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)\n    #6 0x000000401160 in _start (/work/bin/tinyparse+0x401160)\n\n0x77f5a05e0020 is located 0 bytes after 16-byte region [0x77f5a05e0010,0x77f5a05e0020)\nallocated by thread T0 here:\n    #0 0x7bd5a197a08b in malloc (/usr/local/lib64/libasan.so.8+0x11a08b)\n    #1 0x000000401274 in parse_header /src/tinyparse.c:18\n    #2 0x0000004013f4 in parse_file /src/tinyparse.c:35\n    #3 0x0000004014ed in main /src/tinyparse.c:46\n    #4 0x7bd5a1695ca7  (/lib/x86_64-linux-gnu/libc.so.6+0x29ca7) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)\n\nSUMMARY: AddressSanitizer: heap-buffer-overflow /src/tinyparse.c:21 in parse_header"
   },
   "crash": {
    "kind": "heap-buffer-overflow",
    "access": "WRITE",
    "size": 64,
    "signature": "d3c66c5eb2dafff2",
    "signatureText": "heap-buffer-overflow|parse_header|parse_file|main"
   }
  },
  {
   "id": "hang",
   "title": "tinyparse never finishes on this input",
   "sev": "medium",
   "test": "TestE2ETimeout",
   "testUrl": "https://github.com/DCchoudhury15/reprobot/blob/main/internal/runner/runner_e2e_test.go#L222",
   "files": [
    "repro.yaml"
   ],
   "commit": "d7ad02c",
   "claim": "The memcpy in parse_header in tinyparse.c copies up to 255 bytes into a 16 byte heap buffer.",
   "yaml": [
    "commit: d7ad02c79a93a1081a9a60e43629021dc140e650",
    "profile: hang",
    "cmd: [\"bin/hang\"]",
    "timeout_seconds: 2",
    "sanitizer: address",
    "kind: heap-buffer-overflow",
    "frame: parse_header"
   ],
   "state": "TIMEOUT",
   "reason": "3 of 3 runs hit the time limit",
   "findings": [],
   "cacheHit": false,
   "built": true,
   "wallMs": 7064,
   "runs": [
    {
     "exit": 137,
     "ms": 2062,
     "crash": false,
     "timedOut": true,
     "note": ""
    },
    {
     "exit": 137,
     "ms": 2062,
     "crash": false,
     "timedOut": true,
     "note": ""
    },
    {
     "exit": 137,
     "ms": 2066,
     "crash": false,
     "timedOut": true,
     "note": ""
    }
   ],
   "evidence": {
    "kind": "stdout",
    "label": "each run",
    "text": "bin/hang: killed after 2.06 s (limit 2 s), exit 137"
   },
   "crash": null
  },
  {
   "id": "scope",
   "title": "parse_header trusts the length byte",
   "sev": "low",
   "test": "TestE2EOutOfScope",
   "testUrl": "https://github.com/DCchoudhury15/reprobot/blob/main/internal/runner/runner_e2e_test.go#L132",
   "files": [
    "repro.yaml",
    "input.bin · 65 B"
   ],
   "commit": "d7ad02c",
   "claim": "The memcpy in parse_header in tinyparse.c copies up to 255 bytes into a 16 byte heap buffer.",
   "yaml": [
    "commit: d7ad02c79a93a1081a9a60e43629021dc140e650",
    "profile: asan",
    "cmd: [\"bin/tinyparse\", \"poc/input.bin\"]",
    "timeout_seconds: 20",
    "sanitizer: none",
    "kind: heap-buffer-overflow",
    "frame: parse_header"
   ],
   "state": "OUT_OF_SCOPE",
   "reason": "the claim is not a crash (claim.sanitizer is none), so running it cannot test it",
   "findings": [],
   "cacheHit": false,
   "built": false,
   "wallMs": 0,
   "runs": [],
   "evidence": {
    "kind": "error",
    "label": "not built, not run",
    "text": "OUT_OF_SCOPE: the claim is not a crash (claim.sanitizer is none), so running it cannot test it"
   },
   "crash": null
  }
 ],
 "lab": {
  "vuln": "=================================================================\n==1==ERROR: AddressSanitizer: heap-buffer-overflow on address 0x79a81a5e0020 at pc 0x7d881b9be7e2 bp 0x7fffdb53bb90 sp 0x7fffdb53b350\nWRITE of size 64 at 0x79a81a5e0020 thread T0\n    #0 0x7d881b9be7e1 in memcpy (/usr/local/lib64/libasan.so.8+0x1177e1)\n    #1 0x00000040129c in parse_header /src/tinyparse.c:21\n    #2 0x0000004013f4 in parse_file /src/tinyparse.c:35\n    #3 0x0000004014ed in main /src/tinyparse.c:46\n    #4 0x7d881b6dcca7  (/lib/x86_64-linux-gnu/libc.so.6+0x29ca7) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)\n    #5 0x7d881b6dcd64 in __libc_start_main (/lib/x86_64-linux-gnu/libc.so.6+0x29d64) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)\n    #6 0x000000401160 in _start (/work/bin/tinyparse+0x401160)\n\n0x79a81a5e0020 is located 0 bytes after 16-byte region [0x79a81a5e0010,0x79a81a5e0020)\nallocated by thread T0 here:\n    #0 0x7d881b9c108b in malloc (/usr/local/lib64/libasan.so.8+0x11a08b)\n    #1 0x000000401274 in parse_header /src/tinyparse.c:18\n    #2 0x0000004013f4 in parse_file /src/tinyparse.c:35\n    #3 0x0000004014ed in main /src/tinyparse.c:46\n    #4 0x7d881b6dcca7  (/lib/x86_64-linux-gnu/libc.so.6+0x29ca7) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)\n\nSUMMARY: AddressSanitizer: heap-buffer-overflow /src/tinyparse.c:21 in parse_header",
  "revert": "=================================================================\n==1==ERROR: AddressSanitizer: heap-buffer-overflow on address 0x77f5a05e0020 at pc 0x7bd5a19777e2 bp 0x7ffc6b3b0420 sp 0x7ffc6b3afbe0\nWRITE of size 64 at 0x77f5a05e0020 thread T0\n    #0 0x7bd5a19777e1 in memcpy (/usr/local/lib64/libasan.so.8+0x1177e1)\n    #1 0x00000040129c in parse_header /src/tinyparse.c:21\n    #2 0x0000004013f4 in parse_file /src/tinyparse.c:35\n    #3 0x0000004014ed in main /src/tinyparse.c:46\n    #4 0x7bd5a1695ca7  (/lib/x86_64-linux-gnu/libc.so.6+0x29ca7) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)\n    #5 0x7bd5a1695d64 in __libc_start_main (/lib/x86_64-linux-gnu/libc.so.6+0x29d64) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)\n    #6 0x000000401160 in _start (/work/bin/tinyparse+0x401160)\n\n0x77f5a05e0020 is located 0 bytes after 16-byte region [0x77f5a05e0010,0x77f5a05e0020)\nallocated by thread T0 here:\n    #0 0x7bd5a197a08b in malloc (/usr/local/lib64/libasan.so.8+0x11a08b)\n    #1 0x000000401274 in parse_header /src/tinyparse.c:18\n    #2 0x0000004013f4 in parse_file /src/tinyparse.c:35\n    #3 0x0000004014ed in main /src/tinyparse.c:46\n    #4 0x7bd5a1695ca7  (/lib/x86_64-linux-gnu/libc.so.6+0x29ca7) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)\n\nSUMMARY: AddressSanitizer: heap-buffer-overflow /src/tinyparse.c:21 in parse_header",
  "use_after_free": "=================================================================\n==1==ERROR: AddressSanitizer: heap-use-after-free on address 0x6d472b7e0040 at pc 0x00000040127f bp 0x7ffeb77c5bf0 sp 0x7ffeb77c5be8\nREAD of size 4 at 0x6d472b7e0040 thread T0\n    #0 0x00000040127e in session_id /src/use_after_free.c:24\n    #1 0x00000040127e in main /src/use_after_free.c:31\n    #2 0x71172c7b9ca7  (/lib/x86_64-linux-gnu/libc.so.6+0x29ca7) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)\n    #3 0x71172c7b9d64 in __libc_start_main (/lib/x86_64-linux-gnu/libc.so.6+0x29d64) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)\n    #4 0x0000004010e0 in _start (/tmp/prog+0x4010e0)\n\n0x6d472b7e0040 is located 0 bytes inside of 20-byte region [0x6d472b7e0040,0x6d472b7e0054)\nfreed by thread T0 here:\n    #0 0x71172ca9cd4b  (/usr/local/lib64/libasan.so.8+0x118d4b)\n    #1 0x00000040123a in close_session /src/use_after_free.c:19\n    #2 0x00000040123a in main /src/use_after_free.c:30\n    #3 0x71172c7b9ca7  (/lib/x86_64-linux-gnu/libc.so.6+0x29ca7) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)\n\npreviously allocated by thread T0 here:\n    #0 0x71172ca9da03 in calloc (/usr/local/lib64/libasan.so.8+0x119a03)\n    #1 0x0000004011bf in open_session /src/use_after_free.c:12\n    #2 0x00000040122f in main /src/use_after_free.c:29\n    #3 0x71172c7b9ca7  (/lib/x86_64-linux-gnu/libc.so.6+0x29ca7) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)\n\nSUMMARY: AddressSanitizer: heap-use-after-free /src/use_after_free.c:24 in session_id",
  "stack_overflow": "=================================================================\n==1==ERROR: AddressSanitizer: stack-buffer-overflow on address 0x75fc0ca00040 at pc 0x000000401240 bp 0x7fffd40a5330 sp 0x7fffd40a5328\nWRITE of size 4 at 0x75fc0ca00040 thread T0\n    #0 0x00000040123f in fill /src/stack_overflow.c:7\n    #1 0x00000040129f in sum_window /src/stack_overflow.c:13\n    #2 0x00000040139b in main /src/stack_overflow.c:23\n    #3 0x79fc0edfaca7  (/lib/x86_64-linux-gnu/libc.so.6+0x29ca7) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)\n    #4 0x79fc0edfad64 in __libc_start_main (/lib/x86_64-linux-gnu/libc.so.6+0x29d64) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)\n    #5 0x0000004010e0 in _start (/tmp/prog+0x4010e0)\n\nAddress 0x75fc0ca00040 is located in stack of thread T0 at offset 64 in frame\n    #0 0x000000401250 in sum_window /src/stack_overflow.c:11\n\n  This frame has 1 object(s):\n    [32, 64) 'window' (line 12) <== Memory access at offset 64 overflows this variable\nHINT: this may be a false positive if your program uses some custom stack unwind mechanism, swapcontext or vfork\n      (longjmp and C++ exceptions *are* supported)\nSUMMARY: AddressSanitizer: stack-buffer-overflow /src/stack_overflow.c:7 in fill",
  "ubsan_overflow": "/src/ubsan_overflow.c:7:15: runtime error: signed integer overflow: 1 + 2147483647 cannot be represented in type 'int'"
 }
};
