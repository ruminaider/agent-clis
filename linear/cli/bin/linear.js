#!/usr/bin/env node

import { pathToFileURL } from "node:url";
import { CLI_NAME, COMMAND_NAMESPACES } from "../lib/config.js";

export function printUsage() {
  console.log(`${CLI_NAME} scaffold\n\nUsage:\n  ${CLI_NAME} <namespace> [command]\n  ${CLI_NAME} --help\n\nNamespaces:\n  ${COMMAND_NAMESPACES.join(", ")}\n\nThis package is a scaffold only. Core behavior will be implemented in later waves.`);
}

export async function main(argv = process.argv.slice(2)) {
  const [firstArg] = argv;

  if (!firstArg || firstArg === "-h" || firstArg === "--help") {
    printUsage();
    return 0;
  }

  printUsage();
  return 1;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main()
    .then((code) => {
      process.exit(code);
    })
    .catch((error) => {
      console.error(error instanceof Error ? error.message : String(error));
      process.exit(1);
    });
}
