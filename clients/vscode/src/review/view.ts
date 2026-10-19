import * as vscode from 'vscode';
import { reviewTree, type ReviewTreeNode } from '../../../shared/review';
import type { components } from '../api/schema';
import type { Cores } from '../cores/registry';
import type { SessionRef } from '../cores/refs';
import { VirtualDocuments } from '../diff/documents';

type Review = components['schemas']['Review'];
type Action = components['schemas']['ReviewAction'];
type FileContent = components['schemas']['ReviewFileContent'];
type Node = { kind: 'message'; text: string } | { kind: 'action'; text: string; action: string } |
  { kind: 'repo'; id: string } | { kind: 'entry'; repo: string; node: ReviewTreeNode } |
  { kind: 'discussions' } | { kind: 'thread'; id: string };
interface Target { ref: SessionRef; revisionId: string; repositoryId?: string; path?: string; side?: 'before' | 'after'; comparison?: boolean }
interface NativeComment extends vscode.Comment { messageId: string; threadId: string }

/** VS Code's own file tree, diff editor, gutter ranges and Comments panel. */
export class Reviews implements vscode.TreeDataProvider<Node>, vscode.Disposable {
  private ref: SessionRef | undefined;
  private review: Review | undefined;
  private revisionId = '';
  private compareTo = '';
  private comparison: Review | undefined;
  private error = '';
  private readonly changed = new vscode.EventEmitter<Node | undefined>();
  readonly onDidChangeTreeData = this.changed.event;
  private readonly view: vscode.TreeView<Node>;
  private readonly documents = new VirtualDocuments('threavia-review');
  private readonly controller = vscode.comments.createCommentController('threavia.review', 'Threavia Review');
  private readonly targets = new Map<string, Target>();
  private readonly nativeThreads = new Map<string, vscode.CommentThread>();
  private readonly threadTargets = new WeakMap<vscode.CommentThread, Target & { threadId?: string }>();
  private readonly disposables: vscode.Disposable[] = [];
  private loading = 0;

  constructor(private readonly cores: Cores) {
    this.view = vscode.window.createTreeView('threavia.reviews', { treeDataProvider: this, showCollapseAll: true });
    this.controller.options = { prompt: 'Save a draft. Sent only when you request corrections.', placeHolder: 'Describe the change you want…' };
    this.controller.commentingRangeProvider = { provideCommentingRanges: (document) => {
      const target = this.targets.get(document.uri.toString());
      if (!target || !this.isCurrent(target.ref) || (target.side === 'before' && target.comparison)) return [];
      return [new vscode.Range(0, 0, Math.max(0, document.lineCount - 1), 0)];
    } };
    this.disposables.push(this.view, this.documents, this.controller, this.changed,
      cores.on('event', (core, event) => {
        if (this.ref?.coreId === core.id && event.sessionId === this.ref.sessionId && ['review.changed', 'job.completed', 'job.failed', 'job.cancelled'].includes(event.type)) void this.reload();
      }), cores.onDidRemove((id) => { if (this.ref?.coreId === id) { this.ref = undefined; this.review = undefined; this.syncComments(); this.changed.fire(undefined); } }));
  }
  dispose() { this.disposables.forEach((value) => { value.dispose(); }); }
  private isCurrent(ref: SessionRef) { return this.ref?.coreId === ref.coreId && this.ref.sessionId === ref.sessionId; }
  private get revision() { return this.review?.revisions.find((revision) => revision.id === this.revisionId) ?? this.review?.revisions.at(-1); }
  private get displayRevision() { return this.compareTo ? this.comparison?.revisions.find((revision) => revision.id === this.revision?.id) : this.revision; }
  private get client() { const client = this.cores.get(this.ref?.coreId)?.client; if (!client || !this.ref) throw new Error('Open a session review first.'); return client; }

  async open(ref: SessionRef) {
    if (!this.isCurrent(ref)) { this.ref = ref; this.revisionId = ''; this.compareTo = ''; this.comparison = undefined; this.review = undefined; }
    await this.reload();
    if (this.review?.repositories.length && !this.review.revisions.length) await this.act({ action: 'refresh' });
    await vscode.commands.executeCommand('threavia.reviews.focus');
  }
  async reload() {
    const ref = this.ref; if (!ref) return;
    const generation = ++this.loading;
    try {
      const result = await this.client.request<Review>('GET', `/api/v1/sessions/${ref.sessionId}/review`);
      if (generation !== this.loading || !this.isCurrent(ref)) return;
      if (!this.review || result.version >= this.review.version) this.review = result;
      this.error = ''; this.view.description = `${result.required ? 'Validation required' : result.status.toLowerCase()} · ${result.threads.flatMap((thread) => thread.messages).filter((message) => message.draft).length} drafts`;
      this.syncComments();
    } catch (error) { if (this.isCurrent(ref)) this.error = error instanceof Error ? error.message : String(error); }
    this.changed.fire(undefined);
  }
  private async act(input: Omit<Action, 'expectedVersion'>) {
    const ref = this.ref; if (!ref || !this.review) throw new Error('Open a session review first.');
    try {
      const result = await this.client.request<Review>('POST', `/api/v1/sessions/${ref.sessionId}/review`, { ...input, expectedVersion: this.review.version });
      if (this.isCurrent(ref) && (!this.review || result.version >= this.review.version)) { this.review = result; this.syncComments(); this.changed.fire(undefined); }
      return result;
    } catch (error) { await this.reload(); throw error; }
  }

