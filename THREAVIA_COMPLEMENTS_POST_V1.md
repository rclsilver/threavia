# Threavia — Notes complémentaires, post‑V1 et décisions non reprises dans la spec V1

> **Statut :** document complémentaire à `THREAVIA_SPEC_V1.md`  
> **But :** conserver les décisions, idées, inspirations, points ouverts et éléments post‑V1 discutés pendant la conception de Threavia mais qui ne sont pas explicitement décrits dans la spec V1.
>
> **Règle de priorité :** `THREAVIA_SPEC_V1.md` reste le contrat d’architecture et d’implémentation pour la V1.  
> Ce document ne doit pas être interprété par Claude Code, Codex ou un contributeur comme une demande d’implémenter ces éléments maintenant. En cas de contradiction, la spec V1 prévaut.

---

## 1. Origine et positionnement du produit

Threavia a été pensé comme un **control plane / orchestrateur auto‑hébergé pour agents de code**, avec une priorité forte sur la continuité d’une même session de travail entre plusieurs clients et plusieurs lieux d’exécution.

Le scénario emblématique qui a motivé l’architecture est le suivant :

1. démarrer une session de code depuis un PC avec Claude Code, Codex ou un autre agent ;
2. quitter le PC ;
3. appeler un numéro SIP depuis la voiture ;
4. s’authentifier auprès de Threavia ;
5. sélectionner ou reprendre un Project / Session par langage naturel ;
6. recevoir un résumé vocal du contexte courant ;
7. continuer à piloter le travail, répondre aux questions et aux validations ;
8. basculer une validation complexe ou visuelle sur Android ;
9. reprendre plus tard la même Session depuis Web, VS Code ou Android.

La **Session Threavia** est donc volontairement plus durable et plus large qu’une session native de provider.

---

## 2. Projets existants étudiés et enseignements

### 2.1 Codeoid

Codeoid était le projet existant le plus proche conceptuellement.

Idées jugées intéressantes :

- daemon possédant les sessions ;
- clients relativement stateless ;
- mémoire persistante ;
- worktrees ;
- plusieurs modes d’exécution ;
- possibilité d’injecter des entrées pendant un travail ;
- handoff entre agents.

Décision Threavia :

- reprendre les idées de continuité et de session durable ;
- ne pas reprendre tel quel son modèle d’exécution ;
- garder une séparation forte entre Core et BackendInstance ;
- conserver le filesystem, les credentials provider et les sessions natives côté BackendInstance.

### 2.2 AgentStep Gateway

Idées intéressantes :

- séparation entre l’agent et son environnement ;
- normalisation des événements.

Décision Threavia :

- ne **pas** introduire en V1 une abstraction `Agent` / `Environment` séparée ;
- le BackendInstance est la frontière d’exécution réelle : credentials, filesystem, outils, Git et session native restent ensemble.

### 2.3 llm-cli-gateway

Idées intéressantes :

- jobs durables ;
- validation / receipts d’audit ;
- revue multi‑agent.

Décision Threavia :

- conserver les Jobs comme unités durables ;
- intégrer un reçu d’audit minimal pour les validations ;
- garder la revue multi‑agent pour une phase ultérieure.

### 2.4 Différenciateurs recherchés

Les différenciateurs principaux de Threavia sont :

- BackendInstances autonomes et distants ;
- Core ne détenant jamais les credentials provider ;
- connaissance Project durable et indépendante du provider ;
- Session multi‑client native ;
- Voice / SIP considéré comme client de premier rang ;
- limites de confiance et responsabilités explicites ;
- comportement explicite plutôt que scheduler/handoff “magique”.

---

## 3. UX Voice / SIP détaillée

La Voice n’est pas uniquement un autre affichage de la conversation.

### 3.1 Interaction vocale

Le mode Voice doit privilégier une restitution adaptée à la conduite :

- résumer plutôt que lire du code ;
- ne pas lire des diffs ou logs verbeux mot à mot ;
- transformer les sorties techniques en explications courtes ;
- annoncer clairement quand l’agent attend une validation ou une information ;
- permettre de continuer à orienter le travail par langage naturel.

