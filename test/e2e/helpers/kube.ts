import { execFileSync } from "node:child_process";

/**
 * Reads a field of a namespaced resource with kubectl, or returns undefined
 * when kubectl checks are disabled (E2E_SKIP_KUBECTL=true) for runs where the
 * test host cannot reach the cluster API.
 */
export function kubectlJsonPath(
  namespace: string,
  resource: string,
  jsonPath: string,
): string | undefined {
  if (process.env.E2E_SKIP_KUBECTL === "true") {
    return undefined;
  }
  return execFileSync(
    "kubectl",
    ["get", resource, "-n", namespace, "-o", `jsonpath=${jsonPath}`],
    { encoding: "utf8" },
  ).trim();
}
