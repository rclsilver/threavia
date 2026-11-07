# Consignes par niveaux (agent et superviseur)

Date : 2026-11-07 — statut : en attente d'approbation

## Intention

L'utilisateur veut écrire des consignes pour l'agent (prompt système) et pour
le superviseur (mode SUPERVISED) à plusieurs niveaux — global, projet, backend —
pour compléter ou corriger ce que dit le niveau du dessus. Le niveau le plus
bas l'emporte, et le LLM doit en être prévenu explicitement.

## État actuel

- **Agent** : `SystemPrompt` (`internal/backends/shared/runner/prompt.go`) =
  texte Threavia fixe + `ProjectInstructions` (projet, Core, enrichi par Core
  de la délégation, du handoff…) + `LOCAL_INSTRUCTIONS` (variable
  d'environnement du backend, ajoutée en dernier, sans UI). Aucun niveau global.
- **Superviseur** : champ `Supervision` de `ExecutionPolicy`
  (`internal/core/domain/policy.go`), politiques à 5 niveaux instance → backend
  → projet → session → job. `Effective` prend le niveau le plus étroit **en
  bloc** : un projet qui a sa propre politique (ex. pour changer de mode)
  remplace la supervision globale, même par un texte vide. Le backend la
  préfixe à chaque message (`withSupervision` Claude, `supervision.go` Codex),
  car le reviewer ne lit que les messages utilisateur.

## Objectifs

- Consignes agent et superviseur à trois niveaux persistés dans Core :
  global (par utilisateur), projet, backend ; plus session et job pour le
  superviseur (déjà existants).
- Ordre, du plus large au plus étroit : **global → projet → backend →
  session → job** (choix utilisateur : la machine a le dernier mot sur le
  projet). `LOCAL_INSTRUCTIONS` reste la couche la plus basse de l'agent.
- Chaque niveau **complète** les précédents ; une correction s'écrit comme un
  complément (« contrairement à la règle globale… »).
- Le texte assemblé annonce : les consignes viennent de plusieurs niveaux, en
  conflit le plus bas l'emporte. Pour le superviseur et pour l'agent.
- La supervision n'est plus écrasée par un changement de mode d'une politique.

## Non-objectifs

- Mode « remplacer » ou règles globales non remplaçables (écarté pour
  l'instant par l'utilisateur).
- Surcharger le texte Threavia fixe (ask_user, scratch, mémoire, monitors…).
- Changer le protocole backend ou exiger une mise à jour des backends.
- Niveau croisé projet×backend.

## Anti-objectifs

- Une consigne écrite à un niveau qui disparaît silencieusement.
- Des consignes qui ne parviennent plus à des backends déjà déployés.

## Approches considérées

1. **Core assemble le texte, protocole inchangé** — retenue. Core rend deux
   textes sectionnés et les place dans `ProjectContext.project_instructions`
   et `ExecutionPolicy.supervision`. Aucun backend à redéployer. Limite : le
   backend voit un bloc, pas des niveaux ; si un jour un backend doit traiter
   un niveau à part, on passera à l'approche 2.
2. Core envoie une liste structurée `{niveau, libellé, texte}` dans le
   protocole — plus évolutif, mais nouvelle version de protocole, mise à jour
   de chaque backend et compatibilité à gérer.
3. Table générique de couches `(portée, cible, type, texte)` — évite une
   migration pour un futur niveau, mais plus abstrait pour trois niveaux.

## Design

### Stockage (migration 000037)

- Global : table `instance_instructions(owner_id PK, agent text, supervision text, updated_at)`.
- Projet : `projects.instructions` existe (agent) ; ajouter
  `projects.supervision_instructions text not null default ''`.
- Backend : `backend_instances.agent_instructions` et
  `backend_instances.supervision_instructions`, texte, défaut `''`.
  Les consignes sont celles du propriétaire du backend (même modèle que
  `backend_instances.execution_policy`).
- Session / job : la supervision reste portée par leur `execution_policy`
  (voir plus bas).
- Limite de longueur : 4000 caractères par nouveau texte, comme `Supervision`
  aujourd'hui. Pas de limite rétroactive sur `projects.instructions`.
- Le `PATCH` projet traite le nouveau champ comme un pointeur (absent =
  inchangé, chaîne vide = effacé).

**Migration des données** : la `supervision` des politiques instance, backend
et projet est copiée dans le nouveau champ du même niveau puis retirée de la
politique (JSONB). Celle des politiques session et job reste en place.

### Sémantique de `ExecutionPolicy.Supervision`

`Supervision` ne fait plus partie du « bloc » remplacé par `Effective` :
- aux niveaux instance/backend/projet, le champ n'est plus accepté (validation
  refuse, l'API dirige vers les nouveaux endpoints) ;
- aux niveaux session et job, il reste accepté et devient une couche
  supplémentaire, ajoutée sous le backend ;
- `Effective` ne propage plus `Supervision` ; c'est l'assembleur ci-dessous qui
  produit la valeur finale envoyée au backend.

**Ce que contient `Supervision` dans une politique calculée** (choix du
concepteur) : uniquement la supervision **propre** aux niveaux session/job
(job si défini, sinon session), jamais le texte hérité. Ainsi `EffectivePolicy`
copié par la délégation (`sessiontools.go`), stocké par les monitors, ou
pré-rempli par « Surcharger » (`policy-panel.tsx`, `permissions/page.ts`) ne
recopie jamais l'héritage. Les vues `SessionPolicyView` / `PolicyView`
gagnent un champ séparé (ex. `supervisionAssembled`) avec le texte final que
recevra le reviewer, en lecture seule.

**Migration des sessions/jobs** (choix du concepteur) : une supervision de
session ou de job strictement égale (après trim) à la supervision héritée
calculée avant migration est vidée — elle reste au niveau du dessus, rien
n'est perdu — et chaque suppression est journalisée dans l'audit. Les textes
différents sont conservés comme couche `Session` / `Job`.

**Sessions déléguées** (décision utilisateur : A) : une session enfant
créée par `session_create` reçoit **seulement ses propres niveaux** (global →
projet de l'enfant → backend). La copie de la politique du manager ne doit
plus transporter de `Supervision` : le code de délégation retire ce champ de
la politique copiée. La note de délégation ajoutée dans `jobs.go` reste
inchangée, hors sections. Une consigne transversale relève du niveau global.

**Jobs en cours** (choix du concepteur) : une modification des consignes
globales, projet ou backend s'applique au prochain job ; pas de diffusion
`UpdateJobPolicy` vers les jobs actifs. Une modification de politique de
session continue d'être poussée comme aujourd'hui, avec la supervision
réassemblée.

### Assemblage (Core)

Fonction pure dans `internal/core/domain` (ex. `instructions.go`) :
`Layered(kind, layers []Layer) string` avec `Layer{Label, Text}`, couches vides
ignorées.

- 0 couche non vide → chaîne vide (comportement actuel : pas de préfixe).
- 1 seule couche → son texte, sans en-tête ni avertissement (pas de bruit
  quand rien n'est empilé).
- ≥ 2 couches → avertissement puis sections :

```
These instructions come from several levels, broadest first. When two of
them conflict, the later (narrower) level wins.

[Global]
…
[Project threavia]
…
[Backend thomas-zenbook-claude]
…
```

Libellés : `Global`, `Project <nom>`, `Backend <nom affiché>`, `Session`,
`Job`. Texte en anglais, comme le reste des prompts.

- **Agent** : dans `projectContext` (`internal/core/service/jobs.go`), la base
  `jc.ProjectInstructions` devient le rendu global → projet → backend du Run ;
  les ajouts dynamiques existants (délégation, handoff, MCP indisponibles…)
  restent concaténés après, hors sections.
  `LOCAL_INSTRUCTIONS` reste ajouté par le backend après (inchangé) ; s'il y a
  des couches empilées, l'avertissement mentionne qu'une couche « machine »
  peut suivre — à défaut, l'assembleur peut ajouter une phrase générique
  « Any instructions appended after these are narrower still. »
- **Superviseur** : dans `policyToProto` / la construction de la politique du
  job, `supervision` = rendu global → projet → backend → session → job. Même
  chemin pour `UpdateJobPolicy` (changement de politique en cours de job).
- Le backend du Run est celui du job (`runs.backend_instance_id`), comme pour
  `EffectivePolicy`.

Les backends (Claude/Codex) ne changent pas : ils préfixent déjà
`Supervision` à chaque message avec « Standing instructions for this
project… ». Le libellé du préfixe peut rester ; à reconsidérer seulement si le
texte « for this project » devient trompeur (appréciation du builder).

### API

- `GET/PUT /api/v1/me/instructions` → `{agent, supervision}`.
- `GET/PUT /api/v1/backends/{backendId}/instructions` → `{agent, supervision}`.
- Projet : `supervisionInstructions` ajouté au modèle projet (lecture /
  `PATCH` existant), à côté de `instructions`.
- OpenAPI mis à jour, schémas TS régénérés (web et vscode).
- Audit : chaque écriture journalisée (`instructions.set`, avec niveau).
- Optionnel mais utile : `GET /api/v1/projects/{id}/instructions/preview?backendId=`
  qui renvoie le texte final agent et superviseur, pour voir ce que le LLM
  recevra.

### Outils MCP

`project_read` / `project_update` exposent aussi `supervisionInstructions`
(même validation utilisateur que `instructions`). Pas de nouvel outil pour
global/backend dans cette itération.

### Interface web

- Réglages globaux (`default-policy.tsx`) et page backend : deux zones de
  texte « Consignes de l'agent » / « Consignes du superviseur ».
- Page projet : la zone « instructions » existante + une zone superviseur.
- Le champ supervision disparaît des formulaires de politique instance,
  backend et projet (`policy-form.tsx`), reste pour session/job.
- Sous chaque zone, rappel du texte hérité des niveaux du dessus (lecture
  seule) pour que l'on sache ce qu'on complète.

VS Code : seulement la régénération du schéma et le retrait du champ
supervision là où il n'a plus cours (`clients/vscode/src/permissions/page.ts`).

## Laissé au builder

- Noms exacts des colonnes/endpoints et découpage en commits.
- Formulation exacte de l'avertissement et des libellés.
- Forme de l'aperçu (endpoint dédié ou calcul côté client).
- Tests : unitaires sur l'assembleur (0/1/n couches, ordre, couches vides),
  sur la migration, et un test d'API vérifiant que la supervision globale
  survit à une politique projet qui change seulement le mode.
