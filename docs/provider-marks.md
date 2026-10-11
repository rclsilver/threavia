# Provider marks

The clients bundle the provider's own mark rather than a generic icon.

- Claude: `resources/claude-logo.svg` from Anthropic's official `anthropic.claude-code` VS Code extension, version 2.1.296. Original shape and orange fill preserved.
- Codex: icon from OpenAI's official `openai.chatgpt` VS Code extension, downloaded from https://openai.gallery.vsassets.io/_apis/public/gallery/publisher/openai/extension/chatgpt/latest/assetbyname/Microsoft.VisualStudio.Services.Icons.Default on 2026-10-11. Original PNG preserved. The official extension currently uses the OpenAI knot.

Identical copies live in `web/ui/src/assets/providers` and `clients/vscode/media/providers`. Web builds bundle them locally; editor webviews embed them as data URLs, and native tree/picker rows use packaged resources. No provider-hosted image is loaded at runtime.

These marks identify the provider and remain their owners' trademarks.
