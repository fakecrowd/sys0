import { canFocusNode } from "./nodeWorkspace.ts";

type AccessNode = { id: string; state: string; canAccess?: boolean };

export function accessibleFocus(nodes: AccessNode[], focused: string): string {
  return nodes.some((node) => node.id === focused && node.canAccess === true && canFocusNode(node.state)) ? focused : "";
}

export function acceptsNodeMetrics(nodes: AccessNode[], id: string): boolean {
  return nodes.some((node) => node.id === id && node.canAccess === true);
}

export function roleLabel(role: string): string {
  return role === "admin" ? "实例 owner" : "普通用户";
}
