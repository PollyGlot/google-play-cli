# Issue tracker: GitHub

Issues et PRDs vivent dans GitHub Issues sur `PollyGlot/google-play-cli`.
Utiliser le CLI `gh` pour toute opération.

## Conventions de base

- **Créer une issue** : `gh issue create --title "..." --body "$(cat <<'EOF' ... EOF)"`
- **Lire une issue** : `gh issue view <n> --comments`
- **Lister** : `gh issue list --state open --json number,title,labels,body`
- **Commenter** : `gh issue comment <n> --body "..."`
- **Labels** : `gh issue edit <n> --add-label "..."` / `--remove-label "..."`
- **Fermer** : `gh issue close <n> --comment "..."`

## Taxonomie d'issue locale (lire avant tout)

Quatre types d'issue cohabitent. Ils se distinguent par un label `type:*`
et une convention de titre. Voir [docs/ROADMAP.md](../ROADMAP.md) pour la
vue d'ensemble du projet.

| Type        | Label          | Convention de titre                                   |
| ----------- | -------------- | ----------------------------------------------------- |
| **PRD**     | `type:prd`     | `PRD: <area>` ou `PRD — <area>`                        |
| **Slice**   | `type:slice`   | libre (souvent préfixé par l'area, ex: `Apps: ...`)    |
| **Arch**    | `type:arch`    | `[arch] ...`                                          |
| **Parking** | `type:parking` | `Parking: ...`                                        |

Chaque issue doit aussi porter un `area:*` (`area:auth`, `area:apps`,
`area:releases`, `area:tracks`, `area:reviews`) et une `priority:*`
(`priority:high`/`priority:medium`/`priority:low`).

## Quand un skill dit "publish to the issue tracker"

Créer une issue GitHub. Appliquer les labels appropriés :

- **`/to-prd`** → ajoute `type:prd` + le bon `area:*` + `ready-for-agent`.
- **`/to-issues`** → chaque slice publiée porte `type:slice` + `area:*` +
  `ready-for-agent`. Référencer le PRD parent dans le body via `## Parent`
  pointant vers `#<num>` du PRD. **Mettre à jour le body du PRD** pour
  ajouter une section "Implementation issues" avec la checklist `- [ ] #N`.
- **`/triage`** → applique le label d'état (`needs-triage`, `needs-info`,
  `ready-for-agent`, `ready-for-human`, `wontfix`) selon le résultat.

## Maintenir la roadmap

Quand un PRD ou une slice est créée/fermée, mettre à jour
[docs/ROADMAP.md](../ROADMAP.md) (diagramme Mermaid + tables).

## Quand un skill dit "fetch the relevant ticket"

`gh issue view <n> --comments`.

## Workflow PR → issue close

Les PRs qui ferment des issues **doivent** contenir une ligne `Closes #N`
**par** issue dans le body (pas le titre). `Closes #19, #26` ne ferme que
`#19`. Voir aussi `~/.claude/CLAUDE.md`.
