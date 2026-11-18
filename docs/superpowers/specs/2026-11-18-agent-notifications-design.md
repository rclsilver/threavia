# Notifications levées par les agents

Date : 2026-11-18 — statut : en attente d'approbation

## Intention

Un agent doit pouvoir prévenir l'utilisateur sans lui poser de question ni lui
demander de validation : un outil MCP lève une notification de niveau INFO ou
WARNING. Cas typique cité par l'utilisateur : un schedule qui fait une
vérification régulière et, si quelque chose survient, crée une notification.
Ces notifications sont conservées, mises en évidence dans le web et VS Code, et
se marquent comme lues.

## État actuel

- Seules les questions et validations en attente sont mises en avant
  (`attention_list`, `GET /api/v1/me/attention`, page `waiting` du web, badge et
  arbre VS Code).
- La lecture n'existe que par session (`sessions.read_sequence`,
  `session_mark_read`).
- Le Web Push existe (`internal/core/service/push.go`, `notifyAttention`) avec
  une règle de présence : rien ne sonne si l'utilisateur est actif sur un client.
- Les outils Core sont déclarés dans `internal/core/tools`, exposés aux agents
  par le backend, et au contrôle externe via `control_catalog.go`.

## Objectifs

- Outil agent `notify` : `level` (INFO | WARNING), `title`, `message`.
  Rattaché à la session de l'agent, sans validation ni attente.
- Table `notifications` persistée dans Core (propriétaire, session, job, niveau,
  titre, message, `created_at`, `read_at`).
- Marquer lue : « dismiss » (sans ouvrir) ou « rejoindre » (ouvre la session et
  marque lue). Aussi disponible pour un agent de supervision via un outil
  `notification_list` / `notification_mark_read`.
- Mise en évidence dans le web et VS Code : cloche avec compteur de non lues,
  panneau « Notifications », WARNING visuellement distinct (couleur d'alerte,
  en tête de liste), INFO visible mais discret.
- Mise à jour en direct via le flux existant (création et lecture, y compris
  depuis un autre appareil).

## Non-objectifs

- Pas de réponse ni de validation à une notification (c'est le rôle de
  `ask_user` et des validations).
- Pas de niveau ERROR/CRITICAL, pas d'expiration, pas de regroupement
  automatique dans cette version.
- Pas de canal externe nouveau (e-mail, Slack).
- Les notifications ne remplacent pas la pastille « non lu » des sessions.

## Anti-objectifs

- Un schedule bavard ne doit pas noyer l'utilisateur : la déduplication ci-dessous
  existent pour cela.
- Une notification ne doit jamais bloquer ni réveiller un job.

## Approches considérées

- **A. Table dédiée + liste à part (choisie par l'utilisateur).** Modèle propre,
  sémantique « à lire » distincte de « à traiter » ; coût : un écran et un
  compteur de plus dans chaque client.
- B. Événement de timeline dérivé de `read_sequence` : peu de code mais
  impossible de fermer une notification seule, rien ne ressort hors session.
- C. Troisième type dans `Attention` : réutilise page, badge et flux, mais
  brouille « l'agent attend » et « à lire ». Écartée par l'utilisateur.

## Conception (appels du concepteur marqués ⚑)

- **Outil** : `notify` dans `internal/core/tools`, sans `RequiresValidation`.
  ⚑ Taille : titre ≤ 120, message ≤ 2000 caractères, tronqués côté Core.
  Pas de limite de débit (décision de l'utilisateur). ⚑ Doublon : une notification
  non lue de même session, niveau, titre et message est dédupliquée
  (compteur `count` incrémenté, `updated_at` rafraîchi) plutôt que recréée,
  ce qui protège des schedules répétitifs.
- **Core** : migration 39 `notifications`, store Postgres, méthodes de service,
  endpoints `GET /api/v1/me/notifications` (filtre `unread`, `sessionId`),
  `POST /api/v1/notifications/{id}/read`, `POST /api/v1/notifications/read`
  (tout marquer lu), entrées dans `api/openapi.yaml` et regénération des
  clients. Entrées `control_catalog` (`notification_list` read,
  `notification_mark_read` manage).
- **Flux** : événements `notification.created` et `notification.read` sur le
  flux utilisateur.
- **Push** : ⚑ WARNING envoie un Web Push via `notifyOwner` (règle de présence
  inchangée, tag par session). INFO n'envoie rien. Pas de push si la session est
  déléguée (cohérent avec `delegated`, la notification reste listée).
- **Lecture automatique** : ouvrir la session depuis la notification la marque
  lue. ⚑ Ouvrir la session par un autre chemin ne la marque pas.
- **Web** (`web/ui`) : cloche dans l'en-tête avec compteur, panneau/route
  listant les non lues (WARNING d'abord, puis plus récentes), actions
  « Rejoindre » et « Fermer », « Tout marquer lu ». Cache React Query mis à jour
  par le flux.
- **VS Code** (`clients/vscode`) : vue/section « Notifications » de l'arbre,
  badge, mêmes actions ; réutilise le client API et le flux. ⚑ WARNING peut aussi
  afficher une notification VS Code native si la fenêtre est active.
- **Doc** : `docs/session-management.md`, `docs/control-mcp.md`, `docs/api.md`.

## Tests

Go : store (création, dédup, lecture), service, outil via
`managetools_test.go`, catalogue de contrôle. Web : tests du cache et du
composant. VS Code : tests du store et de l'arbre. Vérification en docker-compose
depuis la worktree avec un project-name distinct.

## Laissé au réalisateur

Nommage exact des icônes et couleurs (suivre `DESIGN.md`), forme précise des
requêtes SQL, découpage en commits.
