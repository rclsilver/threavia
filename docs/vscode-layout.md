# VS Code view layout

Threavia already contributes its own Activity Bar container, independently of
Explorer. Keep that container: moving Sessions into Explorer would make agent
work share the height of the file tree, while a second Threavia container would
split related views across two navigation entries.

Sessions is the primary view. Tasks, Memory and Backends start collapsed, so
the session list can use the available sidebar height. VS Code preserves any
layout the user later chooses, including moving these views to the secondary
sidebar. **Threavia: Open Threavia** focuses the dedicated container directly.

Conversations stay in editor tabs. This gives them the editor's height while
keeping the session list available, and lets a file or diff open beside the
conversation. Explorer remains one Activity Bar click away; switching to it
does not close or shrink the conversation. The conversation's **Full width**
toggle controls its reading column within the editor, off by default and
remembered with each panel.
