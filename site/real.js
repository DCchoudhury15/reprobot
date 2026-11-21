/* Generated from real runs of eval/ through reprobot (gcc:15 image,
   Docker) by REPROBOT_EVAL_SITE=1 go test -tags eval ./eval/.
   Do not edit by hand. */
window.RB_REAL = {
 "recorded": "2026-11-21",
 "image": "gcc:15",
 "real": [
  {
   "project": "libxml2",
   "id": "CVE-2025-49794",
   "dir": "cve-2025-49794",
   "title": "Use after free in schematron reports",
   "input": "test file from the upstream fix commit",
   "cmd": "bin/xmllint --schematron poc/poc.sct poc/poc.xml",
   "files": [
    {
     "name": "poc.sct",
     "bytes": 341
    },
    {
     "name": "poc.xml",
     "bytes": 91
    }
   ],
   "claim": {
    "kind": "heap-use-after-free",
    "frame": "xmlSchematronFormatReport"
   },
   "vuln": {
    "commit": "24d7e15914588cb45e7fb41cbe4fcf785e1a4861",
    "state": "REPRODUCED_AS_CLAIMED",
    "reason": "crashed in 3 of 3 runs, as claimed",
    "crashed": 3,
    "runsMs": [
     369,
     217,
     252
    ],
    "exit": [
     85,
     75,
     120
    ],
    "kind": "heap-use-after-free",
    "access": "READ",
    "size": 8,
    "frames": [
     {
      "fn": "xmlSchematronFormatReport",
      "at": "schematron.c:1442"
     },
     {
      "fn": "xmlSchematronReportSuccess",
      "at": "schematron.c:1577"
     },
     {
      "fn": "xmlSchematronRunTest",
      "at": "schematron.c:1827"
     }
    ],
    "signature": "6eb90cbbb7b55c98",
    "site": "4351afed948bfd59",
    "line": "if ((node-\u003ens == NULL) || (node-\u003ens-\u003eprefix == NULL))"
   },
   "fix": {
    "commit": "71e1e8af5ee46dad1b57bb96cfbf1c3ad21fbd7b",
    "state": "NOT_REPRODUCED",
    "reason": "no crash in 3 runs",
    "crashed": 0,
    "runsMs": [
     288,
     242,
     243
    ],
    "exit": [
     3,
     3,
     3
    ]
   }
  },
  {
   "project": "libxml2",
   "id": "CVE-2025-49796",
   "dir": "cve-2025-49796",
   "title": "Type confusion in schematron reports",
   "input": "test file from the upstream fix commit",
   "cmd": "bin/xmllint --schematron poc/poc.sct poc/poc.xml",
   "files": [
    {
     "name": "poc.sct",
     "bytes": 275
    },
    {
     "name": "poc.xml",
     "bytes": 33
    }
   ],
   "claim": {
    "kind": "SEGV",
    "frame": "xmlSchematronFormatReport"
   },
   "vuln": {
    "commit": "24d7e15914588cb45e7fb41cbe4fcf785e1a4861",
    "state": "REPRODUCED_AS_CLAIMED",
    "reason": "crashed in 3 of 3 runs, as claimed",
    "crashed": 3,
    "runsMs": [
     247,
     257,
     247
    ],
    "exit": [
     102,
     117,
     79
    ],
    "kind": "SEGV",
    "access": "READ",
    "frames": [
     {
      "fn": "xmlSchematronFormatReport",
      "at": "schematron.c:1442"
     },
     {
      "fn": "xmlSchematronReportSuccess",
      "at": "schematron.c:1577"
     },
     {
      "fn": "xmlSchematronRunTest",
      "at": "schematron.c:1827"
     }
    ],
    "signature": "2d093e7202494122",
    "site": "4351afed948bfd59",
    "line": "if ((node-\u003ens == NULL) || (node-\u003ens-\u003eprefix == NULL))"
   },
   "fix": {
    "commit": "71e1e8af5ee46dad1b57bb96cfbf1c3ad21fbd7b",
    "state": "NOT_REPRODUCED",
    "reason": "no crash in 3 runs",
    "crashed": 0,
    "runsMs": [
     267,
     274,
     245
    ],
    "exit": [
     3,
     3,
     3
    ]
   }
  },
  {
   "project": "libxml2",
   "id": "CVE-2025-49795",
   "dir": "cve-2025-49795",
   "title": "NULL read with an unknown XPath function",
   "input": "test file from the upstream fix commit, with its broken first tag closed",
   "cmd": "bin/xmllint --schematron poc/poc.sct poc/poc.xml",
   "files": [
    {
     "name": "poc.sct",
     "bytes": 260
    },
    {
     "name": "poc.xml",
     "bytes": 98
    }
   ],
   "claim": {
    "kind": "SEGV",
    "frame": "xmlSchematronFormatReport"
   },
   "vuln": {
    "commit": "069bcda17d8194e9582c64dd4bc9dac99b015810",
    "state": "REPRODUCED_AS_CLAIMED",
    "reason": "crashed in 3 of 3 runs, as claimed",
    "crashed": 3,
    "runsMs": [
     280,
     256,
     262
    ],
    "exit": [
     105,
     116,
     98
    ],
    "kind": "SEGV",
    "access": "READ",
    "frames": [
     {
      "fn": "xmlSchematronFormatReport",
      "at": "schematron.c:1457"
     },
     {
      "fn": "xmlSchematronReportSuccess",
      "at": "schematron.c:1572"
     },
     {
      "fn": "xmlSchematronRunTest",
      "at": "schematron.c:1822"
     }
    ],
    "signature": "2d093e7202494122",
    "site": "8c5533765c12b81a",
    "line": "switch (eval-\u003etype) {"
   },
   "fix": {
    "commit": "499bcb78ab389f60c2fd634ce410d4bb85c18765",
    "state": "NOT_REPRODUCED",
    "reason": "no crash in 3 runs",
    "crashed": 0,
    "runsMs": [
     275,
     227,
     257
    ],
    "exit": [
     3,
     3,
     3
    ]
   }
  },
  {
   "project": "libxml2",
   "id": "CVE-2025-24928",
   "dir": "cve-2025-24928",
   "title": "Stack overflow in DTD error messages",
   "input": "written for this evaluation from the fix diff",
   "cmd": "bin/xmllint --noout --valid poc/poc.xml",
   "files": [
    {
     "name": "poc.xml",
     "bytes": 12080
    }
   ],
   "claim": {
    "kind": "stack-buffer-overflow",
    "frame": "xmlSnprintfElements"
   },
   "vuln": {
    "commit": "5880a9a6bd97c0f9ac8fc4f30110fe023f484746",
    "state": "REPRODUCED_AS_CLAIMED",
    "reason": "crashed in 3 of 3 runs, as claimed",
    "crashed": 3,
    "runsMs": [
     304,
     273,
     299
    ],
    "exit": [
     66,
     74,
     97
    ],
    "kind": "stack-buffer-overflow",
    "access": "WRITE",
    "size": 4001,
    "frames": [
     {
      "fn": "xmlSnprintfElements",
      "at": "valid.c:5016"
     },
     {
      "fn": "xmlValidateElementContent",
      "at": "valid.c:5309"
     },
     {
      "fn": "xmlValidateOneElement",
      "at": "valid.c:5961"
     }
    ],
    "signature": "00f99f190f5aa435",
    "site": "c4e43423f9cfad4d",
    "line": "strcat(buf, (char *) cur-\u003ename);"
   },
   "fix": {
    "commit": "8c8753ad5280ee13aee5eec9b0f6eee2ed920f57",
    "state": "NOT_REPRODUCED",
    "reason": "no crash in 3 runs",
    "crashed": 0,
    "runsMs": [
     275,
     249,
     245
    ],
    "exit": [
     3,
     3,
     3
    ]
   }
  },
  {
   "project": "libxml2",
   "id": "CVE-2023-28484",
   "dir": "cve-2023-28484",
   "title": "NULL read in schema type fixup",
   "input": "test file from the upstream fix commit",
   "cmd": "bin/xmllint --noout --schema poc/poc.xsd poc/poc.xml",
   "files": [
    {
     "name": "poc.xsd",
     "bytes": 712
    },
    {
     "name": "poc.xml",
     "bytes": 45
    }
   ],
   "claim": {
    "kind": "SEGV",
    "frame": "xmlSchemaFixupComplexType"
   },
   "vuln": {
    "commit": "cb1b8b8516ade9add9f63fa0e39eaa3bc7034828",
    "state": "REPRODUCED_AS_CLAIMED",
    "reason": "crashed in 3 of 3 runs, as claimed",
    "crashed": 3,
    "runsMs": [
     287,
     287,
     291
    ],
    "exit": [
     110,
     117,
     68
    ],
    "kind": "SEGV",
    "access": "READ",
    "frames": [
     {
      "fn": "xmlSchemaFixupComplexType",
      "at": "xmlschemas.c:18661"
     },
     {
      "fn": "xmlSchemaFixupComponents",
      "at": "xmlschemas.c:21333"
     },
     {
      "fn": "xmlSchemaParse",
      "at": "xmlschemas.c:21522"
     }
    ],
    "signature": "57624be594b90e0c",
    "site": "ea22bdb6c3faf933",
    "line": "(xmlSchemaTreeItemPtr) xmlSchemaAddParticle(pctxt,"
   },
   "fix": {
    "commit": "e4f85f1bd2eb34d9b49da9154a4cc3a1bc284f68",
    "state": "NOT_REPRODUCED",
    "reason": "no crash in 3 runs",
    "crashed": 0,
    "runsMs": [
     239,
     245,
     247
    ],
    "exit": [
     5,
     5,
     5
    ]
   }
  },
  {
   "project": "jq",
   "id": "CVE-2023-50246",
   "dir": "cve-2023-50246",
   "title": "Heap overflow printing a huge negative exponent",
   "input": "the regression test added to tests/shtest in the fix commit",
   "cmd": "bin/jq . poc/num.json",
   "files": [
    {
     "name": "num.json",
     "bytes": 16
    }
   ],
   "claim": {
    "kind": "heap-buffer-overflow",
    "frame": "jvp_literal_number_literal"
   },
   "vuln": {
    "commit": "c9a51565214eece8f1053089739aea73145bfd6b",
    "state": "REPRODUCED_AS_CLAIMED",
    "reason": "crashed in 3 of 3 runs, as claimed",
    "crashed": 3,
    "runsMs": [
     264,
     251,
     259
    ],
    "exit": [
     90,
     76,
     117
    ],
    "kind": "heap-buffer-overflow",
    "access": "WRITE",
    "size": 1,
    "frames": [
     {
      "fn": "decToString",
      "at": "decNumber.c:3764"
     },
     {
      "fn": "decNumberToString",
      "at": "decNumber.c:449"
     },
     {
      "fn": "jvp_literal_number_literal",
      "at": "jv.c:644"
     }
    ],
    "signature": "37277a99860961d5",
    "site": "ba42b95f8fe6a92f",
    "line": "*c='\\0'; // terminate the string (all paths)"
   },
   "fix": {
    "commit": "71c2ab509a8628dbbad4bc7b3f98a64aa90d3297",
    "state": "NOT_REPRODUCED",
    "reason": "no crash in 3 runs",
    "crashed": 0,
    "runsMs": [
     260,
     244,
     237
    ],
    "exit": [
     0,
     0,
     0
    ]
   }
  },
  {
   "project": "jq",
   "id": "CVE-2023-50268",
   "dir": "cve-2023-50268",
   "title": "Stack overflow comparing a NaN with a payload",
   "input": "the regression test added to tests/shtest in the fix commit",
   "cmd": "bin/jq 1 != . poc/nan.json",
   "files": [
    {
     "name": "nan.json",
     "bytes": 8
    }
   ],
   "claim": {
    "kind": "stack-buffer-overflow",
    "frame": "jvp_number_cmp"
   },
   "vuln": {
    "commit": "c5fd64ba975ee35df17bb0c335efb00bffce4d91",
    "state": "REPRODUCED_AS_CLAIMED",
    "reason": "crashed in 3 of 3 runs, as claimed",
    "crashed": 3,
    "runsMs": [
     268,
     262,
     280
    ],
    "exit": [
     74,
     97,
     71
    ],
    "kind": "stack-buffer-overflow",
    "access": "WRITE",
    "size": 2,
    "frames": [
     {
      "fn": "decNumberCopy",
      "at": "decNumber.c:3375"
     },
     {
      "fn": "decNaNs",
      "at": "decNumber.c:7706"
     },
     {
      "fn": "decCompareOp",
      "at": "decNumber.c:6085"
     }
    ],
    "signature": "3bcf0a0095071c74",
    "site": "9921752f71ae03e4",
    "line": "for (s=src-\u003elsu+1; s\u003csmsup; s++, d++) *d=*s;"
   },
   "fix": {
    "commit": "c9a51565214eece8f1053089739aea73145bfd6b",
    "state": "NOT_REPRODUCED",
    "reason": "no crash in 3 runs",
    "crashed": 0,
    "runsMs": [
     250,
     247,
     255
    ],
    "exit": [
     0,
     0,
     0
    ]
   }
  }
 ],
 "fakes": [
  {
   "project": "libxml2",
   "dir": "false-long-text",
   "about": "harmless long text, claims a heap overflow in a real function",
   "commit": "71e1e8af5ee46dad1b57bb96cfbf1c3ad21fbd7b",
   "claim": {
    "kind": "heap-buffer-overflow",
    "frame": "xmlParseCharDataInternal"
   },
   "state": "NOT_REPRODUCED",
   "reason": "no crash in 3 runs",
   "runsMs": [
    240,
    226,
    251
   ],
   "findings": [],
   "stderrHasReport": false
  },
  {
   "project": "libxml2",
   "dir": "false-made-up-function",
   "about": "claims a stack overflow in a function that does not exist",
   "commit": "71e1e8af5ee46dad1b57bb96cfbf1c3ad21fbd7b",
   "claim": {
    "kind": "stack-buffer-overflow",
    "frame": "xmlParseAttributeBuffer"
   },
   "state": "NOT_REPRODUCED",
   "reason": "no crash in 3 runs",
   "runsMs": [
    266,
    252,
    239
   ],
   "findings": [
    "function \"xmlParseAttributeBuffer\" does not appear anywhere in the code at 71e1e8a"
   ],
   "stderrHasReport": false
  },
  {
   "project": "libxml2",
   "dir": "false-pasted-report",
   "about": "the input is a pasted sanitizer report that xmllint prints back",
   "commit": "71e1e8af5ee46dad1b57bb96cfbf1c3ad21fbd7b",
   "claim": {
    "kind": "heap-buffer-overflow",
    "frame": "xmlParseStartTag2"
   },
   "state": "NOT_REPRODUCED",
   "reason": "no crash in 3 runs",
   "runsMs": [
    232,
    234,
    218
   ],
   "findings": [],
   "stderrHasReport": true
  },
  {
   "project": "jq",
   "dir": "false-small-exponent",
   "about": "a harmless number, claims the CVE-2023-50246 heap overflow",
   "commit": "c9a51565214eece8f1053089739aea73145bfd6b",
   "claim": {
    "kind": "heap-buffer-overflow",
    "frame": "jvp_literal_number_literal"
   },
   "state": "NOT_REPRODUCED",
   "reason": "no crash in 3 runs",
   "runsMs": [
    248,
    256,
    236
   ],
   "findings": [],
   "stderrHasReport": false
  },
  {
   "project": "jq",
   "dir": "false-made-up-function",
   "about": "claims a heap overflow in a function that does not exist",
   "commit": "c9a51565214eece8f1053089739aea73145bfd6b",
   "claim": {
    "kind": "heap-buffer-overflow",
    "frame": "jvp_number_scientific_print"
   },
   "state": "NOT_REPRODUCED",
   "reason": "no crash in 3 runs",
   "runsMs": [
    253,
    241,
    257
   ],
   "findings": [
    "function \"jvp_number_scientific_print\" does not appear anywhere in the code at c9a5156"
   ],
   "stderrHasReport": false
  }
 ]
};
