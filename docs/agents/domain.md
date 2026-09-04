# Domain docs

Comment les skills d'ingénierie doivent consommer la documentation de
domaine de ce repo en explorant le code.

## À lire avant d'explorer

- **`CONTEXT.md`** à la racine — glossaire des termes canoniques
  (`Edit`, `Account`, `Project`, etc.). **Utiliser ces termes verbatim**
  dans le code, les commentaires, les titres d'issue, les noms de test.
- **`docs/DESIGN.md`** — conventions cross-command (auth precedence,
  exit codes, output formats, edit lifecycle).
- **Les issues `type:parking`** (`gh issue list --label type:parking`) —
  surfaces explicitement reportées, chacune avec son rationnel. Vérifier
  avant de suggérer "on devrait aussi ajouter X".
- **`docs/adr/`** — ADRs touchant l'area du travail en cours.
- **Le tracker GitHub** — état actuel des PRDs et slices (`gh issue list
  --label type:prd`) ; il n'y a pas de fichier de roadmap.

Si l'un de ces fichiers n'existe pas, procéder silencieusement. Ne pas
signaler leur absence en préambule.

## Structure : single-context

```text
/
├── CONTEXT.md
├── CLAUDE.md
├── docs/
│   ├── DESIGN.md
│   ├── CI_CD.md
│   ├── adr/
│   └── agents/
└── (cmd|commands|internal)/
```

Pas de `CONTEXT-MAP.md` — ce repo est mono-contexte.

## Utiliser le vocabulaire du glossaire

Quand la sortie nomme un concept du domaine (titre d'issue, proposition
de refacto, hypothèse, nom de test), utiliser le terme tel que défini
dans `CONTEXT.md`. Ne pas dériver vers des synonymes que le glossaire
évite explicitement.

Si le concept à nommer n'est pas dans le glossaire, c'est un signal —
soit on invente un langage que le projet n'utilise pas (reconsidérer),
soit il y a une vraie lacune (la noter pour `/grill-with-docs`).

## Signaler les conflits ADR

Si la sortie contredit un ADR existant, le surfacer explicitement
plutôt que l'écraser silencieusement :

> _Contredit ADR-0005 (TTY-aware output) — mais à rouvrir parce que…_
