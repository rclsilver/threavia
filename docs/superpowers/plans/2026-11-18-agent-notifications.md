# Notifications levées par les agents — plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Un agent lève une notification INFO ou WARNING rattachée à sa session ; elle apparaît dans une cloche (web et VS Code) jusqu'à ce que l'utilisateur la ferme ou rejoigne la conversation.

**Architecture:** Table `notifications` dans Core, outil agent `notify` + outils de contrôle `notification_list` / `notification_mark_read`, endpoints REST, événements `notification.created` / `notification.read` sur le flux existant (le service les émet avec `s.emit`), Web Push pour WARNING via `notifyOwner`. Les clients invalident un cache `notifications` sur ces événements et affichent une cloche avec compteur.

**Tech Stack:** Go (pgx, migrations SQL), OpenAPI (`api/openapi.yaml`, types TS générés), React + TanStack Query (`web/ui`), extension VS Code (`clients/vscode`).

**Spec:** `docs/superpowers/specs/2026-11-18-agent-notifications-design.md`

## Global Constraints

- Niveaux : exactement `INFO` et `WARNING`.
- Titre ≤ 120 caractères, message ≤ 2000 (tronqués côté Core).
- Pas de limite de débit. Déduplication : une notification **non lue** de même session, niveau, titre et message incrémente `count` et rafraîchit `updated_at` au lieu d'être recréée.
- `notify` sans `RequiresValidation`, ne bloque ni ne réveille jamais un job.
- Push : WARNING seulement, règle de présence inchangée (`broker.AnyActive`), tag = id de session, pas de push si la session est déléguée (`s.delegated`). INFO n'envoie jamais de push.
- Toute notification est créée dans la cloche, même session ouverte : aucune carte dans la conversation, aucun marquage lu automatique. Seuls « Fermer » et « Rejoindre » (depuis la cloche) la marquent lue.
- Tri : WARNING d'abord, puis plus récentes.
- Migration `000039_notifications` (up et down). Suivre `DESIGN.md` pour couleurs et icônes.
- Travail dans la worktree `feat/agent-notifications` ; vérification en docker-compose avec un project-name distinct.

## Review Focus

- Message vide ou titre vide → erreur `ErrInvalid` claire (Task 2).
- Titre/message plus longs que les limites → tronqués, pas d'erreur (Task 2).
- Même alerte levée 50 fois par un schedule → une ligne, `count` = 50 (Task 1).
- Marquer lue deux fois, ou depuis deux appareils → idempotent, un seul `notification.read` (Task 1/2).
- Notification d'une autre personne ou d'une session supprimée → introuvable / supprimée en cascade (Task 1).

---

### Task 1: Domaine, migration et store

**Files:**
- Create: `migrations/000039_notifications.up.sql`, `migrations/000039_notifications.down.sql`
- Create: `internal/core/domain/notification.go`
- Create: `internal/core/storage/postgres/notifications.go`
- Test: `internal/core/storage/postgres/notifications_integration_test.go` (même style que `usage_integration_test.go` : `pgtest.New`, `newFixture`)

**Interfaces:**
- Produces (`domain`):
  `type NotificationID string`; `type NotificationLevel string` avec `NotificationInfo = "INFO"`, `NotificationWarning = "WARNING"`;
  `type Notification struct { ID NotificationID; Scope Scope; Level NotificationLevel; Title, Message string; Count int; CreatedAt, UpdatedAt time.Time; ReadAt *time.Time }` (tags JSON camelCase comme `ValidationRequest` ; `Scope` porte project/session/run/job).
