# Linear CLI

Scaffold package for the Linear tool. Core behavior will be added in later waves.

## Install

```bash
# Via npm
npm i -g @ruminaider/linear-cli

# Or from repo clone
bash install.sh
```

## Reserved command namespaces

- `auth`
- `search`
- `issue`
- `project`
- `team`
- `label`
- `comment`
- `config`
- `mcp`

## Reserved module surface

- `cli/bin/linear.js`: executable entrypoint
- `cli/lib/auth.js`: credentials and login flow
- `cli/lib/mcp.js`: MCP transport helpers
- `cli/lib/api.js`: Linear domain helpers
- `cli/lib/config.js`: shared constants and paths
