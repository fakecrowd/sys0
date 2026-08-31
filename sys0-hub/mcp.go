package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/fakecrowd/sys0/internal/rpc"
	"github.com/fakecrowd/sys0/internal/wire"
)

// mcpHandler implements a minimal Model Context Protocol server over HTTP,
// exposing the hub's capabilities as tools backed by the dispatch core.
func (h *Hub) mcpHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"service": "sys0-mcp", "transport": "streamable-http"})
		return
	}
	actor, ok := h.actorFromRequest(r)
	if !ok {
		writeJSON(w, http.StatusOK, jrpcErr(nil, -32001, "unauthorized: provide an API key"))
		return
	}
	var req rpc.Message
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeJSON(w, http.StatusOK, jrpcErr(nil, rpc.CodeParse, "parse error"))
		return
	}

	switch req.Method {
	case "initialize":
		writeJSON(w, http.StatusOK, jrpcOK(req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}, "resources": map[string]any{}},
			"serverInfo":      map[string]any{"name": "sys0-hub", "version": version},
		}))
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		writeJSON(w, http.StatusOK, jrpcOK(req.ID, map[string]any{"tools": h.mcpTools()}))
	case "tools/call":
		h.mcpToolsCall(w, req, actor)
	case "resources/list":
		writeJSON(w, http.StatusOK, jrpcOK(req.ID, map[string]any{"resources": []any{
			map[string]any{"uri": "sys0://nodes", "name": "online nodes", "mimeType": "application/json"},
			map[string]any{"uri": "sys0://audit", "name": "audit log", "mimeType": "application/json"},
		}}))
	case "resources/read":
		h.mcpResourcesRead(w, req)
	default:
		writeJSON(w, http.StatusOK, jrpcErr(req.ID, rpc.CodeNoMethod, "unknown method"))
	}
}

func (h *Hub) mcpTools() []map[string]any {
	tools := []map[string]any{{
		"name":        "sys0_list_nodes",
		"description": "列出当前在线的被控端节点。",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
	}}
	for _, m := range wire.NodeMethods {
		props := map[string]any{
			"nodes": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
				"description": "目标节点 id 列表；留空表示全部在线节点"},
		}
		if ps, ok := m.ParamsSchema["properties"].(map[string]any); ok {
			for k, v := range ps {
				props[k] = v
			}
		}
		tools = append(tools, map[string]any{
			"name":        mcpToolName(m.Name),
			"description": m.Description,
			"inputSchema": map[string]any{"type": "object", "properties": props},
		})
	}
	return tools
}

func (h *Hub) mcpToolsCall(w http.ResponseWriter, req rpc.Message, actor Actor) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	json.Unmarshal(req.Params, &p)

	if p.Name == "sys0_list_nodes" {
		writeJSON(w, http.StatusOK, jrpcOK(req.ID, mcpText(h.ListNodes())))
		return
	}

	method := mcpMethodName(p.Name)
	if _, ok := wire.MethodIndex[method]; !ok {
		writeJSON(w, http.StatusOK, jrpcOK(req.ID, mcpErrText("unknown tool: "+p.Name)))
		return
	}

	var args map[string]json.RawMessage
	json.Unmarshal(p.Arguments, &args)
	sel := wire.Select{All: true}
	if raw, ok := args["nodes"]; ok {
		var nodes []string
		json.Unmarshal(raw, &nodes)
		if len(nodes) > 0 {
			sel = wire.Select{Nodes: nodes}
		}
		delete(args, "nodes")
	}
	paramsJSON, _ := json.Marshal(args)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, rerr := h.Dispatch(ctx, actor, wire.DispatchParams{
		Select: sel, Call: wire.Call{Method: method, Params: paramsJSON},
	})
	if rerr != nil {
		writeJSON(w, http.StatusOK, jrpcOK(req.ID, mcpErrText(rerr.Message)))
		return
	}
	writeJSON(w, http.StatusOK, jrpcOK(req.ID, mcpText(res)))
}

func (h *Hub) mcpResourcesRead(w http.ResponseWriter, req rpc.Message) {
	var p struct {
		URI string `json:"uri"`
	}
	json.Unmarshal(req.Params, &p)
	var payload any
	switch p.URI {
	case "sys0://nodes":
		payload = h.ListNodes()
	case "sys0://audit":
		payload, _ = h.store.ListAudit(50)
	default:
		writeJSON(w, http.StatusOK, jrpcErr(req.ID, rpc.CodeBadParams, "unknown resource"))
		return
	}
	b, _ := json.Marshal(payload)
	writeJSON(w, http.StatusOK, jrpcOK(req.ID, map[string]any{"contents": []any{
		map[string]any{"uri": p.URI, "mimeType": "application/json", "text": string(b)},
	}}))
}

// --- helpers ---

func mcpToolName(method string) string { return "sys0_" + strings.ReplaceAll(method, ".", "_") }
func mcpMethodName(tool string) string {
	return strings.ReplaceAll(strings.TrimPrefix(tool, "sys0_"), "_", ".")
}

func mcpText(v any) map[string]any {
	b, _ := json.MarshalIndent(v, "", "  ")
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(b)}}}
}

func mcpErrText(msg string) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": msg}}, "isError": true}
}

func jrpcOK(id string, result any) rpc.Message {
	b, _ := json.Marshal(result)
	return rpc.Message{JSONRPC: "2.0", ID: id, Result: b}
}

func jrpcErr(id any, code int, msg string) rpc.Message {
	var sid string
	if s, ok := id.(string); ok {
		sid = s
	}
	return rpc.Message{JSONRPC: "2.0", ID: sid, Error: &rpc.Error{Code: code, Message: msg}}
}
