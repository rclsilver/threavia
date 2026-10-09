/**
 * The page a conversation webview loads.
 *
 * A strict policy: nothing loads from anywhere but the extension's own dist
 * folder, the one script runs by its nonce, and no inline style or script the
 * agent could smuggle in through a message would run even if it reached the
 * page. Nothing here imports `vscode`, so the same page renders in a test
 * browser with a stand-in theme.
 */

export function nonce(): string {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';
  let value = '';
  for (let index = 0; index < 32; index++) value += alphabet.charAt(Math.floor(Math.random() * alphabet.length));
  return value;
}

export interface PageOptions {
  /** The webview's own source for the policy (webview.cspSource). */
  cspSource: string;
  scriptUri: string;
  styleUri: string;
  sessionId: string;
  nonce: string;
}

const attribute = (value: string) => value.replace(/[&<>"']/g, (char) => `&#${char.charCodeAt(0)};`);

export function conversationPage(options: PageOptions): string {
  const policy = [
    "default-src 'none'",
    `style-src ${options.cspSource}`,
    `font-src ${options.cspSource}`,
    `img-src ${options.cspSource} data:`,
    `script-src 'nonce-${options.nonce}'`,
  ].join('; ');
  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta http-equiv="Content-Security-Policy" content="${attribute(policy)}">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<link rel="stylesheet" href="${attribute(options.styleUri)}">
<title>Threavia</title>
</head>
<body data-session-id="${attribute(options.sessionId)}">
<script nonce="${attribute(options.nonce)}" src="${attribute(options.scriptUri)}"></script>
</body>
</html>`;
}
