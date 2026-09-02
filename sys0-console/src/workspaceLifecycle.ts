export class GenerationFence {
  private generation = 0;

  begin(): number {
    return ++this.generation;
  }

  invalidate(): void {
    this.generation++;
  }

  current(generation: number, online: boolean): boolean {
    return online && generation === this.generation;
  }
}

function decodeBase64(value: string): string {
  return atob(value);
}

function encodeBase64(value: string): string {
  return btoa(value);
}

export type OutputSnapshot = { data: string; seq: number };
export type OutputEvent = { chunk: string; seq: number };

export function mergeBase64SnapshotAndEvents(snapshot: OutputSnapshot, events: OutputEvent[]): string | null {
  let seq = snapshot.seq;
  let output = snapshot.data ? decodeBase64(snapshot.data) : "";
  for (const event of events.sort((a, b) => a.seq - b.seq)) {
    if (event.seq <= seq) continue;
    if (event.seq !== seq + 1) return null;
    output += decodeBase64(event.chunk);
    seq = event.seq;
  }
  return encodeBase64(output);
}

type PendingSnapshot = { generation: number; events: OutputEvent[] };

export class SnapshotEventBuffer {
  private sequence = 0;
  private pending = new Map<string, PendingSnapshot>();
  private accepted = new Map<string, number>();

  begin(stream: string): number {
    const generation = ++this.sequence;
    this.accepted.delete(stream);
    this.pending.set(stream, { generation, events: [] });
    return generation;
  }

  event(stream: string, chunk: string, seq: number): "buffered" | "accept" | "duplicate" | "gap" {
    const pending = this.pending.get(stream);
    if (pending) {
      pending.events.push({ chunk, seq });
      return "buffered";
    }
    const accepted = this.accepted.get(stream);
    if (accepted === undefined || seq > accepted + 1) return "gap";
    if (seq <= accepted) return "duplicate";
    this.accepted.set(stream, seq);
    return "accept";
  }

  complete(stream: string, generation: number, snapshot: OutputSnapshot): string | null {
    const pending = this.pending.get(stream);
    if (!pending || pending.generation !== generation) return null;
    this.pending.delete(stream);
    const merged = mergeBase64SnapshotAndEvents(snapshot, pending.events);
    if (merged === null) {
      this.accepted.delete(stream);
      return null;
    }
    this.accepted.set(stream, Math.max(snapshot.seq, ...pending.events.map((event) => event.seq)));
    return merged;
  }

  cancelAll(): void { this.pending.clear(); this.accepted.clear(); }
}

export function trimChunkHistory(
  outputs: Record<string, string[]>,
  stream: string,
  chunk: string,
  maxChars = 400_000,
): Record<string, string[]> {
  const next = Object.fromEntries(Object.entries(outputs).map(([key, chunks]) => [key, [...chunks]]));
  const boundedChunk = chunk.length > maxChars ? chunk.slice(-maxChars) : chunk;
  (next[stream] ||= []).push(boundedChunk);
  let total = Object.values(next).reduce((sum, chunks) => sum + chunks.reduce((n, value) => n + value.length, 0), 0);
  for (const key of Object.keys(next)) {
    while (next[key].length > 0 && total > maxChars) {
      total -= next[key][0].length;
      next[key].shift();
    }
    if (next[key].length === 0) delete next[key];
    if (total <= maxChars) break;
  }
  return next;
}
