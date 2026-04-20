# Lessons

- Distinguish commit messages from PR descriptions. For commit messages, prefer a concise one-line subject unless the user explicitly asks for a body. Put fuller explanatory detail in the PR description or release notes instead.
- When the user asks whether Pi and Claude Code skills are updated, do not infer from the repo skill alone. Check the installed Pi skill and the installed Claude Code skill separately, and answer each part explicitly.
- Do not commit generated context artifacts like `context.md`. Add them to `.gitignore` before they can be staged or committed.
