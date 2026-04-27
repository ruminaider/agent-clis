export function buildAutoAssignedMarker({ by, parent, task, agent, effect } = {}) {
  if (!by) throw new Error("buildAutoAssignedMarker: by is required");
  const parts = [`[auto-assigned by ${sanitizeToken(by)}`, "auto-derived"];
  if (parent) parts.push(`parent=${sanitizeToken(parent)}`);
  if (task) parts.push(`task=${sanitizeToken(task)}`);
  if (agent) parts.push(`agent=${sanitizeToken(agent)}`);
  if (effect) parts.push(`effect=${sanitizeToken(effect)}`);
  return `${parts.join(" ")}]`;
}

function sanitizeToken(value) {
  return String(value).replace(/[^A-Za-z0-9._:@/-]/g, "-");
}

export { sanitizeToken };
