---
name: framework-sensitive-data
description: Use in a privacy-sensitive Framework for Stata project, before saving data or outputs, committing, or sharing. Covers the private and public folders.
metadata:
  title: "Framework: sensitive data"
---

# Framework: sensitive data

This project was created as privacy sensitive. Framework keeps private files
in their own folders.

## Folders

- Folders with a `private` segment, such as `inputs/private/` and
  `outputs/private/`, hold private files. Git ignores them.
- In Claude Code, the project's settings deny reading the private folders and
  `.env`.
- `public` folders hold files cleared for sharing.
- `fw_table` and `fw_graph` save to the private output folders by default.
  Add `public` only when the user asks.