### 3.2 Backend `INTERACTION`

Une capacité `INTERACTION` peut être utilisée comme couche d’adaptation conversationnelle :

- elle peut être **stateless** ;
- elle reçoit du contexte structuré ;
- elle produit une réponse humanisée ;
- elle n’a pas besoin de posséder une session de code native.

Le provider utilisé pour `INTERACTION` peut être différent du provider utilisé pour `CODE`.

Exemple futur :

```text
Session
├── CODE        → Claude Code sur laptop
└── INTERACTION → modèle vocal/conversationnel distinct
```

### 3.3 Android comme client compagnon de Voice

Le client Voice peut demander explicitement l’aide du client Android.

Exemple :

1. Voice reçoit une ValidationRequest difficile à expliquer oralement ;
2. Core pousse cette validation vers Android ;
3. l’utilisateur ouvre Android et voit le détail technique ;
4. l’utilisateur approuve ou refuse ;
5. Core émet `validation.resolved` ;
6. Voice reprend immédiatement la conversation.

Le principe est que le **premier résultat atomique valide gagne**.

### 3.4 Validations vocales

Toutes les validations ne nécessitent pas Android.

À terme, on veut pouvoir distinguer :

- validations simples pouvant être approuvées oralement ;
- validations qui demandent une interface visuelle ;
- validations sensibles pour lesquelles un canal plus explicite est requis.

La classification exacte n’est pas figée.

---

## 4. Niveaux de validation — point ouvert

Pendant la conception, des niveaux du type suivant ont été évoqués :

```text
INFO
CONFIRM
SENSITIVE
```

Ils n’ont volontairement pas été figés dans la spec V1.

Intention :

- `INFO` : information utile mais aucune décision utilisateur ;
- `CONFIRM` : action pouvant être acceptée/rejetée normalement ;
- `SENSITIVE` : action demandant une présentation plus riche ou un canal de validation plus sûr.

Ce modèle doit être revu avant implémentation.

Questions restant ouvertes :

- niveaux exacts ;
- règles de mapping depuis les providers ;
- actions autorisées par Voice ;
- actions nécessitant Android/Web ;
- relation avec `ExecutionPolicy`.

---

## 5. Handoff avancé entre backends

La V1 garde le changement de backend **explicite**.

Le handoff avancé reste une évolution future.

### 5.1 Handoff context

Lorsqu’une session native ne peut plus être reprise ou qu’un utilisateur choisit un nouveau BackendInstance, le contexte transmis au nouveau Run pourra être construit à partir de :

- historique récent utile de la Session ;
- ProjectContext ;
- workingDirectory ;
- KnownDirectories ;
- Decisions actives ;
- Tasks ouvertes ;
- résumé du travail précédent ;
- fichiers récemment touchés ;
- éventuelles limitations du backend cible.

### 5.2 Skills indisponibles lors d’un handoff

Un BackendInstance peut avoir des Skills privés locaux.

Lors d’un handoff :

- le Core doit savoir qu’un Skill local était utilisé ;
- il doit pouvoir signaler qu’il n’existe pas sur le backend cible ;
- il ne doit jamais tenter de récupérer le contenu privé de ce Skill depuis le backend source.

### 5.3 Pas de migration silencieuse

Même post‑V1, la migration silencieuse n’est pas considérée comme souhaitable par défaut.

L’utilisateur doit pouvoir comprendre :

- pourquoi un autre backend est proposé ;
- quelles capacités sont différentes ;
- quel cwd sera utilisé ;
- quels Skills locaux manqueront ;
- si la session native précédente n’est plus reprenable.

---

## 6. Multi‑agent review / capacité `REVIEW`

`REVIEW` est prévue comme capacité post‑V1.

### 6.1 Review Job

Un Review Job doit être essentiellement **read‑only** et produire des Findings structurés.

Exemple :

