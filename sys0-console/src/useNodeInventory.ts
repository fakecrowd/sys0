import { useCallback, useEffect, useRef, useState } from "react";
import { api, eventStream, getUser, type Node } from "./api";
import { acceptsNodeMetrics } from "./nodeAccess";
import { setRecentNodeAccess } from "./nodeWorkspace";

export function useNodeInventory() {
  const [nodes, setNodes] = useState<Node[]>([]);
  const [live, setLive] = useState<Record<string, any>>({});
  const inventory = useRef<Node[]>([]);
  const generation = useRef(0);
  const accepted = useRef(0);
  const refresh = useCallback(async () => {
    const request = ++generation.current;
    try {
      const result = await api.nodes();
      // Pending polls must not starve successful responses on slow networks.
      // Only a newer committed response (or cleanup) supersedes this request.
      if (request <= accepted.current || !result.ok) return;
      accepted.current = request;
      inventory.current = result.nodes;
      setRecentNodeAccess(localStorage, getUser(), result.nodes.filter((node) => node.canAccess === true).map((node) => node.id));
      setNodes(result.nodes);
      setLive((metrics) => Object.fromEntries(Object.entries(metrics).filter(([id]) => acceptsNodeMetrics(result.nodes, id))));
    } catch { /* Keep the last verified inventory on transport failure. */ }
  }, []);
  useEffect(() => {
    refresh();
    const stream = eventStream((type, data) => {
      // Events lack per-account permission fields. Never merge them into NodeView.
      if (type === "event.node" || type === "event.access") void refresh();
      if (type === "event.metrics" && acceptsNodeMetrics(inventory.current, data.node)) {
        setLive((metrics) => ({ ...metrics, [data.node]: data.metrics }));
      }
    });
    const timer = setInterval(refresh, 5000);
    return () => { accepted.current = ++generation.current; stream.close(); clearInterval(timer); };
  }, [refresh]);
  return { nodes, live, refresh };
}
