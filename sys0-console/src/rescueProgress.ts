export type RescueProgress = {
  active: boolean;
  module?: string;
  downloaded: number;
  total: number;
  percent: number;
  completed: number;
  modules: number;
};

export function formatBytes(value: number): string {
  const n = Math.max(0, Number.isFinite(value) ? value : 0);
  if (n < 1024) return `${Math.round(n)} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}

export function progressSummary(p: RescueProgress): string {
  const parts: string[] = [];
  if (p.module) parts.push(p.module);
  if (p.total > 0) parts.push(`${Math.max(0, Math.min(100, Math.round(p.percent)))}%`, `${formatBytes(p.downloaded)} / ${formatBytes(p.total)}`);
  else parts.push(formatBytes(p.downloaded));
  if (p.modules > 0) parts.push(`${Math.min(p.completed, p.modules)}/${p.modules}`);
  return parts.join(" · ");
}
