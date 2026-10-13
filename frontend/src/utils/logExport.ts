import { RequestLogEntry } from '../api/types.ts';

function isoOrNull(ts: number): string | null {
  const d = new Date(ts);
  return Number.isNaN(d.getTime()) ? null : d.toISOString();
}

/**
 * The JSON that "Export JSON" downloads: exactly the rows currently displayed (after the search
 * and status filters), in display order, each with an ISO timestamp added. If the filters leave
 * nothing on screen the export is an empty array, never the unfiltered log.
 */
export function buildLogExport(displayed: RequestLogEntry[]) {
  return displayed.map((l) => ({ ...l, isoTimestamp: isoOrNull(l.timestamp) }));
}

export function exportFileName(now: Date = new Date()): string {
  return `infermesh-request-log-${now.toISOString().replace(/[:.]/g, '-')}.json`;
}

/** Triggers a browser download of the given JSON text. */
export function downloadJson(text: string, fileName: string): void {
  const blob = new Blob([text], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = fileName;
  document.body.appendChild(a); // some browsers ignore clicks on detached anchors
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000); // revoking immediately can cancel the download
}
