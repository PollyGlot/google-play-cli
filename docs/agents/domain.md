# Domain docs

How engineering skills use this repo's domain docs. Single context: one
`CONTEXT.md` and `docs/adr/` at the root.

- **A domain term you need**: find its heading in `CONTEXT.md` (`grep -n "^### <Term>"`)
  and read that entry. Read the whole file only when the task is about the
  glossary itself.
- **An area you will change**: read the ADRs in `docs/adr/` whose title touches it.
- **Naming** (issue titles, proposals, test names): use the glossary term
  verbatim. A missing term is either invented language or a real gap; note the
  gap for `/domain-modeling`.
- **Contradicting an ADR**: say so explicitly (_Contradicts ADR-0007, worth
  reopening because…_), never override it silently.
- A missing file is normal: carry on without flagging it.
- **Before suggesting "we should also add X"**: check deferred surfaces, `gh issue list --label type:parking`, each with its rationale.
