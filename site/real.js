/* Generated from real runs of eval/libxml2 through reprobot (gcc:15 image,
   Docker, runc) on 2026-11-14. Do not edit by hand. */
window.RB_REAL = {
 "recorded": "2026-11-14",
 "project": "libxml2",
 "image": "gcc:15",
 "real": [
  {
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
     149,
     148,
     142
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
    "line": "if ((node->ns == NULL) || (node->ns->prefix == NULL))"
   },
   "fix": {
    "commit": "71e1e8af5ee46dad1b57bb96cfbf1c3ad21fbd7b",
    "state": "NOT_REPRODUCED",
    "reason": "no crash in 3 runs",
    "crashed": 0,
    "runsMs": [
     113,
     120,
     120
    ],
    "exit": [
     3,
     3,
     3
    ]
   }
  },
  {
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
     135,
     137,
     142
    ],
    "kind": "SEGV",
    "access": "READ",
    "size": 0,
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
    "line": "if ((node->ns == NULL) || (node->ns->prefix == NULL))"
   },
   "fix": {
    "commit": "71e1e8af5ee46dad1b57bb96cfbf1c3ad21fbd7b",
    "state": "NOT_REPRODUCED",
    "reason": "no crash in 3 runs",
    "crashed": 0,
    "runsMs": [
     121,
     110,
     480
    ],
    "exit": [
     3,
     3,
     3
    ]
   }
  },
  {
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
     147,
     145,
     147
    ],
    "kind": "SEGV",
    "access": "READ",
    "size": 0,
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
    "line": "switch (eval->type) {"
   },
   "fix": {
    "commit": "499bcb78ab389f60c2fd634ce410d4bb85c18765",
    "state": "NOT_REPRODUCED",
    "reason": "no crash in 3 runs",
    "crashed": 0,
    "runsMs": [
     123,
     114,
     115
    ],
    "exit": [
     3,
     3,
     3
    ]
   }
  },
  {
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
     150,
     147,
     138
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
    "line": "strcat(buf, (char *) cur->name);"
   },
   "fix": {
    "commit": "8c8753ad5280ee13aee5eec9b0f6eee2ed920f57",
    "state": "NOT_REPRODUCED",
    "reason": "no crash in 3 runs",
    "crashed": 0,
    "runsMs": [
     119,
     116,
     102
    ],
    "exit": [
     3,
     3,
     3
    ]
   }
  },
  {
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
     145,
     147,
     143
    ],
    "kind": "SEGV",
    "access": "READ",
    "size": 0,
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
     126,
     119,
     105
    ],
    "exit": [
     5,
     5,
     5
    ]
   }
  }
 ],
 "fakes": [
  {
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
    110,
    114,
    105
   ],
   "findings": [],
   "stderrHasReport": false
  },
  {
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
    108,
    118,
    116
   ],
   "findings": [
    "function \"xmlParseAttributeBuffer\" does not appear anywhere in the code at 71e1e8a"
   ],
   "stderrHasReport": false
  },
  {
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
    122,
    103,
    122
   ],
   "findings": [],
   "stderrHasReport": true
  }
 ]
};