- Produces (`*Store`):
  `CreateNotification(ctx, ownerID domain.UserID, n *domain.Notification) (created bool, err error)` — déduplique sur non lue identique, remplit `ID`, `Count`, dates ;
  `ListNotifications(ctx, ownerID, unreadOnly bool, sessionID domain.SessionID) ([]domain.Notification, error)` — WARNING d'abord, puis `updated_at DESC` ;
  `MarkNotificationRead(ctx, ownerID, id domain.NotificationID) (domain.Notification, changed bool, err error)` — `changed` faux si déjà lue ;
  `MarkNotificationsRead(ctx, ownerID, sessionID domain.SessionID) ([]domain.Notification, error)` — marque toutes les non lues (d'une session si fournie), retourne celles qui changent.

- [ ] **Step 1: Écrire les tests** `TestCreateNotificationDeduplicatesUnreadOnly` (deux créations identiques → une ligne, `Count == 2`, `created` vrai puis faux ; après lecture, une nouvelle identique crée une seconde ligne), `TestListNotificationsOrdersWarningFirst`, `TestMarkNotificationReadIsIdempotentAndOwnerScoped` (second appel `changed == false`, autre propriétaire → `ErrNotFound`), `TestNotificationsFollowSessionDeletion` (cascade).
- [ ] **Step 2: Lancer** `go test ./internal/core/storage/postgres -run Notification -v` — attendu : FAIL (symboles absents).
- [ ] **Step 3: Migration** : table `notifications(id UUID PK, owner_id TEXT, project_id, session_id REFERENCES sessions ON DELETE CASCADE, run_id, job_id, level TEXT CHECK IN ('INFO','WARNING'), title, message, count INT DEFAULT 1, created_at, updated_at, read_at NULL)`, index partiel `(owner_id, updated_at DESC) WHERE read_at IS NULL` et index unique partiel `(session_id, level, md5(title || E'\n' || message)) WHERE read_at IS NULL` pour la dédup (`INSERT … ON CONFLICT … DO UPDATE SET count = notifications.count + 1, updated_at = now()`). `down` supprime la table. Suivre les types d'identifiants des migrations voisines (`000030`, `000011`).
- [ ] **Step 4: Implémenter** domaine et store aux signatures ci-dessus ; `classify(err, …)` pour les erreurs, jointure `projects.owner_id` comme `GetValidationRequest` plutôt qu'une colonne propriétaire redondante si les voisins le font.
- [ ] **Step 5: Relancer** les tests — attendu : PASS.
- [ ] **Step 6: Commit** `feat(core): store agent notifications`.

### Task 2: Service, événements, push et outils agent

**Files:**
- Create: `internal/core/service/notifications.go`, `internal/core/tools/notifications.go`
- Modify: `internal/core/events/types.go` (ajouter `TypeNotificationCreated = "notification.created"`, `TypeNotificationRead = "notification.read"` dans les constantes **et** `persistentTypes`), `internal/core/service/payload.go`, `internal/core/tools/tools.go:299` (ajouter `notificationSpecs()` à la liste), `internal/core/service/coretools.go:105-120` (dispatch), `internal/core/service/managetools.go:22` (`isManageTool`) si les outils de lecture/marquage y vivent
- Test: `internal/core/service/notifications_test.go`, `internal/core/api/managetools_test.go` (étendre)

**Interfaces:**
- Consumes: store de la Task 1.
- Produces (`tools`): `NameNotify Name = "notify"`, `NameNotificationList = "notification_list"`, `NameNotificationMarkRead = "notification_mark_read"`.
- Produces (`service`):
  `(s *Service) Notify(ctx, identity auth.Identity, jc jobScope, level domain.NotificationLevel, title, message string) (domain.Notification, error)` ;
  `(s *Service) Notifications(ctx, identity, unreadOnly bool, sessionID domain.SessionID) ([]domain.Notification, error)` ;
  `(s *Service) MarkNotificationRead(ctx, identity, id domain.NotificationID) error` ;
  `(s *Service) MarkNotificationsRead(ctx, identity, sessionID domain.SessionID) error`.
- Payloads : `NotificationPayload{ NotificationID, Level, Title, Message string; Count int }` (created) et `NotificationReadPayload{ NotificationIDs []string }` (read), scope = session.

- [ ] **Step 1: Tests** : `TestNotifyValidatesAndClips` (titre vide, message vide, level inconnu → `ErrInvalid` ; 300 caractères de titre → 120 stockés), `TestNotifyEmitsCreatedEventAndPushesOnlyWarning` (INFO : événement, aucun envoi ; WARNING : un `notifyOwner` avec `Tag == sessionID`, `URL == "/sessions/<id>"` ; WARNING avec `AnyActive` vrai ou session déléguée : pas de push), `TestMarkNotificationReadEmitsOncePerChange`. Dans `managetools_test.go`, appeler `call("notify", …)` et `call("notification_list", {})`.
- [ ] **Step 2: Lancer** `go test ./internal/core/service ./internal/core/api -run 'Notif|ManageTools' -v` — attendu : FAIL.
- [ ] **Step 3: Spécifications d'outils** dans `internal/core/tools/notifications.go` : `notify` (`level` enum, `title`, `message`, tous requis ; description : « Raise a notification for the person: INFO to report, WARNING when something needs their attention. It does not wait for an answer; use ask_user to ask. »), `notification_list` (`unread` bool défaut vrai, `sessionId`), `notification_mark_read` (`notificationId` ou `sessionId` pour toutes celles d'une session). Aucun `RequiresValidation`.
- [ ] **Step 4: Implémenter** les méthodes de service : nettoyer/tronquer (titre 120, message 2000 sur runes), appeler `store.CreateNotification`, émettre `notification.created` via `s.emit` (aussi pour une dédup : le compteur change), puis push WARNING selon les contraintes globales en réutilisant `notifyOwner` et `clip`. Marquage : émettre `notification.read` seulement si `changed`.
- [ ] **Step 5: Brancher** le dispatch dans `invokeTool`/`manageTool` ; `notification_mark_read` n'agit que sur les notifications du propriétaire du job.
- [ ] **Step 6: Relancer** les tests — attendu : PASS ; puis `go build ./... && go vet ./internal/...`.
- [ ] **Step 7: Commit** `feat(core): notify tool and notification service`.

