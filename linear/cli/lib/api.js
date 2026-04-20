import { CLI_NAME } from "./config.js";
import { callTool } from "./mcp.js";

function scaffold(feature) {
  throw new Error(`${CLI_NAME} scaffold: ${feature} is not implemented yet`);
}

export async function searchIssues() {
  return scaffold("searchIssues");
}

export async function getIssue() {
  return scaffold("getIssue");
}

export async function createIssue() {
  return scaffold("createIssue");
}

export async function updateIssue() {
  return scaffold("updateIssue");
}

export async function archiveIssue() {
  return scaffold("archiveIssue");
}

export async function listProjects() {
  return scaffold("listProjects");
}

export async function getProject() {
  return scaffold("getProject");
}

export async function listTeams() {
  return scaffold("listTeams");
}

export async function getTeam() {
  return scaffold("getTeam");
}

export async function listUsers() {
  return scaffold("listUsers");
}

export async function listLabels() {
  return scaffold("listLabels");
}

export async function listStates() {
  return scaffold("listStates");
}

export async function addComment() {
  return scaffold("addComment");
}

export const API_CONTEXT = Object.freeze({
  callTool,
});
