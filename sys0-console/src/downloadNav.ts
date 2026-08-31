export const DOWNLOAD_PATH = "/dl";

export const DOWNLOAD_SECTIONS = [
  {
    kind: "rescue",
    title: "守护/救援端 · sys0-rescue",
    empty: "该 release 暂无 rescue 可执行文件。",
  },
  {
    kind: "agent",
    title: "被控端 · sys0-agent",
    empty: "该 release 暂无 agent 可执行文件。",
  },
] as const;
