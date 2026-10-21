// Test-only type surface for Node's built-in VM; the browser app has no Node dependency.
declare module 'node:vm' {
  export function runInNewContext(source: string, context: Record<string, unknown>): unknown
}
