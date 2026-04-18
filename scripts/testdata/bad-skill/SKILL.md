---
name: bad-skill
description: intentionally broken fixture; references a command that does not exist
---

This skill references `navaris sandbox frobnicate` which is not a real command.
Running `navaris sandbox frobnicate --help` should fail.