```yaml
Finding:
  id: finding-123
  reviewJobId: job-review-1
  severity: ...
  title: ...
  description: ...
  status: OPEN | RESOLVED
```

Le schéma exact de severity reste à définir.

### 6.2 Boucle CODE → REVIEW

Une boucle future possible :

```text
CODE
 ↓
REVIEW
 ↓
Findings
 ↓
CODE corrections
 ↓
REVIEW
 ↓
0 finding
```

Le système devra pouvoir :

- déclencher une review manuellement ;
- éventuellement proposer une review automatiquement ;
- limiter le nombre d’itérations ;
- limiter budget/durée ;
- rattacher Findings et corrections aux Jobs concernés.

### 6.3 Dépendances entre Jobs

Pour ce type de scénario, des dépendances entre Jobs pourront être utiles post‑V1.

Ce graphe est distinct des dépendances de Tasks.

### 6.4 Crit / ChangeSet

Une intégration avec un outil de type Crit / ChangeSet a été envisagée.

Décision :

- ne pas imposer un modèle GitHub Pull Request à la V1 ;
- garder les événements de modifications suffisamment génériques ;
- permettre plus tard à une couche Review de consommer les changements.

---

## 7. Présence des clients et gestion des devices

La spec V1 décrit les principes d’attention, mais l’UX de gestion complète des clients est post‑V1.

### 7.1 Clients enregistrés

À terme, l’utilisateur pourra voir les clients connus, par exemple :

```text
Web — Firefox desktop
Android — Galaxy S24
VS Code — poste de travail
Voice — SIP
```

Actions possibles :

- voir la dernière activité ;
- voir si le client est actif ;
- révoquer un client ;
- gérer les associations de type companion client.

### 7.2 Presence légère

L’objectif n’est pas de créer une lourde abstraction `InteractionContext` en V1.

Le Core peut maintenir le minimum nécessaire :

- client initiateur du Job ;
- client actif/inactif ;
- type de client ;
- relation Voice ↔ Android companion ;
- informations nécessaires aux décisions de notification.

### 7.3 `AttentionItem` comme vue dérivée

Une idée importante :

`AttentionItem` ne doit pas devenir une nouvelle source de vérité.

Il peut être calculé à partir des objets persistants actuellement ouverts :

- ValidationRequest PENDING ;
- UserInputRequest PENDING ;
- autres objets futurs nécessitant une action.

Ainsi, l’état d’attention ne diverge pas de l’état réel.

---

## 8. Notifications externes

Post‑V1, Threavia pourra éventuellement diffuser des notifications vers des systèmes externes :

- Webex ;
- Slack ;
- email ;
- autres systèmes de notification.

Principe à conserver :

- ne pas transformer chaque Event en notification ;
- les notifications doivent être guidées par l’attention courante, la présence et la pertinence pour l’utilisateur.

---

## 9. Project sharing / RBAC / équipe

La V1 reste volontairement simple : isolation par utilisateur.

Évolution envisagée :

- membres d’un Project ;
- rôles ;
- Projects d’équipe ;
- Backends partagés ;
- règles d’accès par Project ;
- éventuellement permissions sur les BackendInstances.

Le modèle devra être introduit sans casser l’isolation V1.

---

## 10. Recherche mémoire par embeddings / vector

La V1 utilise PostgreSQL FTS.

Évolution possible :

- embeddings de l’historique ;
- recherche sémantique ;
- index vectoriel ;
- combinaison lexical + sémantique ;
- éventuellement résumé hiérarchique de très longues Sessions.

Cette évolution ne doit pas supprimer les Decisions/Tasks structurées, qui restent une source de connaissance explicite.

---

## 11. Audit au‑delà des validation receipts

La spec V1 contient un `audit_entries` potentiel mais ne détaille pas toute la politique d’audit envisagée.

Événements candidats à l’audit :

- création / archivage / restauration de Session ;
- création / archivage de Project ;
- enregistrement d’un BackendInstance ;
- claim ;
- connexion / déconnexion ;
- révocation d’un BackendInstance ;
- changements de Tasks ;
- changements de Decisions / mémoire structurée ;
- actions d’authentification ;
- validation / refus d’une ValidationRequest ;
- changements d’ExecutionPolicy.