  getTreeItem(node: Node): vscode.TreeItem {
    const expandable = node.kind === 'repo' || node.kind === 'discussions' || (node.kind === 'entry' && !node.node.file);
    const item = new vscode.TreeItem('', expandable ? vscode.TreeItemCollapsibleState.Expanded : vscode.TreeItemCollapsibleState.None);
    if (node.kind === 'message') { item.label = node.text; return item; }
    if (node.kind === 'action') { item.label = node.text; item.command = { command: 'threavia.review.action', title: node.text, arguments: [node.action] }; item.iconPath = new vscode.ThemeIcon(node.action === 'refresh' ? 'refresh' : node.action === 'approve' ? 'check' : 'comment-discussion'); }
    if (node.kind === 'repo') { const repo = this.review?.repositories.find((value) => value.id === node.id); item.label = repo?.name ?? 'Repository'; item.description = repo?.directory; item.tooltip = repo?.directory; item.iconPath = new vscode.ThemeIcon('repo'); }
    if (node.kind === 'entry') {
      item.label = node.node.name; item.id = `${this.ref?.coreId}/${this.ref?.sessionId}/${node.repo}/${node.node.path}`;
      item.iconPath = new vscode.ThemeIcon(node.node.file ? 'file-code' : 'folder');
      if (node.node.file) { const tree = this.revision?.repositories.find((repo) => repo.repositoryId === node.repo)?.headTree; item.description = `${node.node.file.state.toLowerCase()}${this.review?.seen[`${node.repo}:${node.node.path}`] === tree ? ' · viewed' : ''}`; item.contextValue = 'reviewFile'; item.command = { command: 'threavia.review.openFile', title: 'Open review diff', arguments: [node] }; }
    }
    if (node.kind === 'discussions') { item.label = 'Discussions'; item.description = `${this.review?.threads.filter((thread) => !thread.resolved).length ?? 0} open`; item.iconPath = new vscode.ThemeIcon('comment-discussion'); }
    if (node.kind === 'thread') { const thread = this.review?.threads.find((value) => value.id === node.id); item.label = thread?.messages[0]?.body.split('\n')[0] ?? 'Discussion'; item.description = `${thread?.resolved ? 'resolved' : 'open'} · ${thread?.path ?? 'review'}`; item.command = { command: 'threavia.review.openThread', title: 'Open discussion', arguments: [node.id] }; item.iconPath = new vscode.ThemeIcon(thread?.resolved ? 'pass' : 'comment'); }
    return item;
  }
  getChildren(node?: Node): Node[] {
    if (!this.ref) return [{ kind: 'message', text: 'Open a session and choose Review Changes.' }];
    if (this.error) return [{ kind: 'message', text: this.error }, { kind: 'action', text: 'Retry loading review', action: 'reload' }];
    const review = this.review; if (!review) return [{ kind: 'message', text: 'Loading review…' }];
    if (!node) return [
      { kind: 'action', text: 'Refresh changes', action: 'refresh' }, { kind: 'action', text: 'Choose revision', action: 'revision' },
      { kind: 'action', text: 'Compare revisions', action: 'compare' }, { kind: 'action', text: 'Add review comment', action: 'comment' },
      ...(review.required ? [] : [{ kind: 'action' as const, text: 'Require human validation', action: 'require' }]),
      { kind: 'action', text: 'Request corrections', action: 'publish' }, { kind: 'action', text: 'Approve review', action: 'approve' },
      ...review.repositories.map((repo): Node => ({ kind: 'repo', id: repo.id })), { kind: 'discussions' },
      ...(!review.repositories.length ? [{ kind: 'message' as const, text: 'Repositories are captured when work starts.' }] : []),
    ];
    if (node.kind === 'repo') { const state = this.displayRevision?.repositories.find((repo) => repo.repositoryId === node.id); return state?.error ? [{ kind: 'message', text: state.error }] : reviewTree(state?.files ?? []).map((entry) => ({ kind: 'entry', repo: node.id, node: entry })); }
    if (node.kind === 'entry' && !node.node.file) return node.node.children.map((entry) => ({ kind: 'entry', repo: node.repo, node: entry }));
    if (node.kind === 'discussions') return review.threads.map((thread) => ({ kind: 'thread', id: thread.id }));
    return [];
  }

