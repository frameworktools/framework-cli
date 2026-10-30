---
name: framework-sensitive-data
description: Use before reading data, writing output, committing, or sharing anything in this Framework project with restricted data. Covers which folders are private and how to work without seeing record-level values.
---

# Restricted data

This project holds restricted data, such as identifiable health records.
Record-level values must not leave the machine that is approved to hold them,
and an AI assistant's context counts as leaving.

## Folders

- Folders with a `private` segment, such as `inputs/private/` and
  `outputs/private/`, hold restricted data. They are gitignored and the
  assistant's settings deny reading them.
- `public` folders hold files that someone has reviewed and de-identified.
  Only these may be committed or shared.
- `.env` holds credentials and is never read or printed.

## Working without seeing values

- Do not open, `list`, `browse`, `tabulate`, or `codebook` restricted data in a
  session an assistant can read, because the output shows values.
- Write and check code against the structure instead: variable names, types,
  and labels from `describe`, or the project's data dictionary and simulated
  data when it has them.
- When a run on restricted data is needed, the person runs `do master.do`
  themselves and shares only the error message or the aggregate result.

## Outputs

- Tables and figures go to private output folders first.
- Before anything moves to a public folder, check it for small cells (counts
  below the project's threshold, often 11) and for identifying details such as
  dates, places, or free text.
- Never commit, publish, or paste record-level data.