Principe :

- auditer les actions significatives ;
- ne **pas** journaliser chaque token ou chaque micro‑événement dans une table d’audit dédiée.

---

## 12. Détails supplémentaires sur les événements

### 12.1 Messages structurés

`user.message` et `agent.message` peuvent utiliser des **content blocks** structurés à terme :

- texte ;
- Artifact ;
- image ;
- référence ;
- éventuellement autres types.

### 12.2 Exemple d’événements additionnels

Événements possibles :

```text
backend.offline
backend.online
run.resume_unavailable
artifact.created
skill.installed
client.connected
client.disconnected
```

Tous ne doivent pas nécessairement être persistés.

### 12.3 Éphémère vs persistant

Exemples typiquement éphémères :

- heartbeat ;
- typing/presence ;
- progression très fine ;
- “agent working”.

Exemples persistants :

- message ;
- tool call significatif ;
- validation ;
- résultat ;
- changement d’état ;
- modification de fichier significative.

---

## 13. Backend auth — comportement en cas d’expiration

Lorsque l’auth provider d’un BackendInstance expire :

```text
BackendInstance → DEGRADED
providerAuth    → AUTHENTICATION_REQUIRED
```

Un Job déjà en cours peut passer en :

```text
WAITING_BACKEND
```

Le backend pourra fournir des instructions textuelles, par exemple :

```text
Run `claude /login` on the backend machine.
```

Post‑V1, une abstraction générique de `AuthenticationChallenge` pourrait permettre des flows distants plus intégrés.

---

## 14. Capacité et sélection explicite des backends

Quelques comportements UX discutés :

- lors de la création d’une Session, l’utilisateur sélectionne explicitement un BackendInstance ;
- un changement de backend se fait via une liste de backends compatibles ;
- un backend à capacité maximale ne déclenche pas de migration automatique ;
- il peut être affiché comme temporairement indisponible / en attente ;
- `maxConcurrentRuns=0` sert notamment à drainer volontairement un backend.

Pas de :

- labels/selector de scheduler ;
- scoring automatique ;
- décision silencieuse de placement.

---

## 15. KnownDirectory — cas manquant important

Si la Session possède un `workingDirectory` logique et que le backend choisi n’a aucune binding valide :

**ne jamais lancer le Job silencieusement dans un autre répertoire.**

Le système doit essayer, dans cet ordre conceptuel :

1. résolution déterministe ;
2. recherche dans DiscoveryRoots ;
3. binding d’un chemin exact connu ;
4. proposition de clone si `gitRemote` est disponible ;
5. UserInputRequest demandant le chemin ;
6. choix d’un autre backend.

Le cwd logique n’est pas une simple préférence ignorée en cas d’erreur.

---

## 16. Promotion explicite d’un nouveau répertoire

Un agent peut travailler dans un nouveau répertoire sans que celui‑ci devienne automatiquement un KnownDirectory.

S’il devient utile de le conserver :

```text
known_directory_register(...)
working_directory_set(...)
```

Cela évite que des `/tmp/...`, worktrees temporaires ou répertoires intermédiaires polluent la connaissance durable du Project.

---

## 17. Native session TTL — intention détaillée

Le Core ne doit pas déduire la reprenabilité d’une session native à partir d’un TTL théorique documenté par un provider.

Principe :

- `nativeSessionId` est une référence ;
- le BackendInstance tente la reprise réelle ;
- le résultat réel met à jour `resumeStatus`.

Raisons possibles :

```text
PURGED
NOT_FOUND
INCOMPATIBLE
AUTH_REQUIRED
UNKNOWN
```

Cette liste n’est pas figée.

---

## 18. Compatibilité des Agent Skills — travail restant

Le format Agent Skills (`SKILL.md`, scripts, references, assets) a été retenu comme direction, mais un point reste explicitement à vérifier :

