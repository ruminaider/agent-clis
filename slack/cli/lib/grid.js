// Enterprise Grid workspace discovery.
//
// On Grid the Slack desktop app usually caches a single session token: the
// org-level one (`E…`). That token reaches the whole org for search, the user
// directory, and channel reads, but Slack refuses workspace-scoped methods on
// it, so `conversations.list` fails with `enterprise_is_restricted` and the CLI
// has nothing to fall back to.
//
// The signed-in session can mint the missing tokens itself. The org token names
// every workspace the user belongs to (`users.info` → `enterprise_user.teams`,
// then `team.info` for each host), and loading a workspace's web app with the
// same `d` cookie returns a page whose boot data carries that workspace's
// token. Discovery therefore fills in one token per workspace at login, which
// is what makes channel and DM listing work on Grid.
//
// Regular (non-Grid) workspaces report no enterprise id, so nothing here runs
// for them.

import { buildCookieHeader, webApiCall } from "./api.js";

const API_TOKEN_RE = /"api_token"\s*:\s*"(xoxc-[^"]+)"/;

// A dead session cannot be worked around by trying the next workspace, so these
// stop discovery outright rather than producing one warning per workspace.
const SESSION_DEAD_ERRORS = new Set(["invalid_auth", "token_revoked", "account_inactive", "not_authed"]);

// The boot page is the browser-facing web app; ask for it as a browser would.
const BOOT_USER_AGENT =
  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36";

function hostFromUrl(url) {
  if (!url) return null;
  try {
    return new URL(url).host;
  } catch {
    return String(url).replace(/^https?:\/\//, "").split("/")[0] || null;
  }
}

// Read a workspace's own session token out of its web app boot data.
export async function mintWorkspaceToken(host, cookie, cookieDs) {
  const res = await fetch(`https://${host}/`, {
    headers: {
      Cookie: buildCookieHeader({ cookie, cookieDs }),
      "User-Agent": BOOT_USER_AGENT,
      Accept: "text/html,application/xhtml+xml",
    },
    redirect: "follow",
  });
  if (!res.ok) throw new Error(`${host} returned HTTP ${res.status}`);
  const match = (await res.text()).match(API_TOKEN_RE);
  if (!match) {
    throw new Error(
      `${host} served no session token. If every workspace reports this, Slack changed its sign-in page and discovery needs updating.`,
    );
  }
  return match[1];
}

// Add a record for every workspace in the user's Grid org that the desktop app
// did not hand us, including the org-level entry when only workspace tokens
// were found. `verify` turns a raw token into a workspace record (auth.test).
// Discovery is best-effort: a workspace that cannot be reached is reported as a
// warning and the rest still land.
export async function discoverGridWorkspaces(workspaces, cookie, cookieDs, verify) {
  const byId = new Map(workspaces.filter((w) => w.id).map((w) => [w.id, w]));
  const warnings = [];

  // One seed per org: any verified token from that org can name its siblings.
  const seeds = new Map();
  for (const workspace of workspaces) {
    if (workspace.enterprise_id && !seeds.has(workspace.enterprise_id)) {
      seeds.set(workspace.enterprise_id, workspace);
    }
  }

  for (const [enterpriseId, seed] of seeds) {
    const creds = { token: seed.token, cookie, cookieDs, host: seed.host };
    let teamIds = [];
    try {
      const me = await webApiCall("users.info", { user: seed.user_id }, creds);
      teamIds = me.user?.enterprise_user?.teams || [];
    } catch (err) {
      if (SESSION_DEAD_ERRORS.has(err.slackError)) {
        throw new Error(
          `Your Slack session is no longer valid (${err.slackError}). Reopen the Slack app, then re-run \`slack-cli auth login\`.`,
        );
      }
      warnings.push(
        `Could not list the workspaces of org ${enterpriseId}: ${err.message}. Channel and DM listings will cover only the workspaces already stored.`,
      );
    }

    for (const id of [enterpriseId, ...teamIds]) {
      if (byId.has(id)) continue;
      try {
        const info = await webApiCall("team.info", { team: id }, creds);
        const host = hostFromUrl(info.team?.url) || (info.team?.domain ? `${info.team.domain}.slack.com` : null);
        if (!host) throw new Error("Slack returned no URL for it");
        const record = await verify(await mintWorkspaceToken(host, cookie, cookieDs));
        // Slack can answer a workspace URL with the org client, which would hand
        // back the org token and quietly leave this workspace unlisted. Storing
        // it under whatever it turned out to be would hide that.
        if (!record?.id) throw new Error("the minted token verified without a team id");
        if (record.id !== id) throw new Error(`${host} handed back a token for ${record.id}`);
        byId.set(record.id, record);
      } catch (err) {
        if (SESSION_DEAD_ERRORS.has(err.slackError)) {
          throw new Error(
            `Your Slack session is no longer valid (${err.slackError}). Reopen the Slack app, then re-run \`slack-cli auth login\`.`,
          );
        }
        warnings.push(`Could not add workspace ${id}: ${err.message}`);
      }
    }
  }

  return { workspaces: [...byId.values()], warnings };
}
