/** Actual source line numbers, never indexes into reconstructed diff hunks. */
export interface ReviewLine {
  kind: 'context' | 'added' | 'deleted' | 'hunk';
  text: string;
  before?: number;
  after?: number;
}

export function reviewLines(diff: string): ReviewLine[] {
  const result: ReviewLine[] = [];
  let before = 0, after = 0, inHunk = false;
  for (const line of diff.split('\n')) {
    const match = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line);
    if (match) {
      before = Number(match[1]); after = Number(match[2]); inHunk = true;
      result.push({ kind: 'hunk', text: line });
    } else if (inHunk && line.startsWith('+')) result.push({ kind: 'added', text: line.slice(1), after: after++ });
    else if (inHunk && line.startsWith('-')) result.push({ kind: 'deleted', text: line.slice(1), before: before++ });
    else if (inHunk && line.startsWith(' ')) result.push({ kind: 'context', text: line.slice(1), before: before++, after: after++ });
    else if (line.startsWith('diff --git ')) inHunk = false;
  }
  return result;
}

export function wholeFileLines(text: string, side: 'before' | 'after'): ReviewLine[] {
  if (!text) return [];
  return text.replace(/\n$/, '').split('\n').map((value, i) => ({ kind: 'context', text: value, [side]: i + 1 }));
}

export function splitReviewLines(lines: ReviewLine[]): { before?: ReviewLine; after?: ReviewLine; hunk?: string }[] {
  const result: { before?: ReviewLine; after?: ReviewLine; hunk?: string }[] = [];
  for (let i = 0; i < lines.length;) {
    const line = lines[i];
    if (line.kind === 'hunk') { result.push({ hunk: line.text }); i++; }
    else if (line.kind === 'context') { result.push({ before: line.before ? line : undefined, after: line.after ? line : undefined }); i++; }
    else {
      const removed: ReviewLine[] = [], added: ReviewLine[] = [];
      while (i < lines.length && lines[i].kind === 'deleted') removed.push(lines[i++]);
      while (i < lines.length && lines[i].kind === 'added') added.push(lines[i++]);
      for (let j = 0; j < Math.max(removed.length, added.length); j++) result.push({ before: removed[j], after: added[j] });
    }
  }
  return result;
}

export interface ReviewTreeNode { name: string; path: string; children: ReviewTreeNode[]; file?: { path: string; state: string; oldPath?: string } }

/** A real directory tree; literal paths are preserved even with duplicate names. */
export function reviewTree(files: { path: string; state: string; oldPath?: string }[]): ReviewTreeNode[] {
  const roots: ReviewTreeNode[] = [];
  for (const file of files) {
    let children = roots, prefix = '';
    for (const [i, name] of file.path.split('/').entries()) {
      prefix += (prefix ? '/' : '') + name;
      let node = children.find((child) => child.name === name);
      if (!node) { node = { name, path: prefix, children: [] }; children.push(node); }
      if (i === file.path.split('/').length - 1) node.file = file;
      children = node.children;
    }
  }
  const sort = (nodes: ReviewTreeNode[]) => { nodes.sort((a, b) => Number(Boolean(a.file)) - Number(Boolean(b.file)) || a.name.localeCompare(b.name)); nodes.forEach((node) => sort(node.children)); };
  sort(roots); return roots;
}