- Claude Code ;
- Codex ;
- Gemini ;
- autres providers possibles.

Avant de figer le contrat V1 final des Skills, il faut vérifier :

- format exact supporté ;
- mécanisme de découverte ;
- capacité à injecter un Skill temporairement ;
- gestion des scripts ;
- résolution des assets ;
- différences de sandbox/permissions.

---

## 19. Providers / backends tiers

Le protocole Backend ↔ Core doit rester indépendant du langage.

Même si le SDK de référence est Go, on veut pouvoir écrire plus tard :

- backend Python ;
- backend Rust ;
- backend TypeScript ;
- backend spécifique à un provider interne.

Le protobuf/gRPC constitue le contrat ; `pkg/backend-sdk` n’est qu’une implémentation de référence.

---

## 20. Applications clientes envisagées

Clients envisagés :

- Web ;
- Android ;
- Voice / SIP ;
- VS Code.

Le Web constitue le premier client d’implémentation.

Android et Voice sont volontairement retardés après le premier vertical slice.

VS Code doit pouvoir arriver sans modifier le modèle métier.

---

## 21. Architecture monorepo — intention générale

Au‑delà de l’arborescence donnée dans la spec V1, les principes de structure étaient :

- démarrer en monorepo ;
- code serveur/backend principalement en Go ;
- protocoles indépendants du langage ;
- éviter les microservices prématurés ;
- UI, Android et Voice peuvent être ajoutés dans le même dépôt ou comme composants clairement versionnés ;
- les adapters provider doivent rester isolés du domaine Core.

---

## 22. Éléments explicitement laissés ouverts

Les sujets suivants ont été identifiés comme non bloquants pour commencer le développement, mais pas totalement figés :

1. niveaux/règles exacts de ValidationRequest ;
2. compatibilité exacte Agent Skills entre Claude/Codex/Gemini ;
3. protocole détaillé du handoff avancé ;
4. modèle exact de Review/Finding ;
5. modèle complet de registered devices ;
6. stratégie embeddings/vector ;
7. modèle RBAC/Project sharing ;
8. providers de notifications externes ;
9. generic remote provider authentication challenge ;
10. éventuelle stratégie de scheduler dans une version très future.

---

## 23. Ordre de priorité après la V1 / après le premier MVP

Ordre conceptuel envisagé, non contractuel :

```text
Fondations MVP
  ↓
Tasks / Decisions / mémoire projet
  ↓
Artifacts + S3
  ↓
Skills
  ↓
Android
  ↓
Voice / SIP + companion Android
  ↓
handoff cross-backend enrichi
  ↓
registered devices / notifications
  ↓
REVIEW / multi-agent
  ↓
RBAC / projets partagés
  ↓
vector search / mémoire avancée
```

L’ordre réel dépendra de l’usage et des besoins rencontrés.

---

## 24. Principes à conserver dans les évolutions futures

Même après la V1 :

- Core ne doit pas récupérer les credentials provider ;
- Backend reste maître du filesystem et de l’exécution réelle ;
- Session reste indépendante du provider ;
- backend selection reste compréhensible par l’utilisateur ;
- l’historique Core doit survivre à la disparition des sessions natives ;
- les clients doivent converger sur la même vérité ;
- les validations résolues ne doivent pas rester actionnables ailleurs ;
- l’automatisation ne doit pas masquer les changements de backend importants ;
- la mémoire structurée (Decisions / Tasks) reste distincte de la simple recherche historique ;
- les fonctionnalités futures ne doivent pas forcer un modèle GitHub/PR spécifique.

---

## 25. Comment utiliser ce document avec un agent de code

Instruction recommandée :

> Lis d’abord `THREAVIA_SPEC_V1.md`, qui est le contrat d’implémentation actuel.  
> Lis ensuite ce document uniquement comme contexte produit, backlog et décisions futures.  
> N’implémente aucun élément post‑V1 ou explicitement différé sans demande explicite.  
> Si ce document semble contredire `THREAVIA_SPEC_V1.md`, la spec V1 prévaut.