### Task 3: API REST, contrat et catalogue de contrôle

**Files:**
- Create: `internal/core/api/notifications.go`
- Modify: `api/openapi.yaml` (schéma `Notification`, `NotificationLevel`, trois chemins), `internal/core/api/api.go` (enregistrement des routes avec `h.handle`), `internal/core/api/control_catalog.go` (deux entrées), `docs/api.md`, `docs/control-mcp.md`, `docs/session-management.md`
- Regénérer : `web/ui/src/api/schema.d.ts` (`make web-generate`), `clients/vscode/src/api/schema.d.ts` (`make vscode-generate`)
- Test: `internal/core/api/notifications_test.go` (style de `managetools_test.go` : `setup`, `c.mustDo`)

**Interfaces:**
- Consumes: méthodes de service de la Task 2.
- Produces (HTTP) :
  `GET /api/v1/me/notifications?unread=true&sessionId=…` → liste `Notification` (via `writeList`) ;
  `POST /api/v1/notifications/{notificationId}/read` → 204 ;
  `POST /api/v1/notifications/read` (corps `{sessionId?}`) → 204.
- Catalogue : `{"notification_list", "read", "GET /api/v1/me/notifications"}`, `{"notification_mark_read", "manage", "POST /api/v1/notifications/{notificationId}/read"}`.

- [ ] **Step 1: Test** `TestNotificationEndpoints` : une notification créée via `notify` apparaît dans `GET …?unread=true` triée WARNING d'abord ; `POST …/read` → 204 puis liste `unread=true` vide ; second `POST` → 204 (idempotent) ; id d'un autre utilisateur → 404 ; le catalogue de contrôle (`control_catalog_test.go`) liste les deux nouveaux outils.
- [ ] **Step 2: Lancer** `go test ./internal/core/api -run 'Notification|ControlCatalog' -v` — attendu : FAIL.
- [ ] **Step 3: Ajouter** le contrat OpenAPI puis les handlers (modèle : `markSessionRead`, `pinnedSessions`) et les entrées du catalogue.
- [ ] **Step 4: Regénérer** les types TS (`make web-generate vscode-generate`) ; `make web-generate-check` doit passer.
- [ ] **Step 5: Relancer** les tests Go — attendu : PASS (`go test ./internal/core/...`).
- [ ] **Step 6: Documenter** les outils et endpoints dans les trois docs.
- [ ] **Step 7: Commit** `feat(api): notifications endpoints and control tools`.

### Task 4: Client web — cloche et panneau

