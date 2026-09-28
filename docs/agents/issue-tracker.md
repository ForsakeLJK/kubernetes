# Issue tracker: Local Markdown

Issues and specs for this repo live as markdown files in `.scratch/`.

## Conventions

- One feature per directory: `.scratch/<feature-slug>/`
- The spec is `.scratch/<feature-slug>/spec.md`
- Implementation issues are one file per ticket at `.scratch/<feature-slug>/issues/<NN>-<slug>.md`, numbered from `01`
- Record state as a `Status:` line near the top of each issue file
- Append comments under a `## Comments` heading

## When a skill says "publish to the issue tracker"

Create a new file under `.scratch/<feature-slug>/`, creating the directory if needed.

## When a skill says "fetch the relevant ticket"

Read the referenced file. The user will normally provide its path or issue number.

## Wayfinding operations

- Map: `.scratch/<effort>/map.md`, containing Notes, Decisions-so-far, and Fog.
- Child ticket: `.scratch/<effort>/issues/NN-<slug>.md`, numbered from `01`. Use `Type:` for `research`, `prototype`, `grilling`, or `task`; use `Status:` for `claimed` or `resolved`.
- Blocking: record `Blocked by: NN, NN` near the top. A ticket is unblocked when every listed ticket is resolved.
- Frontier: scan for open, unblocked, unclaimed tickets; first by number wins.
- Claim: set `Status: claimed` and save before work begins.
- Resolve: append the answer under `## Answer`, set `Status: resolved`, then append a context pointer to the map's Decisions-so-far.