  async execute(action: string) {
    if (action === 'reload') return this.reload();
    if (!this.review || !this.ref) return;
    if (action === 'revision' || action === 'compare') {
      const revisions = action === 'compare' ? this.review.revisions.slice(0, this.review.revisions.findIndex((revision) => revision.id === this.revision?.id)) : this.review.revisions;
      const options = revisions.map((revision, index) => ({ label: `Revision ${index + 1}${revision.approved ? ' · approved' : ''}`, value: revision.id }));
      if (action === 'compare') options.unshift({ label: 'Since last approval', value: '' }, { label: 'Since tracking began', value: 'initial' });
      const picked = await vscode.window.showQuickPick(options, { title: action === 'compare' ? 'Comparison baseline' : 'Review revision' }); if (!picked) return;
      if (action === 'compare') this.compareTo = picked.value; else { this.revisionId = picked.value; this.compareTo = ''; }
      this.comparison = undefined;
      if (this.compareTo && this.revision) this.comparison = await this.client.request<Review>('GET', `/api/v1/sessions/${this.ref.sessionId}/review?${new URLSearchParams({ revisionId: this.revision.id, compareTo: this.compareTo })}`);
      this.changed.fire(undefined); return;
    }
    if (action === 'comment') {
      if (!this.revision) return;
      const uri = this.documents.put(`/${this.ref.coreId}/${this.ref.sessionId}/${this.revision.id}/review.md`, '# Review comment\n\nSave a draft below. It is sent with the whole review.\n');
      const target: Target = { ref: this.ref, revisionId: this.revision.id };
      this.targets.set(uri.toString(), target); await vscode.window.showTextDocument(uri);
      const thread = this.controller.createCommentThread(uri, new vscode.Range(0, 0, 0, 0), []); thread.contextValue = 'reviewThread'; thread.collapsibleState = vscode.CommentThreadCollapsibleState.Expanded; this.threadTargets.set(thread, target); return;
    }
    const accepted = new Set(['refresh', 'require', 'publish', 'approve']); if (!accepted.has(action)) return;
    await this.act({ action: action as Action['action'], revisionId: this.revision?.id, ...(action === 'publish' ? { batchId: crypto.randomUUID() } : {}) });
    void vscode.window.showInformationMessage(action === 'approve' ? 'Review approved. The agent may continue; no merge was performed.' : action === 'publish' ? 'Review sent. Corrections are queued in the same session.' : 'Review updated.');
  }
  async openFile(node: Node, revision = this.revision, compareTo = this.compareTo) {
    if (node.kind !== 'entry' || !node.node.file || !revision || !this.ref) return;
    const q = new URLSearchParams({ revisionId: revision.id, repositoryId: node.repo, path: node.node.path, compareTo });
    const content = await this.client.request<FileContent>('GET', `/api/v1/sessions/${this.ref.sessionId}/review/file?${q}`);
    if (content.binary || content.truncated) { void vscode.window.showWarningMessage(`${node.node.path}: ${content.binary ? 'binary file' : 'preview too large'}. Use a file comment.`); return; }
    const at = (side: string) => `/${this.ref!.coreId}/${this.ref!.sessionId}/${revision.id}/${node.repo}/${compareTo || 'base'}/${side}/${node.node.path}`;
    const before = this.documents.put(at('before'), content.before), after = this.documents.put(at('after'), content.after);
    this.targets.set(before.toString(), { ref: this.ref, revisionId: revision.id, repositoryId: node.repo, path: node.node.path, side: 'before', comparison: Boolean(compareTo) });
    this.targets.set(after.toString(), { ref: this.ref, revisionId: revision.id, repositoryId: node.repo, path: node.node.path, side: 'after' });
    this.syncComments(); await vscode.commands.executeCommand('vscode.diff', before, after, `${node.node.path} · review`, { preview: false });
  }
  async saveComment(reply: vscode.CommentReply) {
    const target = this.threadTargets.get(reply.thread) ?? this.targets.get(reply.thread.uri.toString());
    if (!target || !this.isCurrent(target.ref)) throw new Error('Reopen this session review before saving. Your comment was not sent.');
    const threadId = this.threadTargets.get(reply.thread)?.threadId;
    await this.act({ action: 'comment', revisionId: target.revisionId, repositoryId: target.repositoryId, path: target.path, side: target.side,
      ...(!threadId && target.path ? { startLine: reply.thread.range.start.line + 1, endLine: reply.thread.range.end.line + 1 } : {}), threadId, body: reply.text });
    if (!threadId) { reply.thread.dispose(); this.syncComments(); }
  }
  async openThread(id: string) {
    const thread = this.review?.threads.find((item) => item.id === id); if (!thread || !this.ref || !this.review) return;
    const revision = this.review.revisions.find((item) => item.id === thread.revisionId); if (!revision) return;
    if (thread.path && thread.repositoryId) {
      const file = revision.repositories.find((repo) => repo.repositoryId === thread.repositoryId)?.files.find((value) => value.path === thread.path);
      if (file) await this.openFile({ kind: 'entry', repo: thread.repositoryId, node: { name: thread.path, path: thread.path, children: [], file } }, revision, '');
    } else { const uri = this.documents.put(`/${this.ref.coreId}/${this.ref.sessionId}/${revision.id}/review.md`, '# Review discussions\n'); this.targets.set(uri.toString(), { ref: this.ref, revisionId: revision.id }); await vscode.window.showTextDocument(uri); this.syncComments(); }
    const native = this.nativeThreads.get(id); if (native) native.collapsibleState = vscode.CommentThreadCollapsibleState.Expanded;
  }
  async fileAction(node: Node, action: 'seen' | 'comment') {
    if (node.kind !== 'entry' || !node.node.file || !this.revision) return;
    if (action === 'seen') { await this.act({ action: 'seen', revisionId: this.revision.id, repositoryId: node.repo, path: node.node.path }); return; }
    const body = await vscode.window.showInputBox({ title: `Draft comment on ${node.node.path}`, prompt: 'Saved as a draft; sent only with the whole review.', ignoreFocusOut: true });
    if (body?.trim()) await this.act({ action: 'comment', revisionId: this.revision.id, repositoryId: node.repo, path: node.node.path, body });
  }
  async resolve(native: vscode.CommentThread) { const target = this.threadTargets.get(native); if (!target?.threadId) return; const thread = this.review?.threads.find((value) => value.id === target.threadId); await this.act({ action: 'resolve', threadId: target.threadId, resolved: !thread?.resolved }); }
  async deleteDraft(comment: NativeComment) { await this.act({ action: 'delete', threadId: comment.threadId, messageId: comment.messageId }); }
  editDraft(comment: NativeComment) { const thread = this.nativeThreads.get(comment.threadId); if (thread) thread.comments = thread.comments.map((value) => (value as NativeComment).messageId === comment.messageId ? { ...value, mode: vscode.CommentMode.Editing } : value); }
  async saveEditedDraft(comment: NativeComment) {
    try { await this.act({ action: 'edit', threadId: comment.threadId, messageId: comment.messageId, body: typeof comment.body === 'string' ? comment.body : comment.body.value }); }
    catch (error) {
      const thread = this.nativeThreads.get(comment.threadId);
      if (thread) thread.comments = thread.comments.map((value) => (value as NativeComment).messageId === comment.messageId ? { ...value, body: comment.body, mode: vscode.CommentMode.Editing } : value);
      throw error;
    }
  }
  async cancelEditDraft() { await this.reload(); }
  private syncComments() {
    const keep = new Set<string>();
    for (const [uriText, target] of this.targets) {
      if (!this.isCurrent(target.ref)) continue;
      if (target.side === 'before' && target.comparison) continue;
      for (const thread of this.review?.threads ?? []) {
        if (thread.revisionId !== target.revisionId || thread.repositoryId !== target.repositoryId || thread.path !== target.path || (thread.side && thread.side !== target.side)) continue;
        keep.add(thread.id); let native = this.nativeThreads.get(thread.id);
        if (!native) { native = this.controller.createCommentThread(vscode.Uri.parse(uriText), new vscode.Range(Math.max(0, (thread.startLine ?? 1) - 1), 0, Math.max(0, (thread.endLine ?? 1) - 1), 0), []); this.nativeThreads.set(thread.id, native); }
        this.threadTargets.set(native, { ...target, threadId: thread.id }); native.contextValue = 'reviewThread'; native.label = thread.resolved ? 'Resolved' : 'Open discussion'; native.state = thread.resolved ? vscode.CommentThreadState.Resolved : vscode.CommentThreadState.Unresolved;
        native.comments = thread.messages.map((message): NativeComment => ({ body: new vscode.MarkdownString(message.body), mode: vscode.CommentMode.Preview, author: { name: message.author === 'user' ? 'You' : 'Agent' }, label: message.draft ? 'Draft · not sent' : undefined, contextValue: message.draft ? 'reviewDraft' : 'reviewPublished', timestamp: new Date(message.createdAt), messageId: message.id, threadId: thread.id }));
      }
    }
    for (const [id, thread] of this.nativeThreads) if (!keep.has(id)) { thread.dispose(); this.nativeThreads.delete(id); }
  }
}