**Files:**
- Create: `web/ui/src/components/notification-bell.tsx`, `web/ui/src/routes/notifications.tsx`
- Modify: `web/ui/src/api/keys.ts` (`notifications: () => ['notifications'] as const`), `web/ui/src/api/queries.ts` (`useNotifications`, `useMarkNotificationRead`, `useMarkAllNotificationsRead`), `web/ui/src/api/stream.ts` (cases `notification.created` / `notification.read` → `invalidate({ queryKey: keys.notifications() })`), `web/ui/src/router.tsx`, `web/ui/src/components/app-shell.tsx` (cloche dans l'en-tête, près du lien `/waiting` ligne ~214), `web/ui/src/demo/data.ts` si le mode démo liste les routes
- Test: `web/ui/src/**/*.test.ts(x)` suivant les tests voisins de `attention-cache` / `stream`

**Interfaces:**
- Consumes: `GET /api/v1/me/notifications`, `POST …/read` (Task 3), type `Schemas['Notification']`.
- Produces: route `/notifications` ; `notificationBell` affiche le nombre de non lues, couleur d'alerte si au moins une WARNING non lue.

- [ ] **Step 1: Tests** : le stream invalide `keys.notifications()` sur `notification.created` et `notification.read` ; un tri pur `sortNotifications` (WARNING d'abord, puis `updatedAt` décroissant) ; le composant cloche rend le compteur et la variante WARNING.
- [ ] **Step 2: Lancer** `cd web/ui && npx vitest run notification` — attendu : FAIL.
- [ ] **Step 3: Implémenter** hooks, cloche et route : une ligne par notification (niveau, titre, message, session, `count` « ×N » si > 1, date), actions « Rejoindre » (marque lue puis navigue vers `/sessions/<id>`), « Fermer » (marque lue), « Tout marquer lu ». Mise à jour optimiste de la liste en cache comme `markSessionRead` (`queries.ts:609`).
- [ ] **Step 4: Relancer** vitest, puis `npm run typecheck` et `npm run lint` si présents — attendu : PASS.
- [ ] **Step 5: Commit** `feat(web): notification bell and list`.

### Task 5: Extension VS Code

**Files:**
- Create: `clients/vscode/src/notifications/store.ts`
- Modify: `clients/vscode/src/api/client.ts` (méthodes `notifications()`, `markNotificationRead(id)`, `markNotificationsRead()`), `clients/vscode/src/api/types.ts` (payloads d'événements, à côté de la ligne 114), `clients/vscode/src/api/events.ts` (nouvel effet `{ kind: 'notifications' }` pour les deux types), `clients/vscode/src/tree/provider.ts` et `model.ts` (section « Notifications » de l'arbre, WARNING avec icône d'alerte), `clients/vscode/src/tree/badge.ts` et `model.ts` (`activityBadge` compte aussi les non lues), `clients/vscode/src/extension.ts` et `package.json` (commandes « Rejoindre », « Fermer », « Tout marquer lu »)
- Test: `clients/vscode/test/notifications.test.ts`, étendre `events.test.ts` et `attention.test.ts`

**Interfaces:**
- Consumes: endpoints de la Task 3 ; effet `kind: 'notifications'` produit par `effectsOf`.
- Produces: `NotificationStore` (liste triée, `unreadCount`, rechargé sur l'effet `notifications`) ; commandes `threavia.notification.open|dismiss|dismissAll`.

- [ ] **Step 1: Tests** : `effectsOf(event('notification.created'))` et `notification.read` contiennent `{ kind: 'notifications' }` ; le store trie WARNING d'abord et `unreadCount` ignore les lues ; `activityBadge` additionne les non lues.
- [ ] **Step 2: Lancer** `cd clients/vscode && npx vitest run` (ou la commande de `package.json`) — attendu : FAIL.
- [ ] **Step 3: Implémenter** store, effet, arbre et badge ; « Rejoindre » ouvre la conversation (comme les éléments d'attention) puis marque lue. ⚑ WARNING affiche aussi `vscode.window.showWarningMessage` quand la fenêtre est active (`presence.ts`), avec l'action « Rejoindre ».
- [ ] **Step 4: Relancer** les tests et le typecheck de l'extension — attendu : PASS.
- [ ] **Step 5: Commit** `feat(vscode): notification tree and badge`.

### Task 6: Vérification de bout en bout

**Files:** aucune modification de code sauf correctifs ; docs si le comportement observé diffère.

- [ ] **Step 1:** `go test ./... && make web-generate-check` — attendu : PASS.
- [ ] **Step 2:** Lancer `docker compose -p threavia-notif up -d --build` depuis la worktree ; lancer une session, faire appeler `notify` en INFO puis WARNING (y compris deux fois la même, `count` = 2) ; vérifier dans le web : cloche, ordre, « Fermer », « Rejoindre », mise à jour depuis un second onglet, et qu'aucune carte n'apparaît dans la conversation ouverte.
- [ ] **Step 3:** Vérifier WARNING → Web Push seulement quand aucun client n'est actif.
- [ ] **Step 4:** Rapporter les résultats à l'utilisateur ; ne pas fusionner sans sa demande. Après fusion : `docker compose -p threavia-notif down -v` et suppression de la worktree.
