# Domain Docs

How the engineering skills consume this repo's domain documentation.

## Before exploring, read these

- `CONTEXT.md` at the repo root, when it exists.
- Relevant ADRs in `docs/adr/`, when they exist.

Proceed silently when these files are absent. The domain-modeling skill creates them when terms or decisions are resolved.

## File structure

This is a single-context repo. Keep the glossary in root `CONTEXT.md` and architectural decisions in `docs/adr/`.

## Use the glossary's vocabulary

Use terms defined in `CONTEXT.md` in issue titles, proposals, hypotheses, and test names. If a needed term is missing, reconsider the wording or note the gap for domain-modeling.

## Flag ADR conflicts

Surface any conflict with an existing ADR explicitly rather than silently overriding it.
