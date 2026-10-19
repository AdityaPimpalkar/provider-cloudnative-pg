import { execFileSync } from "node:child_process";

/**
 * Whether the test host can reach the cluster API with kubectl. Set
 * E2E_SKIP_KUBECTL=true when it cannot; cluster checks are then skipped, and
 * reported as skipped.
 */
export const kubectlEnabled = process.env.E2E_SKIP_KUBECTL !== "true";

/** Reads a field of a namespaced resource with kubectl. */
export function kubectlJsonPath(namespace: string, resource: string, jsonPath: string): string {
  return execFileSync(
    "kubectl",
    ["get", resource, "-n", namespace, "-o", `jsonpath=${jsonPath}`],
    { encoding: "utf8" },
  ).trim();
}

/**
 * Deletes an Instance without waiting, ignoring one that is already gone. A
 * no-op when kubectl is disabled.
 */
export function kubectlDeleteInstance(namespace: string, name: string): void {
  if (!kubectlEnabled) {
    return;
  }
  execFileSync(
    "kubectl",
    ["delete", "instance", name, "-n", namespace, "--ignore-not-found", "--wait=false"],
    { encoding: "utf8" },
  );
}
